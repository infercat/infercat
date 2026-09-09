package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/logger"
)

// session is one tunnel session held by one friend: a native tailcat client (the path the future
// `connect` command uses) and an HTTP client that dials port 80 through it. With --relay-only the
// process's magicsock has UDP disabled (TS_DEBUG_ALWAYS_USE_DERP), so every byte crosses the relay
// as a browser friend's would. A friend normally holds one session; --sessions-per-friend opens more
// on the same key (the abuse shape).
type session struct {
	friend, idx int
	keyID       string
	secret      string
	client      *tailcat.Client
	http        *http.Client
	history     []msg
	turn        int
	path        string
	connectMS   int64
	connectErr  string
}

// reqRecord is one line of requests.jsonl: what the friend saw. The host's own event for the same
// request (queued_ms, prompt_tokens, the settle) is in host.jsonl; the summary reads both.
type reqRecord struct {
	TS        time.Time `json:"ts"`
	Friend    int       `json:"friend"`
	Session   int       `json:"session"`
	KeyID     string    `json:"key_id"`
	Endpoint  string    `json:"endpoint"`
	Turn      int       `json:"turn"`
	Doc       bool      `json:"doc,omitempty"`
	PromptEst int       `json:"prompt_est"`
	PromptTok int       `json:"prompt_tokens"`
	MaxTokens int       `json:"max_tokens"`
	Status    int       `json:"status"`
	Code      string    `json:"code,omitempty"`
	QueuedMS  int64     `json:"queued_ms"` // client view: first `: queued` comment → first data line (includes prefill)
	TTFTMS    int64     `json:"ttft_ms"`   // client view: request start → first data line
	TotalMS   int64     `json:"total_ms"`
	CompTok   int       `json:"completion_tokens"`
	TokS      float64   `json:"tok_s"`
	BytesUp   int       `json:"bytes_up"`
	BytesDown int       `json:"bytes_down"`
	Path      string    `json:"path"`
	Err       string    `json:"err,omitempty"`
	Aborted   bool      `json:"aborted,omitempty"` // the run ended before the request did
}

func newSession(friend, idx int, keyID, secret, addr string, logf logger.Logf, keepalive bool) *session {
	cl := &tailcat.Client{Server: tailcat.Addr(addr), Logf: logf}
	tr := &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return cl.DialTCPPort(ctx, 80) },
		DisableKeepAlives: !keepalive, // the browser dials per request; so does this by default
	}
	return &session{friend: friend, idx: idx, keyID: keyID, secret: secret, client: cl, http: &http.Client{Transport: tr}}
}

// connect brings the session up the way the browser's connect() does: Ping until the handshake
// answers, bounded by ctx; then label the path.
func (s *session) connect(ctx context.Context) error {
	start := time.Now()
	var last error
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := s.client.Ping(pctx)
		cancel()
		if err == nil {
			s.connectMS = time.Since(start).Milliseconds()
			s.path = s.pathLabel(ctx)
			return nil
		}
		last = err
		if ctx.Err() != nil {
			s.connectMS = time.Since(start).Milliseconds()
			s.connectErr = fmt.Sprintf("%v (last: %v)", ctx.Err(), last)
			return fmt.Errorf("connect: %s", s.connectErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pathLabel is how the client reaches the host right now: "direct <endpoint>" or "relayed via <region>".
func (s *session) pathLabel(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := s.client.DiscoPing(pctx)
	switch {
	case err != nil:
		return "unknown (" + err.Error() + ")"
	case r.Endpoint != "":
		return "direct " + r.Endpoint
	default:
		return "relayed via " + r.DERPRegionCode
	}
}

func (s *session) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://tunnel"+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.http.Do(req)
}

// get is one small request (GET /me, /healthz, /v1/models) recorded like any other.
func (s *session) get(ctx context.Context, path string) (reqRecord, []byte) {
	rec := reqRecord{TS: time.Now(), Friend: s.friend, Session: s.idx, KeyID: s.keyID, Endpoint: path, Path: s.path}
	resp, err := s.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		rec.Err, rec.TotalMS, rec.Aborted = err.Error(), time.Since(rec.TS).Milliseconds(), ctx.Err() != nil
		return rec, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	rec.Status, rec.BytesDown, rec.TotalMS, rec.TTFTMS = resp.StatusCode, len(b), time.Since(rec.TS).Milliseconds(), time.Since(rec.TS).Milliseconds()
	if resp.StatusCode != 200 {
		rec.Code = errCode(b)
	}
	return rec, b
}

// chat sends one streamed chat and reads it to the end (or, for the friend who never reads, holds
// the response open without reading until ctx ends). It returns the record and the reply text.
func (s *session) chat(ctx context.Context, model string, messages []msg, maxTokens int, read bool) (reqRecord, string, int) {
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true, "messages": messages, "max_tokens": maxTokens})
	rec := reqRecord{TS: time.Now(), Friend: s.friend, Session: s.idx, KeyID: s.keyID, Endpoint: "/v1/chat/completions",
		PromptEst: estTokens(messages), MaxTokens: maxTokens, BytesUp: len(body), Path: s.path}
	retryAfter := 0
	resp, err := s.do(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		rec.Err, rec.TotalMS, rec.Aborted = err.Error(), time.Since(rec.TS).Milliseconds(), ctx.Err() != nil
		return rec, "", 0
	}
	defer resp.Body.Close()
	rec.Status = resp.StatusCode
	if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		retryAfter = ra
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		rec.BytesDown, rec.Code, rec.TotalMS = len(b), errCode(b), time.Since(rec.TS).Milliseconds()
		return rec, "", retryAfter
	}
	if !read {
		<-ctx.Done() // never read a byte; the host's write deadline is what ends this
		rec.TotalMS, rec.Code = time.Since(rec.TS).Milliseconds(), "never_read"
		return rec, "", 0
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	var queuedAt, firstAt time.Time
	var text strings.Builder
	for sc.Scan() {
		line := sc.Text()
		rec.BytesDown += len(line) + 1
		switch {
		case line == ": queued":
			if queuedAt.IsZero() {
				queuedAt = time.Now()
			}
		case strings.HasPrefix(line, "data: ") && line != "data: [DONE]":
			var ev struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *struct {
					Prompt     int `json:"prompt_tokens"`
					Completion int `json:"completion_tokens"`
				} `json:"usage"`
				Error *struct {
					Code       string `json:"code"`
					Message    string `json:"message"`
					RetryAfter int    `json:"retry_after"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(line[6:]), &ev) != nil {
				continue
			}
			if ev.Error != nil {
				rec.Code, rec.Err, retryAfter = ev.Error.Code, ev.Error.Message, ev.Error.RetryAfter
				continue
			}
			if firstAt.IsZero() {
				firstAt = time.Now()
			}
			if len(ev.Choices) > 0 {
				text.WriteString(ev.Choices[0].Delta.Content)
			}
			if ev.Usage != nil {
				rec.PromptTok, rec.CompTok = ev.Usage.Prompt, ev.Usage.Completion
			}
		}
	}
	if err := sc.Err(); err != nil && rec.Err == "" {
		rec.Err, rec.Aborted = err.Error(), ctx.Err() != nil
	}
	end := time.Now()
	rec.TotalMS = end.Sub(rec.TS).Milliseconds()
	if !firstAt.IsZero() {
		rec.TTFTMS = firstAt.Sub(rec.TS).Milliseconds()
		if !queuedAt.IsZero() {
			rec.QueuedMS = firstAt.Sub(queuedAt).Milliseconds()
		}
		if gen := end.Sub(firstAt).Seconds(); gen > 0 && rec.CompTok > 0 {
			rec.TokS = float64(rec.CompTok) / gen
		}
	}
	return rec, text.String(), retryAfter
}

// runChat is the mix: turn after turn until the loop context ends; the request in flight finishes
// under grace. A served reply extends the history; the context wall (422) starts a new chat, as a
// friend would; busy answers (429/503) are retried after Retry-After — what the web app's copy
// tells the friend to do.
func (s *session) runChat(loop, grace context.Context, r *run) {
	for loop.Err() == nil {
		s.turn++
		spec := turnFor(s.friend, s.turn)
		if r.bodyBytes > 0 && s.turn == 1 {
			// A message of exactly bodyBytes characters, so the HTTP body clears the gateway's cap
			// when bodyBytes > 4 MiB (413 body_too_large) and lands just under it otherwise. maxTokens
			// is small: this run tests the uplink and the cap, not generation.
			spec = turnSpec{content: strings.Repeat("lorem ipsum dolor sit amet ", r.bodyBytes/27+1)[:r.bodyBytes], maxTokens: 64}
		}
		messages := append(slices.Clone(s.history), msg{"user", spec.content})
		rec, reply, retryAfter := s.chat(grace, r.model, messages, spec.maxTokens, !r.noRead(s))
		rec.Turn, rec.Doc = s.turn, spec.doc
		r.record(rec)
		switch {
		case rec.Status == 200 && rec.Code == "" && rec.Err == "":
			s.history = append(s.history, msg{"user", spec.content}, msg{"assistant", reply})
			if s.turn%10 == 0 {
				s.path = s.pathLabel(grace)
			}
		case rec.Code == "context_too_long":
			s.history, s.turn = nil, 0
		case rec.Status == 429 || rec.Status == 503 || rec.Code == "queue_timeout":
			s.turn--
			sleepCtx(loop, time.Duration(max(1, min(retryAfter, 5)))*time.Second)
		default:
			s.turn--
			sleepCtx(loop, 2*time.Second)
		}
	}
}

// runSessions is layers 1–2 at large N: stay connected, dial for a tiny GET every trickle
// interval, and one friend in eight sends a small chat each interval — sessions, not inference.
func (s *session) runSessions(loop, grace context.Context, r *run) {
	rec, _ := s.get(grace, "/me")
	r.record(rec)
	t := time.NewTicker(r.trickle)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-loop.Done():
			return
		case <-t.C:
		}
		rec, _ := s.get(grace, "/healthz")
		r.record(rec)
		if (i+s.friend)%8 == 0 {
			rec, _, _ := s.chat(grace, r.model, []msg{{"user", turnFor(s.friend, 1).content}}, 64, true)
			rec.Turn = 1
			r.record(rec)
		}
	}
}

func errCode(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Code != "" {
		return e.Error.Code
	}
	return "non_json"
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

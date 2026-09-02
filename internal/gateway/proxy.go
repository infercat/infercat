package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/upstream"
)

const (
	maxUpstreamBody = 64 << 20         // non-stream response cap
	countTimeout    = 10 * time.Second // CountTokens / models list
)

// ---- request body ----

// readBody enforces MaxBody: a declared Content-Length over the cap is refused before reading a
// byte; an undeclared one is cut off by MaxBytesReader.
func (c *call) readBody() ([]byte, *gwError) {
	limit := c.g.cfg.MaxBody
	if c.r.ContentLength > limit {
		return nil, errf(CodeBodyTooLarge, 0, "request body is %d bytes; the limit is %d", c.r.ContentLength, limit)
	}
	b, err := io.ReadAll(http.MaxBytesReader(c.w, c.r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errf(CodeBodyTooLarge, 0, "request body exceeds the limit of %d bytes", limit)
		}
		return nil, errf(CodeInvalidRequest, 0, "reading request body: %v", err)
	}
	return b, nil
}

// decodeObject keeps numbers as json.Number so unknown fields round-trip byte-exact in value.
func decodeObject(b []byte) (map[string]any, *gwError) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, errf(CodeInvalidRequest, 0, "request body must be a JSON object")
	}
	return m, nil
}

// messagesText concatenates the text of every message (string content, or text parts; image parts
// count as nothing) for the token pre-check and, with LogPrompts, the event's Prompt.
func messagesText(v any) string {
	msgs, _ := v.([]any)
	var sb strings.Builder
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		switch content := mm["content"].(type) {
		case string:
			sb.WriteString(content)
			sb.WriteByte('\n')
		case []any:
			for _, p := range content {
				pm, _ := p.(map[string]any)
				if t, _ := pm["type"].(string); t == "text" {
					if s, ok := pm["text"].(string); ok {
						sb.WriteString(s)
						sb.WriteByte('\n')
					}
				}
			}
		}
	}
	return sb.String()
}

// inputText is the embeddings equivalent: a string or an array of strings.
func inputText(v any) string {
	switch in := v.(type) {
	case string:
		return in
	case []any:
		var sb strings.Builder
		for _, p := range in {
			if s, ok := p.(string); ok {
				sb.WriteString(s)
				sb.WriteByte('\n')
			}
		}
		return sb.String()
	}
	return ""
}

func (c *call) countTokens(text string) int {
	if text == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(c.r.Context(), countTimeout)
	defer cancel()
	n, _, err := c.g.up.CountTokens(ctx, text)
	if err != nil {
		c.g.logf("gateway: count tokens failed, estimating: %v", err)
		return (len(text) + 3) / 4
	}
	return n
}

// resolveModel fills a missing model (first upstream model the key allows, else the key's first
// allowed model) and enforces the allowlist.
func (c *call) resolveModel(body map[string]any) (string, *gwError) {
	m, _ := body["model"].(string)
	if m == "" {
		for _, id := range c.g.up.Info().Models {
			if c.key.AllowsModel(id) {
				m = id
				break
			}
		}
		if m == "" && len(c.key.Limits.Models) > 0 {
			m = c.key.Limits.Models[0]
		}
		if m != "" {
			body["model"] = m
		}
	}
	if m != "" && !c.key.AllowsModel(m) {
		return "", errf(CodeModelNotAllowed, 0, "model %q is not allowed for this key", m)
	}
	return m, nil
}

// clampMaxTokens caps max_tokens / max_completion_tokens to the key's MaxOutputTokens (never
// rejects; sets max_tokens when absent) and returns the cap in force, 0 if none.
func clampMaxTokens(body map[string]any, lim keys.Limits) int {
	inForce := 0
	present := false
	for _, f := range []string{"max_tokens", "max_completion_tokens"} {
		n, ok := body[f].(json.Number)
		if !ok {
			continue
		}
		present = true
		v, err := n.Int64()
		if err != nil || v <= 0 { // absent-equivalent (llama.cpp's -1 = unlimited)
			v = 0
		}
		if lim.MaxOutputTokens > 0 && (v == 0 || v > int64(lim.MaxOutputTokens)) {
			v = int64(lim.MaxOutputTokens)
			body[f] = json.Number(strconv.FormatInt(v, 10))
		}
		if v > 0 && (inForce == 0 || int(v) < inForce) {
			inForce = int(v)
		}
	}
	if !present && lim.MaxOutputTokens > 0 {
		body["max_tokens"] = json.Number(strconv.Itoa(lim.MaxOutputTokens))
		inForce = lim.MaxOutputTokens
	}
	return inForce
}

// minOutputTokens is the floor when max_tokens is shrunk to fit the context.
const minOutputTokens = 16

// fitContext: effective context = min(key.MaxContext, upstream model context), ignoring zeros.
// The prompt alone must fit (422 otherwise). When prompt + max_tokens would overshoot, max_tokens
// shrinks to what remains (floor minOutputTokens), as llama.cpp does itself, instead of a
// rejection (ticket 005 fix 10c).
func (c *call) fitContext(body map[string]any, prompt, maxTok int) *gwError {
	eff := c.g.up.Info().ModelContext
	if k := c.key.Limits.MaxContext; k > 0 && (eff == 0 || k < eff) {
		eff = k
	}
	if eff == 0 {
		return nil
	}
	if prompt > eff {
		return errf(CodeContextTooLong, 0, "prompt is %d tokens but the context is %d", prompt, eff)
	}
	if maxTok > 0 && prompt+maxTok > eff {
		setMaxTokens(body, max(eff-prompt, minOutputTokens))
	}
	return nil
}

// setMaxTokens rewrites whichever cap field(s) the request carries; max_tokens when neither is present.
func setMaxTokens(body map[string]any, n int) {
	v := json.Number(strconv.Itoa(n))
	set := false
	for _, f := range []string{"max_tokens", "max_completion_tokens"} {
		if _, ok := body[f]; ok {
			body[f] = v
			set = true
		}
	}
	if !set {
		body["max_tokens"] = v
	}
}

func setIncludeUsage(body map[string]any) {
	so, _ := body["stream_options"].(map[string]any)
	if so == nil {
		so = map[string]any{}
	}
	so["include_usage"] = true
	body["stream_options"] = so
}

// ---- routes ----

func (c *call) chat() {
	raw, gerr := c.readBody()
	if gerr != nil {
		c.fail(gerr)
		return
	}
	body, gerr := decodeObject(raw)
	if gerr != nil {
		c.fail(gerr)
		return
	}
	c.ev.Stream, _ = body["stream"].(bool)
	model, gerr := c.resolveModel(body)
	if gerr != nil {
		c.fail(gerr)
		return
	}
	c.ev.Model = model
	text := messagesText(body["messages"])
	if c.g.cfg.LogPrompts {
		c.ev.Prompt = text
	}
	prompt := c.countTokens(text)
	maxTok := clampMaxTokens(body, c.key.Limits)
	if gerr := c.fitContext(body, prompt, maxTok); gerr != nil {
		c.fail(gerr)
		return
	}
	if c.ev.Stream {
		setIncludeUsage(body)
	}
	c.proxy(body, "/v1/chat/completions", prompt)
}

func (c *call) embeddings() {
	raw, gerr := c.readBody()
	if gerr != nil {
		c.fail(gerr)
		return
	}
	body, gerr := decodeObject(raw)
	if gerr != nil {
		c.fail(gerr)
		return
	}
	model, gerr := c.resolveModel(body)
	if gerr != nil {
		c.fail(gerr)
		return
	}
	c.ev.Model = model
	text := inputText(body["input"])
	if c.g.cfg.LogPrompts {
		c.ev.Prompt = text
	}
	c.proxy(body, "/v1/embeddings", c.countTokens(text))
}

// models proxies GET /v1/models and keeps only the ids the key allows.
func (c *call) models() {
	ctx, cancel := context.WithTimeout(c.r.Context(), countTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.upstreamURL("/v1/models"), nil)
	resp, err := c.doUpstream(req)
	if err != nil {
		c.fail(c.upstreamErr(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		c.fail(errf(CodeUpstreamError, 0, "upstream returned HTTP %d for /v1/models: %s", resp.StatusCode, snippet(resp.Body)))
		return
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBody))
	dec.UseNumber()
	if err := dec.Decode(&list); err != nil {
		c.fail(errf(CodeUpstreamError, 0, "upstream returned an unparseable model list"))
		return
	}
	out := make([]map[string]any, 0, len(list.Data))
	for _, m := range list.Data {
		if id, _ := m["id"].(string); c.key.AllowsModel(id) {
			out = append(out, m)
		}
	}
	c.writeJSON(map[string]any{"object": "list", "data": out})
}

type meResponse struct {
	Key struct {
		ID     string      `json:"id"`
		Name   string      `json:"name"`
		Status keys.Status `json:"status"`
	} `json:"key"`
	Limits keys.Limits `json:"limits"`
	Usage  struct {
		RPMUsed     int `json:"rpm_used"`
		TPMUsed     int `json:"tpm_used"`
		TodayTokens int `json:"today_tokens"`
		InFlight    int `json:"in_flight"`
	} `json:"usage"`
	Host struct {
		Name     string `json:"name"`
		Upstream struct {
			Kind         upstream.Kind `json:"kind"`
			Healthy      bool          `json:"healthy"`
			ModelContext int           `json:"model_context"`
		} `json:"upstream"`
		Models []string `json:"models"`
		Relay  struct {
			Region string `json:"region"`
		} `json:"relay"`
	} `json:"host"`
}

func (c *call) me() {
	var m meResponse
	m.Key.ID, m.Key.Name, m.Key.Status = c.key.ID, c.key.Name, c.key.Status
	m.Limits = c.key.Limits
	cnt := c.g.lim.counters(c.key.ID)
	m.Usage.RPMUsed, m.Usage.TPMUsed, m.Usage.TodayTokens, m.Usage.InFlight = cnt.RPMUsed, cnt.TPMUsed, cnt.TodayTokens, cnt.InFlight
	info := c.g.up.Info()
	m.Host.Name = c.g.cfg.HostName
	m.Host.Upstream.Kind, m.Host.Upstream.Healthy, m.Host.Upstream.ModelContext = info.Kind, info.Healthy, info.ModelContext
	m.Host.Models = []string{}
	for _, id := range info.Models {
		if c.key.AllowsModel(id) {
			m.Host.Models = append(m.Host.Models, id)
		}
	}
	if c.g.cfg.RelayRegion != nil {
		m.Host.Relay.Region = c.g.cfg.RelayRegion()
	}
	c.writeJSON(m)
}

func (c *call) writeJSON(v any) {
	b, _ := json.Marshal(v)
	c.w.Header().Set("Content-Type", "application/json")
	c.w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	c.writeHeader(http.StatusOK)
	_, _ = c.w.Write(b)
}

// ---- upstream ----

func (c *call) upstreamURL(path string) string {
	u := *c.g.up.BaseURL()
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return u.String()
}

func (c *call) doUpstream(req *http.Request) (*http.Response, error) {
	return (&http.Client{Transport: c.g.up.Transport()}).Do(req)
}

// upstreamErr maps a transport error. The friend never sees the engine's address (Protection 1);
// the host sees it in the log.
func (c *call) upstreamErr(err error) *gwError {
	if c.r.Context().Err() != nil {
		return errf(CodeClientClosed, 0, "client went away")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		c.g.logf("gateway: upstream timed out after %s", c.g.cfg.RequestTimeout)
		return errf(CodeUpstreamError, 0, "the host's engine did not answer within %s", c.g.cfg.RequestTimeout)
	}
	c.g.logf("gateway: upstream unreachable: %v", err)
	return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's engine is not reachable right now")
}

func snippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return strings.TrimSpace(string(b))
}

// proxy runs the guarded upstream call: health, per-key admission, global slot, the request, then
// the stream or body pipe. Tokens are charged to the key only for a 2xx (full or partial) response.
func (c *call) proxy(body map[string]any, path string, prompt int) {
	g, key := c.g, c.key
	c.ev.PromptTokens = prompt // provisional; replaced by the upstream's usage when present
	if !g.up.Info().Healthy {
		c.fail(errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's engine is not reachable right now"))
		return
	}
	if gerr := g.lim.admit(key.ID, key.Limits, prompt); gerr != nil {
		c.fail(gerr)
		return
	}
	charged := 0
	defer func() { g.lim.release(key.ID, charged) }()

	qstart := time.Now()
	releaseSlot, gerr := g.acquire(c.r.Context())
	c.ev.QueuedMS = time.Since(qstart).Milliseconds()
	if gerr != nil {
		c.fail(gerr)
		return
	}
	defer releaseSlot()

	payload, err := json.Marshal(body)
	if err != nil {
		c.fail(errf(CodeInvalidRequest, 0, "request body could not be re-encoded: %v", err))
		return
	}
	ctx, cancel := context.WithTimeout(c.r.Context(), g.cfg.RequestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.upstreamURL(path), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if c.ev.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.doUpstream(req)
	if err != nil {
		c.fail(c.upstreamErr(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		c.fail(errf(CodeUpstreamError, 0, "upstream returned HTTP %d: %s", resp.StatusCode, snippet(resp.Body)))
		return
	}
	if c.ev.Stream {
		c.pipeStream(resp, cancel)
	} else if !c.pipeBody(resp) {
		return
	}
	charged = c.ev.PromptTokens + c.ev.CompletionTokens
}

type usageT struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (c *call) applyUsage(u *usageT) {
	if u == nil {
		return
	}
	c.ev.PromptTokens, c.ev.CompletionTokens = u.PromptTokens, u.CompletionTokens
}

// pipeBody passes a non-stream response through verbatim after parsing its usage. Reports whether
// anything was written (false = an error response was written instead).
func (c *call) pipeBody(resp *http.Response) bool {
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBody))
	if err != nil {
		c.fail(c.upstreamErr(err))
		return false
	}
	var parsed struct {
		Usage   *usageT `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		c.g.logf("gateway: upstream response is not JSON: %v", err)
		c.fail(errf(CodeUpstreamError, 0, "upstream returned an unparseable response"))
		return false
	}
	c.applyUsage(parsed.Usage)
	if c.g.cfg.LogPrompts && len(parsed.Choices) > 0 {
		c.ev.Completion = parsed.Choices[0].Message.Content
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.w.Header().Set("Content-Type", ct)
	c.w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	c.writeHeader(http.StatusOK)
	c.markTTFT()
	_, _ = c.w.Write(b)
	return true
}

type sseChunk struct {
	Usage   *usageT `json:"usage"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
}

// pipeStream copies SSE bytes to the friend verbatim, flushing at every event boundary, while
// reading each data: payload for usage. When the stream ends without a usage chunk (the friend hit
// stop, or the engine omitted it) completion tokens are estimated as the number of delta chunks seen
// (llama.cpp and vLLM emit one token per chunk) so an aborted stream is not free; prompt tokens fall
// back to the pre-check count. A write error means the friend is gone: the upstream is cancelled.
func (c *call) pipeStream(resp *http.Response, cancelUpstream context.CancelFunc) {
	h := c.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	c.writeHeader(http.StatusOK)
	rc := http.NewResponseController(c.w)

	br := bufio.NewReaderSize(resp.Body, 64<<10)
	chunks, sawUsage := 0, false
	var completion strings.Builder
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := c.w.Write(line); werr != nil {
				cancelUpstream()
				break
			}
			c.markTTFT()
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(trimmed) == 0 {
				_ = rc.Flush()
			} else if data, ok := bytes.CutPrefix(trimmed, []byte("data:")); ok {
				data = bytes.TrimSpace(data)
				var ch sseChunk
				if !bytes.Equal(data, []byte("[DONE]")) && json.Unmarshal(data, &ch) == nil {
					if ch.Usage != nil {
						sawUsage = true
						c.applyUsage(ch.Usage)
					}
					for _, choice := range ch.Choices {
						if choice.Delta.Content != "" || choice.Delta.ReasoningContent != "" {
							chunks++
							if c.g.cfg.LogPrompts {
								completion.WriteString(choice.Delta.Content)
							}
						}
					}
				}
			}
		}
		if err != nil {
			_ = rc.Flush()
			if !errors.Is(err, io.EOF) {
				c.fail(c.upstreamErr(err))
			}
			break
		}
	}
	if !sawUsage {
		c.ev.CompletionTokens = chunks
	}
	if c.g.cfg.LogPrompts {
		c.ev.Completion = completion.String()
	}
}

package gateway

// The engine-facing half of a request: body shaping (what normalize and checkBudgets call), the two
// read-only routes, the upstream client, and the two relays. The pipeline itself is in request.go.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
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

// ---- body shaping ----

// decodeObject keeps numbers as json.Number so unknown fields round-trip byte-exact in value.
func decodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not an object")
	}
	return m, nil
}

// overrideKeys are removed from every proxied body (006 promise 1; the ticket's one new concept).
// Source: llama.cpp tools/server/README.md (POST /completion options and the OAI-compat extras, read
// 2026-09-02) plus vLLM's and Ollama's OpenAI layers. A key is here if it could raise output length
// past the clamp, multiply it, pin or bypass a slot, or override what the host configured. Everything
// that only shapes sampling or the response passes through: temperature, top_p, top_k, min_p, seed,
// stop, presence/frequency/repeat penalties, dry_*, xtc_*, mirostat*, logit_bias, samplers, grammar,
// json_schema, response_format, tools, logprobs, reasoning_*, chat_template_kwargs, cache_prompt,
// t_max_predict_ms, timings_per_token, return_tokens. stream_options is not here either: the gateway
// forces include_usage and no other member changes cost.
var overrideKeys = []string{
	// Output length. llama.cpp copies n_predict over max_tokens ("if n_predict is present, we overwrite
	// the value specified earlier by max_tokens"); vLLM has min_tokens and ignore_eos; Ollama num_predict.
	"n_predict", "max_new_tokens", "min_tokens", "num_predict", "ignore_eos",
	// Output multipliers: n completions for one prompt are n times the tokens (and, in vLLM, n
	// sequences) behind one global slot and one clamp.
	"n", "n_cmpl", "best_of", "use_beam_search",
	// Top-N probabilities for every generated token.
	"n_probs",
	// Slot, cache, context, and scheduling: pin a slot, retain or shift context, jump the queue.
	"id_slot", "slot_id", "n_keep", "num_keep", "n_discard", "n_ctx", "num_ctx", "priority",
	// The host's model configuration: per-request LoRA adapters.
	"lora",
}

// stripOverrides deletes overrideKeys from body, plus any max_tokens / max_completion_tokens that is
// not a number (a string there could shadow the clamp on an engine that prefers one over the other;
// removed, the clamp sets the cap). Returns what was removed, sorted, for the host's log.
func stripOverrides(body map[string]any) []string {
	var removed []string
	for _, k := range overrideKeys {
		if _, ok := body[k]; ok {
			delete(body, k)
			removed = append(removed, k)
		}
	}
	for _, f := range []string{"max_tokens", "max_completion_tokens"} {
		if v, ok := body[f]; ok {
			if _, isNum := v.(json.Number); !isNum {
				delete(body, f)
				removed = append(removed, f)
			}
		}
	}
	sort.Strings(removed)
	return removed
}

// normalize is the one place the body is shaped before the engine sees it: engine-override aliases
// are stripped (promise 1, overrideKeys), a missing model is filled and the allowlist enforced, the
// output cap is clamped to the key's, and streams get include_usage. Everything else in body passes
// through byte-identical. It returns what the later stages read (DESIGN §1.7).
func normalize(kind endpoint, body map[string]any, k *keys.Key, engineModels []string) (normalized, *gwError) {
	n := normalized{body: body, stripped: stripOverrides(body)}
	model, err := resolveModel(body, k, engineModels)
	if err != nil {
		return n, err
	}
	n.model = model
	switch kind {
	case chatEndpoint:
		n.stream, _ = body["stream"].(bool)
		n.text = messagesText(body["messages"])
		n.maxTok = clampMaxTokens(body, k.Limits)
		if n.stream {
			setIncludeUsage(body)
		}
	case embeddingsEndpoint:
		n.text = inputText(body["input"])
	}
	return n, nil
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

func (q *request) countTokens(text string) int {
	if text == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(q.r.Context(), countTimeout)
	defer cancel()
	n, _, err := q.g.up.CountTokens(ctx, text)
	if err != nil {
		q.g.logf("gateway: count tokens failed, estimating: %v", err)
		return (len(text) + 3) / 4
	}
	return n
}

// resolveModel fills a missing model (first engine model the key allows, else the key's first
// allowed model) and enforces the allowlist.
func resolveModel(body map[string]any, k *keys.Key, engineModels []string) (string, *gwError) {
	m, _ := body["model"].(string)
	if m == "" {
		for _, id := range engineModels {
			if k.AllowsModel(id) {
				m = id
				break
			}
		}
		if m == "" && len(k.Limits.Models) > 0 {
			m = k.Limits.Models[0]
		}
		if m != "" {
			body["model"] = m
		}
	}
	if m != "" && !k.AllowsModel(m) {
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

// fitContext: effective context = min(key.MaxContext, upstream model context), ignoring zeros.
// The prompt alone must fit (422 otherwise). When prompt + max_tokens would overshoot, max_tokens
// shrinks to what remains (floor minOutputTokens), as llama.cpp does itself, instead of a
// rejection (ticket 005 fix 10c). Embeddings carry no cap: only the first rule applies.
func (q *request) fitContext() *gwError {
	eff := q.g.up.Info().ModelContext
	if k := q.key.Limits.MaxContext; k > 0 && (eff == 0 || k < eff) {
		eff = k
	}
	if eff == 0 {
		return nil
	}
	if q.prompt > eff {
		return errf(CodeContextTooLong, 0, "prompt is %d tokens but the context is %d", q.prompt, eff)
	}
	if q.n.maxTok > 0 && q.prompt+q.n.maxTok > eff {
		q.setMaxTok(max(eff-q.prompt, minOutputTokens))
	}
	return nil
}

// setMaxTok rewrites the cap in force: whichever cap field(s) the request carries, max_tokens when
// neither is present, and the record's copy.
func (q *request) setMaxTok(n int) {
	v := json.Number(strconv.Itoa(n))
	set := false
	for _, f := range []string{"max_tokens", "max_completion_tokens"} {
		if _, ok := q.n.body[f]; ok {
			q.n.body[f] = v
			set = true
		}
	}
	if !set {
		q.n.body["max_tokens"] = v
	}
	q.n.maxTok = n
}

func setIncludeUsage(body map[string]any) {
	so, _ := body["stream_options"].(map[string]any)
	if so == nil {
		so = map[string]any{}
	}
	so["include_usage"] = true
	body["stream_options"] = so
}

// ---- read-only routes ----

// models proxies GET /v1/models under per-key concurrency and RPM (006 promise 5; no global slot:
// it is a list, not a generation) and keeps only the ids the key allows. A list call counts as a
// request whatever the engine answers (EngineErr or Served).
func (q *request) models() {
	if err := q.admitKey(); err != nil {
		q.fail(err)
		return
	}
	q.outcome = outcomeEngineErr
	ctx, cancel := context.WithTimeout(q.r.Context(), countTimeout)
	q.cancelUpstream = cancel
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, q.upstreamURL("/v1/models"), nil)
	resp, err := q.doUpstream(req)
	if err != nil {
		q.fail(q.upstreamErr(err))
		return
	}
	q.resp = resp
	if resp.StatusCode/100 != 2 { // a GET carries nothing of the friend's: any failure is the host's
		q.fail(errf(CodeUpstreamError, 0, "upstream returned HTTP %d for /v1/models: %s", resp.StatusCode, upstreamMessage(resp.Body)))
		return
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBody))
	dec.UseNumber()
	if err := dec.Decode(&list); err != nil {
		q.fail(errf(CodeUpstreamError, 0, "upstream returned an unparseable model list"))
		return
	}
	out := make([]map[string]any, 0, len(list.Data))
	for _, m := range list.Data {
		if id, _ := m["id"].(string); q.key.AllowsModel(id) {
			out = append(out, m)
		}
	}
	q.outcome = outcomeServed
	q.writeJSON(map[string]any{"object": "list", "data": out})
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
		LogPrompts bool `json:"log_prompts"` // the host records prompt text (Protection 3 disclosure; 006 promise 11)
	} `json:"host"`
}

func (q *request) me() {
	var m meResponse
	m.Key.ID, m.Key.Name, m.Key.Status = q.key.ID, q.key.Name, q.key.Status
	m.Limits = q.key.Limits
	cnt := q.g.lim.counters(q.key.ID)
	m.Usage.RPMUsed, m.Usage.TPMUsed, m.Usage.TodayTokens, m.Usage.InFlight = cnt.RPMUsed, cnt.TPMUsed, cnt.TodayTokens, cnt.InFlight
	info := q.g.up.Info()
	m.Host.Name = q.g.cfg.HostName
	m.Host.LogPrompts = q.g.cfg.LogPrompts
	m.Host.Upstream.Kind, m.Host.Upstream.Healthy, m.Host.Upstream.ModelContext = info.Kind, info.Healthy, info.ModelContext
	m.Host.Models = []string{}
	for _, id := range info.Models {
		if q.key.AllowsModel(id) {
			m.Host.Models = append(m.Host.Models, id)
		}
	}
	if q.g.cfg.RelayRegion != nil {
		m.Host.Relay.Region = q.g.cfg.RelayRegion()
	}
	q.writeJSON(m)
}

func (q *request) writeJSON(v any) {
	b, _ := json.Marshal(v)
	q.w.Header().Set("Content-Type", "application/json")
	q.w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	q.writeHeader(http.StatusOK)
	_, _ = q.w.Write(b)
}

// ---- upstream ----

func (q *request) upstreamURL(path string) string {
	u := *q.g.up.BaseURL()
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return u.String()
}

// doUpstream never follows a redirect (006 promise 6): the engine's transport stamps the host's
// upstream API key on every request it sends, so a 3xx would replay that key to wherever Location
// points. The 3xx surfaces as a non-2xx status instead.
func (q *request) doUpstream(req *http.Request) (*http.Response, error) {
	client := &http.Client{
		Transport:     q.g.up.Transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client.Do(req)
}

// upstreamErr maps a failed engine read: the friend left, the engine stalled past the idle
// deadline, or the engine is not there. The friend never sees the engine's address (Protection 1);
// the host sees it in the log.
func (q *request) upstreamErr(err error) *gwError {
	if q.r.Context().Err() != nil {
		return errf(CodeClientClosed, 0, "client went away")
	}
	if q.engineIdle.Load() {
		q.g.logf("gateway: upstream sent nothing for %s", q.g.idleTimeout)
		return errf(CodeUpstreamError, 0, "the host's engine stopped answering for %s", q.g.idleTimeout)
	}
	q.g.logf("gateway: upstream unreachable: %v", err)
	return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's engine is not reachable right now")
}

func snippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return strings.TrimSpace(string(b))
}

// upstreamMessage is the engine's own sentence when its error body has one (llama.cpp:
// {"error":{"message":…}}; vLLM: {"message":…}), else the raw snippet.
func upstreamMessage(r io.Reader) string {
	s := snippet(r)
	var e struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s), &e) == nil {
		if e.Error.Message != "" {
			return e.Error.Message
		}
		if e.Message != "" {
			return e.Message
		}
	}
	return s
}

// upstreamStatusErr maps a non-2xx status from a proxied POST (006 promise 8). 400 and 422 are about
// the friend's own request (a schema the model cannot follow, an image it cannot take, a field it
// rejects): 400 invalid_request carrying the engine's sentence, so their client does not retry
// against a "broken host". Everything else — 5xx, a redirect the gateway refused to follow, 401/404
// from a misconfigured upstream — is the host's problem: 502 upstream_error.
func upstreamStatusErr(resp *http.Response) *gwError {
	msg := upstreamMessage(resp.Body)
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
		return errf(CodeInvalidRequest, 0, "the host's engine rejected this request (HTTP %d): %s", resp.StatusCode, msg)
	}
	return errf(CodeUpstreamError, 0, "upstream returned HTTP %d: %s", resp.StatusCode, msg)
}

// ---- relays ----

// idleReader re-arms the engine idle deadline on every read: a live engine, however slow, is never
// cut; one that sends nothing for the idle duration is cancelled (DESIGN §1.6).
type idleReader struct {
	r io.Reader
	t *time.Timer
	d time.Duration
}

func (x idleReader) Read(p []byte) (int, error) {
	n, err := x.r.Read(p)
	x.t.Reset(x.d)
	return n, err
}

type usageT struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (q *request) applyUsage(u *usageT) {
	if u == nil {
		return
	}
	q.ev.PromptTokens, q.ev.CompletionTokens = u.PromptTokens, u.CompletionTokens
}

// pipeBody passes a non-stream response through verbatim after parsing its usage. A body that
// cannot be read is Cut when the friend left (the engine did the work) and the engine's failure
// otherwise.
func (q *request) pipeBody(body io.Reader) *gwError {
	resp := q.resp
	b, err := io.ReadAll(io.LimitReader(body, maxUpstreamBody))
	if err != nil {
		if q.r.Context().Err() != nil {
			q.outcome = outcomeCut
		} else {
			q.outcome = outcomeEngineErr
		}
		return q.upstreamErr(err)
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
		q.outcome = outcomeEngineErr
		q.g.logf("gateway: upstream response is not JSON: %v", err)
		return errf(CodeUpstreamError, 0, "upstream returned an unparseable response")
	}
	q.applyUsage(parsed.Usage)
	if q.g.cfg.LogPrompts && len(parsed.Choices) > 0 {
		q.ev.Completion = parsed.Choices[0].Message.Content
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	q.w.Header().Set("Content-Type", ct)
	q.w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	q.writeHeader(http.StatusOK)
	q.markTTFT()
	_, _ = q.w.Write(b)
	return nil
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
// back to the pre-check count. A write or flush error means the friend is gone or has stopped
// reading (the per-line write deadline fired); a read error means the friend left, the engine went
// idle, or it died: all Cut. EOF is the end.
func (q *request) pipeStream(body io.Reader) *gwError {
	h := q.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	q.writeHeader(http.StatusOK)

	br := bufio.NewReaderSize(body, 64<<10)
	chunks, sawUsage := 0, false
	var completion strings.Builder
	var result *gwError
	for result == nil {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			q.armWrite()
			if _, werr := q.w.Write(line); werr != nil {
				result = errf(CodeClientClosed, 0, "client stopped reading")
				break
			}
			q.markTTFT()
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(trimmed) == 0 {
				if ferr := q.rc.Flush(); ferr != nil {
					result = errf(CodeClientClosed, 0, "client stopped reading")
					break
				}
			} else if data, ok := bytes.CutPrefix(trimmed, []byte("data:")); ok {
				data = bytes.TrimSpace(data)
				var ch sseChunk
				if !bytes.Equal(data, []byte("[DONE]")) && json.Unmarshal(data, &ch) == nil {
					if ch.Usage != nil {
						sawUsage = true
						q.applyUsage(ch.Usage)
					}
					for _, choice := range ch.Choices {
						if choice.Delta.Content != "" || choice.Delta.ReasoningContent != "" {
							chunks++
							if q.g.cfg.LogPrompts {
								completion.WriteString(choice.Delta.Content)
							}
						}
					}
				}
			}
		}
		if err != nil {
			q.armWrite()
			_ = q.rc.Flush()
			if !errors.Is(err, io.EOF) {
				result = q.upstreamErr(err)
			}
			break
		}
	}
	if result != nil {
		q.outcome = outcomeCut
	}
	if !sawUsage {
		q.ev.CompletionTokens = chunks
	}
	if q.g.cfg.LogPrompts {
		q.ev.Completion = completion.String()
	}
	return result
}

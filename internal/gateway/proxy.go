package gateway

// The engine-facing half of a request: body shaping (what normalize and checkBudgets call), the two
// read-only routes, the error mapping for the engine seam, and the two relays. The pipeline itself
// is in request.go. Everything the engine is sent goes through upstream.Engine.Do: this package
// never sees the engine's address or transport (DESIGN §3.4, E4).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

const maxUpstreamBody = 64 << 20 // non-stream response cap

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
func normalize(kind endpoint, body map[string]any, k *keys.Key, engineModels, pinned []string) (normalized, *gwError) {
	n := normalized{body: body, stripped: stripOverrides(body)}
	model, err := resolveModel(body, k, engineModels, pinned)
	if err != nil {
		return n, err
	}
	n.model = model
	switch kind {
	case chatEndpoint:
		n.stream, _ = body["stream"].(bool)
		n.text = messagesText(body["messages"])
		if v, ok := body["messages"]; ok {
			n.messages, _ = json.Marshal(v)
		}
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

// countTokens asks the engine, which bounds the call itself (upstream.ProbeTimeout); a chat is
// counted under the engine's template (036), so the count is the one the engine will enforce.
func (q *request) countTokens(text string, messages []byte) int {
	if text == "" {
		return 0
	}
	n, _, err := q.destination.Text.CountTokens(q.r.Context(), q.n.model, text, messages)
	if err != nil {
		q.g.logf("gateway: count tokens failed, estimating: %v", err)
		return upstream.EstimateTokens(text) + upstream.TemplateAllowance(messages)
	}
	return n
}

// resolveModel chooses the first permitted engine model, then the first effective allowlist entry.
// An empty intersection must refuse rather than let the engine choose its own default.
func resolveModel(body map[string]any, k *keys.Key, engineModels, pinned []string) (string, *gwError) {
	m, _ := body["model"].(string)
	if m == "" {
		for _, id := range engineModels {
			if allowsModel(k, pinned, id) {
				m = id
				break
			}
		}
		fallback := k.Limits.Models
		if len(fallback) == 0 {
			fallback = pinned
		}
		for _, id := range fallback {
			if m == "" && allowsModel(k, pinned, id) {
				m = id
			}
		}
		if m != "" {
			body["model"] = m
		}
	}
	if m == "" && len(pinned) > 0 {
		return "", errf(CodeModelNotAllowed, 0, "no model is allowed for this key")
	}
	if m != "" && !allowsModel(k, pinned, m) {
		return "", errf(CodeModelNotAllowed, 0, "model %q is not allowed for this key", m)
	}
	return m, nil
}

func allowsModel(k *keys.Key, pinned []string, model string) bool {
	return k.AllowsModel(model) && (len(pinned) == 0 || slices.Contains(pinned, model))
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
	eff := q.effContext()
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

// effContext keeps the key ceiling while using the selected model's window when reported.
func (q *request) effContext() int {
	info := q.destination.Up.Info()
	eff := info.ModelContext
	if model := info.ModelDetails[q.n.model]; model.Context > 0 {
		eff = model.Context
	}
	if k := q.key.Limits.MaxContext; k > 0 && (eff == 0 || k < eff) {
		eff = k
	}
	return eff
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

// models proxies GET /v1/models under per-key concurrency (006 promise 5; no global slot: it is a
// list, not a generation) and keeps only the ids the key allows. It is admitted like any request,
// so per-key concurrency still bounds it, but the settle table does not count it against RPM
// (014 promise 5): a list is not one of the friend's messages.
func (q *request) models() {
	q.kind = modelsEndpoint
	if err := q.admitKey(); err != nil {
		q.fail(err)
		return
	}
	q.outcome = outcomeEngineErr
	resp, err := q.destination.Text.Do(q.r.Context(), http.MethodGet, "/v1/models", nil, false)
	if err != nil {
		q.fail(q.upstreamErr(err))
		return
	}
	q.resp = resp
	if resp.StatusCode/100 != 2 { // a GET carries nothing of the friend's: any failure is the host's
		msg, _ := upstreamMessage(resp.Body)
		q.fail(errf(CodeUpstreamError, 0, "upstream returned HTTP %d for /v1/models: %s", resp.StatusCode, msg))
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
		if id, _ := m["id"].(string); allowsModel(q.key, q.g.cfg.ModelsPinned, id) {
			out = append(out, m)
		}
	}
	q.outcome = outcomeServed
	q.writeJSON(map[string]any{"object": "list", "data": out})
}

type meResponse struct {
	Agent *bool `json:"agent,omitempty"`
	Key   struct {
		ID     string      `json:"id"`
		Name   string      `json:"name"`
		Status keys.Status `json:"status"`
	} `json:"key"`
	Limits keys.Limits `json:"limits"`
	Usage  struct {
		TodayImages int `json:"today_images,omitempty"`
		RPMUsed     int `json:"rpm_used"`
		TPMUsed     int `json:"tpm_used"`
		TodayTokens int `json:"today_tokens"`
		InFlight    int `json:"in_flight"`
	} `json:"usage"`
	Host struct {
		Images   *imageOffer `json:"images,omitempty"`
		Name     string      `json:"name"`
		Upstream struct {
			Kind         upstream.Kind `json:"kind"`
			Healthy      bool          `json:"healthy"`
			ModelContext int           `json:"model_context"`
		} `json:"upstream"`
		Models []string         `json:"models"`
		Vision map[string]*bool `json:"vision"`
		Audio  struct {
			Transcriptions *string `json:"transcriptions"`
			Speech         *string `json:"speech"`
		} `json:"audio"`
		Relay struct {
			Region string `json:"region"`
		} `json:"relay"`
		LogPrompts bool `json:"log_prompts"` // the host records prompt text (Protection 3 disclosure; 006 promise 11)
	} `json:"host"`
}

func (q *request) me() {
	var m meResponse
	if q.g.runs != nil && q.g.runs.Kinds["agent"] != nil {
		enabled := q.key.Agent
		m.Agent = &enabled
	}
	m.Key.ID, m.Key.Name, m.Key.Status = q.key.ID, q.key.Name, q.key.Status
	m.Limits = keys.AudioDefaults(q.key.Limits)
	m.Host.Images, _ = q.g.imageOffer(q.key)
	m.Limits.MaxQueuedImages = ImageQueueCap(q.key.Limits)
	cnt := q.g.lim.counters(q.key.ID)
	m.Usage.TodayImages = cnt.TodayImages
	m.Usage.RPMUsed, m.Usage.TPMUsed, m.Usage.TodayTokens, m.Usage.InFlight = cnt.RPMUsed, cnt.TPMUsed, cnt.TodayTokens, cnt.InFlight
	info := q.g.router.text.Up.Info()
	m.Host.Name = q.g.cfg.HostName
	if q.g.cfg.LiveHostName != nil {
		m.Host.Name = q.g.cfg.LiveHostName()
	}
	m.Host.Audio.Transcriptions = q.g.router.audioModel(transcribeEndpoint)
	m.Host.Audio.Speech = q.g.router.audioModel(speechEndpoint)
	for _, model := range []**string{&m.Host.Audio.Transcriptions, &m.Host.Audio.Speech} {
		if *model != nil && !allowsModel(q.key, nil, **model) {
			*model = nil
		}
	}
	m.Host.LogPrompts = q.g.cfg.LogPrompts
	m.Host.Upstream.Kind, m.Host.Upstream.Healthy, m.Host.Upstream.ModelContext = info.Kind, info.Health.OK, info.ModelContext
	m.Host.Models = []string{}
	m.Host.Vision = make(map[string]*bool)
	for _, id := range info.Models {
		if allowsModel(q.key, q.g.cfg.ModelsPinned, id) {
			m.Host.Models = append(m.Host.Models, id)
			m.Host.Vision[id] = info.Vision[id]
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

// upstreamErr maps a failed engine call or read: the friend left, the engine stalled past the
// idle deadline, the engine's own deadline fired (its first byte, or a list's probe bound — a
// timeout error from the seam), or the engine is not there. The friend never sees the engine's
// address (Protection 1); the host sees it in the log.
func (q *request) upstreamErr(err error) *gwError {
	if q.r.Context().Err() != nil {
		return errf(CodeClientClosed, 0, "client went away")
	}
	if q.engineIdle.Load() {
		q.g.logf("gateway: upstream sent nothing for %s", q.g.idleTimeout)
		return errf(CodeUpstreamError, 0, "the host's engine stopped answering for %s", q.g.idleTimeout)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		q.g.logf("gateway: upstream did not answer in time: %v", err)
		return errf(CodeUpstreamError, 0, "the host's engine did not answer in time")
	}
	q.g.logf("gateway: upstream unreachable: %v", err)
	return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's engine is not reachable right now")
}

func snippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return strings.TrimSpace(string(b))
}

// upstreamMessage is the engine's own sentence and error type when its error body has them
// (llama.cpp and vLLM 0.25: {"error":{"message":…,"type":…}}; older vLLM: {"message":…,"type":…}),
// else the raw snippet and "".
func upstreamMessage(r io.Reader) (msg, typ string) {
	s := snippet(r)
	var e struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(s), &e) == nil {
		var nested struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}
		if json.Unmarshal(e.Error, &nested) == nil && nested.Message != "" {
			return nested.Message, nested.Type
		}
		var sentence string // Ollama's native error shape.
		if json.Unmarshal(e.Error, &sentence) == nil && sentence != "" {
			return sentence, ""
		}
		if e.Message != "" {
			return e.Message, e.Type
		}
	}
	return s, ""
}

// upstreamStatusErr maps a non-2xx status from a proxied POST (006 promise 8). 400 and 422 are about
// the friend's own request (a schema the model cannot follow, an image it cannot take, a field it
// rejects): 400 invalid_request carrying the engine's sentence, so their client does not retry
// against a "broken host" — except the one 400 that is the gateway's own case, the prompt not
// fitting the context (036: the engine's count and the pre-check's can still differ by a token at
// the ceiling, and the floor of 16 output tokens overshoots it by design), which is 422
// context_too_long in the gateway's words, the engine's kept for the host's log. Everything else —
// 5xx, a redirect the gateway refused to follow, 401/404 from a misconfigured upstream — is the
// host's problem: 502 upstream_error.
func (q *request) upstreamStatusErr(resp *http.Response) *gwError {
	msg, typ := upstreamMessage(resp.Body)
	lower := strings.ToLower(msg)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && hasImageParts(q.n.body["messages"]) &&
		(strings.Contains(lower, "image") || strings.Contains(lower, "multimodal") || strings.Contains(lower, "multi-modal")) && !contextOverflow(typ, msg) {
		return errf(CodeImagesNotSupported, 0, "%s", msg)
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
		if contextOverflow(typ, msg) {
			q.g.logf("gateway: the engine rejected a prompt the pre-check counted at %d tokens as over its context: %s", q.prompt, msg)
			if eff := q.effContext(); eff > 0 {
				return errf(CodeContextTooLong, 0, "the prompt (%d tokens) leaves no room in the model's context (%d) for a reply; shorten the conversation", q.prompt, eff)
			}
			return errf(CodeContextTooLong, 0, "the prompt does not fit the model's context; shorten the conversation")
		}
		return errf(CodeInvalidRequest, 0, "the host's engine rejected this request (HTTP %d): %s", resp.StatusCode, msg)
	}
	return errf(CodeUpstreamError, 0, "upstream returned HTTP %d: %s", resp.StatusCode, msg)
}

// hasImageParts checks structure, never text that happens to mention an image.
func hasImageParts(v any) bool {
	messages, _ := v.([]any)
	for _, message := range messages {
		m, _ := message.(map[string]any)
		parts, _ := m["content"].([]any)
		for _, part := range parts {
			p, _ := part.(map[string]any)
			if p["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

// contextOverflow is the engine saying the prompt (plus the output it was asked for) does not fit
// its context, in the engines' own words as read on 2026-09-03: llama.cpp b9553 answers type
// "exceed_context_size_error" ("request (N tokens) exceeds the available context size (M tokens),
// try increasing it"); vLLM 0.25 "This model's maximum context length is M tokens. However, …".
// Anything else a 400 says is about the friend's request, not its length.
func contextOverflow(typ, msg string) bool {
	return typ == "exceed_context_size_error" || strings.Contains(msg, "exceeds the available context size") || strings.Contains(msg, "maximum context length is")
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
	PromptDetails    struct {
		Cached int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		Reasoning int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
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

type chatToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type chatDelta struct {
	Content          string         `json:"content"`
	ReasoningContent *string        `json:"reasoning_content"`
	Reasoning        string         `json:"reasoning"`
	Refusal          string         `json:"refusal"`
	ToolCalls        []chatToolCall `json:"tool_calls"`
}

func (d chatDelta) reasoning() string {
	if d.ReasoningContent != nil {
		return *d.ReasoningContent
	}
	return d.Reasoning
}

// Both wire formats observe original chat deltas, never the translated envelope.
type streamMeter struct {
	chunks     int
	sawUsage   bool
	completion strings.Builder
}

func (m *streamMeter) observe(q *request, ch sseChunk) {
	if ch.Usage != nil {
		m.sawUsage = true
		q.applyUsage(ch.Usage)
	}
	for _, c := range ch.Choices {
		work := c.Delta.Content != "" || c.Delta.reasoning() != ""
		for _, call := range c.Delta.ToolCalls {
			work = work || call.Function.Arguments != ""
		}
		if work {
			m.chunks++
		}
		if q.g.cfg.LogPrompts {
			m.completion.WriteString(c.Delta.Content)
		}
	}
}
func (m *streamMeter) finish(q *request) {
	if !m.sawUsage {
		q.ev.CompletionTokens = m.chunks
	}
	if q.g.cfg.LogPrompts {
		q.ev.Completion = m.completion.String()
	}
}

type chatChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}
type sseChunk struct {
	Usage   *usageT         `json:"usage"`
	Choices []chatChoice    `json:"choices"`
	Error   json.RawMessage `json:"error"`
}

// pipeStream copies SSE bytes to the friend verbatim, flushing at every event boundary, while
// reading each data: payload for usage. The head is written here unless the request queued for
// its slot, in which case it went out then (018) and the engine's bytes follow on the same response. When the stream ends without a usage chunk (the friend hit
// stop, or the engine omitted it) completion tokens are estimated as the number of delta chunks seen
// (llama.cpp and vLLM emit one token per chunk) so an aborted stream is not free; prompt tokens fall
// back to the pre-check count. A write or flush error means the friend is gone or has stopped
// reading (the per-line write deadline fired); a read error means the friend left, the engine went
// idle, or it died: all Cut. EOF is the end.
func (q *request) pipeStream(body io.Reader) *gwError {
	if !q.wroteHeader {
		q.streamHead()
	}

	br := bufio.NewReaderSize(body, 64<<10)
	meter := &streamMeter{}
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
					meter.observe(q, ch)
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
	meter.finish(q)
	return result
}

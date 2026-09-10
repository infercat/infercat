package gateway

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Items are append-only during this request; their ids and output indices never
// depend on engine chunk boundaries. Function arguments may interleave by index.
type responseItem struct {
	payload   strings.Builder
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	Status    string           `json:"status,omitempty"`
	Role      string           `json:"role,omitempty"`
	Content   []map[string]any `json:"content,omitempty"`
	Summary   []map[string]any `json:"summary,omitempty"`
	CallID    string           `json:"call_id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Namespace string           `json:"namespace,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
}

// Required empty arrays and argument strings must survive item-added events.
func (i *responseItem) MarshalJSON() ([]byte, error) {
	v := map[string]any{"id": i.ID, "type": i.Type, "status": i.Status}
	switch i.Type {
	case "message":
		v["role"], v["content"] = i.Role, i.Content
	case "reasoning":
		v["summary"] = i.Summary
	case "function_call":
		v["call_id"], v["name"], v["arguments"] = i.CallID, i.Name, i.Arguments
		if i.Namespace != "" {
			v["namespace"] = i.Namespace
		}
	}
	return json.Marshal(v)
}

type responseCall struct {
	name, id string
	item     *responseItem
}
type responseOutput struct {
	sawChoice                bool
	q                        *request
	id                       string
	items                    []*responseItem
	text, reasoning, refusal *responseItem
	calls                    map[int]*responseCall
	finishReason             string
	seq                      int
	usage                    *usageT
	emit                     bool
}

func newResponseOutput(q *request, emit bool) *responseOutput {
	return &responseOutput{q: q, id: responseID("resp"), items: []*responseItem{}, calls: map[int]*responseCall{}, emit: emit}
}
func newResponseID() string { return rand.Text() }
func (o *responseOutput) envelope(status string) map[string]any {
	value := map[string]any{"id": o.id, "object": "response", "created_at": o.q.start.Unix(), "status": status, "model": o.q.n.model, "output": o.items, "store": false, "error": nil, "incomplete_details": nil}
	if status != "in_progress" {
		cached, reasoning := 0, 0
		if o.usage != nil {
			cached = o.usage.PromptDetails.Cached
			reasoning = o.usage.CompletionDetails.Reasoning
		}
		value["usage"] = map[string]any{"input_tokens": o.q.ev.PromptTokens, "output_tokens": o.q.ev.CompletionTokens, "total_tokens": o.q.ev.PromptTokens + o.q.ev.CompletionTokens, "input_tokens_details": map[string]any{"cached_tokens": cached}, "output_tokens_details": map[string]any{"reasoning_tokens": reasoning}}
	}
	if status == "incomplete" {
		reason := "max_output_tokens"
		if o.finishReason == "content_filter" {
			reason = "content_filter"
		}
		value["incomplete_details"] = map[string]any{"reason": reason}
	}
	return value
}
func (o *responseOutput) event(kind string, fields map[string]any) *gwError {
	if !o.emit {
		return nil
	}
	fields["type"], fields["sequence_number"], fields["response_id"] = kind, o.seq, o.id
	o.seq++
	b, err := json.Marshal(fields)
	if err != nil {
		return errf(CodeUpstreamError, 0, "could not encode response event")
	}
	if len(b) > maxUpstreamBody {
		return errf(CodeUpstreamError, 0, "engine response exceeds the output size limit")
	}
	o.q.armWrite()
	if _, err = io.WriteString(o.q.w, "event: "+kind+"\ndata: "+string(b)+"\n\n"); err != nil {
		return errf(CodeClientClosed, 0, "client stopped reading")
	}
	if err = o.q.rc.Flush(); err != nil {
		return errf(CodeClientClosed, 0, "client stopped reading")
	}
	o.q.markTTFT()
	return nil
}
func (o *responseOutput) index(item *responseItem) int {
	for i, candidate := range o.items {
		if candidate == item {
			return i
		}
	}
	panic("response item not owned")
}
func (o *responseOutput) fields(item *responseItem) map[string]any {
	return map[string]any{"item_id": item.ID, "output_index": o.index(item)}
}
func (o *responseOutput) add(item *responseItem) *gwError {
	o.items = append(o.items, item)
	fields := o.fields(item)
	fields["item"] = item
	return o.event("response.output_item.added", fields)
}
func (o *responseOutput) textDelta(text, kind string) *gwError {
	if text == "" {
		return nil
	}
	dest, itemType, prefix, partType, partKey, partIndex := &o.text, "message", "response.output_text", "output_text", "text", "content_index"
	if kind == "reasoning" {
		dest, itemType, prefix, partType, partIndex = &o.reasoning, "reasoning", "response.reasoning_summary_text", "summary_text", "summary_index"
	}
	if kind == "refusal" {
		dest, prefix, partType, partKey = &o.refusal, "response.refusal", "refusal", "refusal"
	}
	if *dest == nil {
		item := &responseItem{ID: responseID("item"), Type: itemType, Status: "in_progress"}
		if itemType == "message" {
			item.Role = "assistant"
			item.Content = []map[string]any{}
		}
		if itemType == "reasoning" {
			item.Summary = []map[string]any{}
		}
		*dest = item
		if err := o.add(item); err != nil {
			return err
		}
		part := map[string]any{"type": partType, partKey: ""}
		event := "response.content_part.added"
		if itemType == "reasoning" {
			item.Summary = append(item.Summary, part)
			event = "response.reasoning_summary_part.added"
		} else {
			if kind == "text" {
				part["annotations"] = []any{}
			}
			item.Content = append(item.Content, part)
		}
		fields := o.fields(item)
		fields[partIndex], fields["part"] = 0, part
		if err := o.event(event, fields); err != nil {
			return err
		}
	}
	item := *dest
	item.payload.WriteString(text)
	fields := o.fields(item)
	fields[partIndex], fields["delta"] = 0, text
	return o.event(prefix+".delta", fields)
}
func (o *responseOutput) toolDelta(delta chatToolCall) *gwError {
	if delta.Type != "" && delta.Type != "function" {
		return errf(CodeUpstreamError, 0, "engine returned a non-function tool call")
	}
	if delta.Index < 0 {
		return errf(CodeUpstreamError, 0, "engine returned an invalid tool index")
	}
	call := o.calls[delta.Index]
	if call == nil {
		call = &responseCall{}
		o.calls[delta.Index] = call
	}
	if call.item != nil && (delta.Function.Name != "" || delta.ID != "") {
		return errf(CodeUpstreamError, 0, "engine changed a function identity after arguments started")
	}
	call.name += delta.Function.Name
	call.id += delta.ID
	if delta.Function.Arguments != "" {
		if err := o.startCall(call); err != nil {
			return err
		}
		call.item.payload.WriteString(delta.Function.Arguments)
		fields := o.fields(call.item)
		fields["delta"] = delta.Function.Arguments
		return o.event("response.function_call_arguments.delta", fields)
	}
	return nil
}
func (o *responseOutput) startCall(call *responseCall) *gwError {
	if call.item != nil {
		return nil
	}
	original, ok := o.q.responses.tools[call.name]
	if !ok {
		return errf(CodeUpstreamError, 0, "engine called an undeclared function")
	}
	if call.id == "" {
		call.id = responseID("call")
	}
	for _, other := range o.calls {
		if other != call && other.item != nil && other.id == call.id {
			return errf(CodeUpstreamError, 0, "engine reused a function call id")
		}
	}
	call.item = &responseItem{ID: responseID("fc"), Type: "function_call", Status: "in_progress", CallID: call.id, Name: original.Name, Namespace: original.Namespace}
	return o.add(call.item)
}
func (o *responseOutput) chunk(ch sseChunk) *gwError {
	if ch.Error != nil && !bytes.Equal(ch.Error, []byte("null")) {
		return errf(CodeUpstreamError, 0, "engine reported a stream error")
	}
	if ch.Usage != nil {
		o.usage = ch.Usage
	}
	if len(ch.Choices) > 1 {
		return errf(CodeUpstreamError, 0, "engine returned multiple choices for one response")
	}
	for _, choice := range ch.Choices {
		o.sawChoice = true
		if choice.Index != 0 || o.finishReason != "" {
			return errf(CodeUpstreamError, 0, "engine sent a choice after response finalisation")
		}
		if err := o.textDelta(choice.Delta.reasoning(), "reasoning"); err != nil {
			return err
		}
		if err := o.textDelta(choice.Delta.Content, "text"); err != nil {
			return err
		}
		if err := o.textDelta(choice.Delta.Refusal, "refusal"); err != nil {
			return err
		}
		for _, call := range choice.Delta.ToolCalls {
			if err := o.toolDelta(call); err != nil {
				return err
			}
		}
		if choice.FinishReason != nil {
			o.finishReason = *choice.FinishReason
		}
	}
	return nil
}
func (o *responseOutput) complete() (map[string]any, *gwError) {
	if !o.sawChoice {
		return nil, errf(CodeUpstreamError, 0, "engine returned no response choice")
	}
	// Sort pending metadata-only calls by engine index, not map iteration order.
	for _, index := range sortedCallIndices(o.calls) {
		if err := o.startCall(o.calls[index]); err != nil {
			return nil, err
		}
	}
	status := "completed"
	switch o.finishReason {
	case "", "stop", "tool_calls":
	case "length", "content_filter":
		status = "incomplete"
	default:
		return nil, errf(CodeUpstreamError, 0, "engine returned an unsupported finish reason")
	}
	for _, item := range o.items {
		switch item.Type {
		case "message", "reasoning":
			parts, idx, partDone := item.Content, "content_index", "response.content_part.done"
			if item.Type == "reasoning" {
				parts, idx, partDone = item.Summary, "summary_index", "response.reasoning_summary_part.done"
			}
			for i, part := range parts {
				prefix, key := "response.output_text", "text"
				if item.Type == "reasoning" {
					prefix = "response.reasoning_summary_text"
				}
				if str(part, "type") == "refusal" {
					prefix, key = "response.refusal", "refusal"
				}
				part[key] = item.payload.String()
				fields := o.fields(item)
				fields[idx], fields[key] = i, part[key]
				if err := o.event(prefix+".done", fields); err != nil {
					return nil, err
				}
				fields = o.fields(item)
				fields[idx], fields["part"] = i, part
				if err := o.event(partDone, fields); err != nil {
					return nil, err
				}
			}
		case "function_call":
			item.Arguments = item.payload.String()
			fields := o.fields(item)
			fields["arguments"], fields["name"] = item.Arguments, item.Name
			if err := o.event("response.function_call_arguments.done", fields); err != nil {
				return nil, err
			}
		}
		item.Status = status
		fields := o.fields(item)
		fields["item"] = item
		if err := o.event("response.output_item.done", fields); err != nil {
			return nil, err
		}
	}
	value := o.envelope(status)
	if err := o.event("response."+status, map[string]any{"response": value}); err != nil {
		return nil, err
	}
	return value, nil
}

func (q *request) pipeResponsesStream(body io.Reader) (result *gwError) {
	if !q.wroteHeader {
		q.streamHead()
	}
	meter := &streamMeter{}
	defer func() {
		meter.finish(q)
		if result != nil {
			q.outcome = outcomeCut
		}
	}()
	o := newResponseOutput(q, true)
	for _, kind := range []string{"response.created", "response.in_progress"} {
		if err := o.event(kind, map[string]any{"response": o.envelope("in_progress")}); err != nil {
			return err
		}
	}
	// The final Response repeats its items: cap accumulated source bytes as well
	// as each SSE event, so an engine cannot force an unbounded output assembly.
	limited := &io.LimitedReader{R: body, N: maxUpstreamBody + 1}
	reader := bufio.NewReader(limited)
	var data []byte
	done := false
	consume := func() *gwError {
		payload := bytes.TrimSpace(data)
		data = nil
		if len(payload) == 0 {
			return nil
		}
		if done {
			return errf(CodeUpstreamError, 0, "engine sent data after stream end")
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			done = true
			return nil
		}
		var chunk sseChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return errf(CodeUpstreamError, 0, "engine returned invalid stream JSON")
		}
		meter.observe(q, chunk)
		return o.chunk(chunk)
	}
	for {
		line, err := reader.ReadBytes('\n')
		if limited.N == 0 {
			return errf(CodeUpstreamError, 0, "engine response exceeds the output size limit")
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if e := consume(); e != nil {
				return e
			}
		} else if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			data = append(data, bytes.TrimPrefix(value, []byte(" "))...)
			data = append(data, '\n')
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return q.upstreamErr(err)
			}
			if e := consume(); e != nil {
				return e
			}
			break
		}
		if done {
			break
		}
	}
	if q.r.Context().Err() != nil {
		return errf(CodeClientClosed, 0, "client went away")
	}
	if !done && o.finishReason == "" {
		return errf(CodeUpstreamError, 0, "engine stream ended before completion")
	}
	meter.finish(q)
	_, result = o.complete()
	return result
}
func (q *request) pipeResponsesBody(body io.Reader) *gwError {
	raw, err := io.ReadAll(io.LimitReader(body, maxUpstreamBody+1))
	if err != nil {
		q.outcome = outcomeEngineErr
		if q.r.Context().Err() != nil {
			q.outcome = outcomeCut
		}
		return q.upstreamErr(err)
	}
	q.outcome = outcomeEngineErr
	if len(raw) > maxUpstreamBody {
		return errf(CodeUpstreamError, 0, "engine response exceeds the output size limit")
	}
	var chat struct {
		Choices []struct {
			Index        int       `json:"index"`
			Message      chatDelta `json:"message"`
			FinishReason *string   `json:"finish_reason"`
		} `json:"choices"`
		Usage *usageT         `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &chat) != nil || len(chat.Choices) != 1 || (chat.Error != nil && !bytes.Equal(chat.Error, []byte("null"))) {
		return errf(CodeUpstreamError, 0, "engine returned an invalid chat response")
	}
	q.applyUsage(chat.Usage)
	choice := chat.Choices[0]
	// Non-stream chat reasoning is not an encrypted Responses reasoning item.
	choice.Message.Reasoning, choice.Message.ReasoningContent = "", nil
	for i := range choice.Message.ToolCalls {
		choice.Message.ToolCalls[i].Index = i
	}
	if q.g.cfg.LogPrompts {
		q.ev.Completion = choice.Message.Content
	}
	o := newResponseOutput(q, false)
	o.usage = chat.Usage
	chunk := sseChunk{Choices: []chatChoice{{Index: choice.Index, Delta: choice.Message, FinishReason: choice.FinishReason}}}
	if e := o.chunk(chunk); e != nil {
		return e
	}
	value, e := o.complete()
	if e != nil {
		return e
	}
	data, err := json.Marshal(value)
	if err != nil {
		return errf(CodeUpstreamError, 0, "could not encode response")
	}
	if len(data) > maxUpstreamBody {
		return errf(CodeUpstreamError, 0, "engine response exceeds the output size limit")
	}
	q.w.Header().Set("Content-Type", "application/json")
	q.w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	q.writeHeader(http.StatusOK)
	q.markTTFT()
	if _, err := q.w.Write(data); err != nil {
		q.outcome = outcomeCut
		return errf(CodeClientClosed, 0, "client stopped reading")
	}
	q.outcome = outcomeNone
	return nil
}

func sortedCallIndices(calls map[int]*responseCall) []int {
	indices := make([]int, 0, len(calls))
	for i := range calls {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	return indices
}

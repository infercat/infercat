package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/usage"
)

type chatRunInput struct {
	Body            map[string]any `json:"body"`
	Delivery        string         `json:"delivery"`
	Conversation    string         `json:"conversation,omitempty"`
	ClientRequestID string         `json:"client_request_id,omitempty"`
}
type chatDelivery struct {
	mu     sync.Mutex
	runID  string
	key    string
	ready  chan struct{}
	done   chan struct{}
	deltas chan chatDelta
	output map[string]any
	err    error
	finish string
}

// Body routing is still behind early key admission. The returned continuation
// runs only after that request owner has finished and returned its admission.
func (q *request) routeHostTools() *gwError {
	if q.r.URL.Path != string(chatEndpoint) {
		return nil
	}
	enabled, err := asksHostTools(q.n.body)
	if err != nil {
		return err
	}
	delete(q.n.body, "host_tools")
	if !enabled {
		return nil
	}
	offer, _ := q.g.imageOffer(q.key)
	if offer == nil {
		return nil
	}
	if q.g.runs == nil {
		return errf(CodeNotFound, 0, "chat runs unavailable")
	}
	input := chatRunInput{Body: maps.Clone(q.n.body), Delivery: rand.Text()}
	for name, dest := range map[string]*string{"conversation": &input.Conversation, "client_request_id": &input.ClientRequestID} {
		if value, ok := input.Body[name]; ok {
			s, ok := value.(string)
			if !ok || len(s) > 128 {
				return errf(CodeInvalidRequest, 0, "%s must be text of at most 128 bytes", name)
			}
			*dest = s
			delete(input.Body, name)
		}
	}
	// Validate normalization/model policy before durable creation; each attempt
	// repeats it against current key/engine state before doing any model work.
	if _, e := normalize(chatEndpoint, maps.Clone(input.Body), q.key, q.destination.Up.Info().Models, q.g.cfg.ModelsPinned); e != nil {
		return e
	}
	raw, e := json.Marshal(input)
	if e != nil || len(raw) > runstate.MaxInput {
		return errf(CodeBodyTooLarge, 0, "host-tool chat input exceeds the run input limit")
	}
	d := &chatDelivery{key: q.key.ID, ready: make(chan struct{}), done: make(chan struct{}), deltas: make(chan chatDelta, 32)}
	d.mu.Lock()
	q.g.chatDeliveries.Store(input.Delivery, d)
	run, e := q.g.runs.Submit(q.key.ID, "chat", "interactive", raw, input.ClientRequestID)
	if e != nil {
		q.g.chatDeliveries.Delete(input.Delivery)
		d.mu.Unlock()
		return runError(e)
	}
	d.runID = run.ID
	d.mu.Unlock()
	q.ev.Code = usage.RunHandoff
	q.ev.Status = http.StatusAccepted
	q.continueChat = func() { defer q.g.chatDeliveries.Delete(input.Delivery); q.followChat(run.ID, d) }
	return nil
}

func (g *Gateway) chatRun(ctx context.Context, w *runstate.Work) (json.RawMessage, error) {
	var input chatRunInput
	if json.Unmarshal(w.Run.Input, &input) != nil {
		return nil, runstate.ErrInvalid
	}
	value, ok := g.chatDeliveries.Load(input.Delivery)
	if !ok {
		return nil, errors.New("chat connection unavailable; not replayed")
	}
	d := value.(*chatDelivery)
	d.mu.Lock()
	owns := d.key == w.Run.KeyID && d.runID == w.Run.ID
	d.mu.Unlock()
	// A copied input cannot claim another run's delivery or execute its tool twice.
	if !owns || !g.chatDeliveries.CompareAndDelete(input.Delivery, d) {
		return nil, runstate.ErrConflict
	}
	defer close(d.done)
	select {
	case <-d.ready:
	case <-ctx.Done():
		d.err = ctx.Err()
		return nil, d.err
	}
	raw, err := g.executeChat(ctx, w, input, d)
	d.err = err
	return raw, err
}
func (g *Gateway) executeChat(ctx context.Context, w *runstate.Work, input chatRunInput, d *chatDelivery) (json.RawMessage, error) {
	body := maps.Clone(input.Body)
	key, err := g.chatKey(ctx, w.Run.KeyID)
	if err != nil {
		return nil, err
	}
	offer, _ := g.imageOffer(key)
	if offer == nil {
		return nil, errors.New("image capability no longer available")
	}
	body["tools"] = []any{makeImageTool(offer.QueueCap)}
	body["parallel_tool_calls"] = false
	body["stream"] = true
	var text, thought strings.Builder
	var prompt, completion int
	var jobs []string
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reply := &hostToolReply{}
		result, err := w.Step(runstate.Step{Purpose: "chat", Route: string(chatEndpoint), Input: raw, Observe: func(b []byte) error {
			return reply.consume(b, func(delta chatDelta) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case d.deltas <- delta:
					return nil
				default:
					return errors.New("chat reader stopped keeping up")
				}
			})
		}})
		prompt += result.Usage.PromptTokens
		completion += result.Usage.CompletionTokens
		text.WriteString(reply.content.String())
		thought.WriteString(reply.reasoning.String())
		if err != nil {
			return nil, err
		}
		if !reply.finished || (reply.finishReason != "stop" && reply.finishReason != "tool_calls" && reply.finishReason != "length") {
			return nil, errors.New("model stream ended before completion")
		}
		if len(reply.calls) == 0 {
			d.finish = reply.finishReason
			break
		}
		if reply.finishReason == "length" {
			return nil, errors.New("model stopped before completing its tool call")
		}
		if attempt == 1 {
			_, err = g.chatTool(ctx, w, input, reply.calls, false)
			if err != nil {
				return nil, err
			}
			return nil, errors.New("one image request per turn; the second tool call was refused")
		}
		toolResult, e := g.chatTool(ctx, w, input, reply.calls, true)
		if e != nil {
			return nil, e
		}
		jobs = append(jobs, toolResult.Jobs...)
		messages, ok := body["messages"].([]any)
		if !ok {
			return nil, errors.New("chat messages must be an array")
		}
		messages = append(append([]any{}, messages...), reply.message())
		content, _ := json.Marshal(toolResult)
		for _, call := range reply.calls {
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(content)})
		}
		body["messages"] = messages
		body["tool_choice"] = "none"
	}
	response := map[string]any{"id": w.Run.ID, "object": "chat.completion", "run_id": w.Run.ID, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text.String(), "reasoning_content": thought.String()}, "finish_reason": d.finish}}, "usage": map[string]int{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion}}
	d.output = response
	return json.Marshal(map[string]any{"text": text.String(), "job_ids": jobs, "response": response})
}

type imageToolResult struct {
	Jobs  []string `json:"job_ids,omitempty"`
	Error string   `json:"error,omitempty"`
}

func (g *Gateway) chatTool(ctx context.Context, w *runstate.Work, input chatRunInput, calls []chatToolCall, execute bool) (result imageToolResult, err error) {
	release, err := w.Store.Admit(w.Run.KeyID, w.Run.ID, 32<<10)
	if err != nil {
		return result, err
	}
	defer release()
	step := runstate.StepEvent{ID: "tool_" + rand.Text(), Type: "step", At: time.Now().UTC(), Kind: "other", Name: "Make image", Tool: "make_image", Status: "running", Result: "submitting image jobs; outcome not confirmed"}
	if err = w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, nil, nil, step); err != nil {
		return result, err
	}
	result.Error = "one image request per turn"
	if execute && len(calls) == 1 && calls[0].Function.Name == "make_image" {
		var key *keys.Key
		key, err = g.chatKey(ctx, w.Run.KeyID)
		if err != nil {
			result.Error = err.Error()
		} else if offer, _ := g.imageOffer(key); offer == nil {
			result.Error = "image capability no longer available"
		} else {
			args, e := parseImageTool(calls[0].Function.Arguments, offer.QueueCap)
			if e != nil {
				result.Error = e.Error()
			} else if ctx.Err() != nil {
				result.Error = "chat cancelled before image submission"
			} else {
				inputs := make([]json.RawMessage, args.Count)
				for i := range inputs {
					inputs[i], _ = json.Marshal(runstate.ImageInput{Prompt: args.Prompt, Conversation: input.Conversation, ClientRequestID: input.ClientRequestID, ParentRunID: w.Run.ID, ToolCallID: step.ID})
				}
				rows, e := g.runs.SubmitBatch(w.Run.KeyID, "image", "interactive", inputs)
				if e != nil {
					result.Error = runError(e).Message
				} else {
					result.Error = ""
					for _, r := range rows {
						result.Jobs = append(result.Jobs, r.ID)
					}
				}
			}
		}
	}
	step.Status = "done"
	step.Result = fmt.Sprintf("submitted %d image jobs", len(result.Jobs))
	if result.Error != "" {
		step.Status = "failed"
		step.Result = result.Error
	}
	state, _ := json.Marshal(result)
	if e := w.Store.Retain(w.Run.KeyID, w.Run.ID, state, nil, nil, step); e != nil {
		return result, e
	}
	return result, nil
}
func (g *Gateway) chatKey(ctx context.Context, id string) (*keys.Key, error) {
	all, err := g.store.List(ctx)
	if err != nil {
		return nil, errors.New("key store unavailable")
	}
	for _, k := range all {
		if k.ID == id {
			if k.Status != keys.Active {
				return nil, errors.New("key is not active")
			}
			copy := *k
			return &copy, nil
		}
	}
	return nil, runstate.ErrNotFound
}

func (q *request) followChat(id string, d *chatDelivery) {
	close(d.ready)
	// A disconnected chat has Stop semantics, unlike an independently submitted image.
	defer func() { _, _ = q.g.runs.Cancel(q.key.ID, id) }()
	stream, _ := q.n.body["stream"].(bool)
	if stream {
		q.streamHead()
		raw, _ := json.Marshal(map[string]string{"run_id": id})
		if _, err := fmt.Fprintf(q.w, "event: run\ndata: %s\n\n", raw); err != nil {
			return
		}
		if q.rc.Flush() != nil {
			return
		}
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// A queued run may be cancelled before its consumer claims the delivery.
	statePoll := time.NewTicker(100 * time.Millisecond)
	defer statePoll.Stop()
	done := d.done
	for {
		select {
		case <-q.r.Context().Done():
			return
		case delta := <-d.deltas:
			if stream {
				if err := q.chatDelta(id, delta); err != nil {
					return
				}
			}
		case <-statePoll.C:
			r, err := q.g.runs.Store.Get(q.key.ID, id)
			if err != nil {
				q.fail(runError(err))
				return
			}
			if r.State == runstate.Failed || r.State == runstate.Cancelled {
				select {
				case <-d.done:
					continue
				default:
				} // Drain an owned consumer before its terminal error.
				q.fail(errf(CodeUpstreamError, 0, "host-tool chat ended: %s", r.Reason))
				return
			}
		case <-ticker.C:
			if stream {
				q.armWrite()
				if _, err := fmt.Fprint(q.w, ": running\n\n"); err != nil || q.rc.Flush() != nil {
					return
				}
			}
		case <-done:
			done = nil
			for len(d.deltas) > 0 {
				delta := <-d.deltas
				if stream {
					if err := q.chatDelta(id, delta); err != nil {
						return
					}
				}
			}
			// The consumer returns before Manager commits the final state. Never announce
			// completion until that durable decision is readable.
			deadline := time.NewTimer(30 * time.Second)
			poll := time.NewTicker(10 * time.Millisecond)
			defer deadline.Stop()
			defer poll.Stop()
			for {
				r, err := q.g.runs.Store.Get(q.key.ID, id)
				if err != nil {
					q.fail(runError(err))
					return
				}
				if r.State == runstate.Done || r.State == runstate.Failed || r.State == runstate.Cancelled {
					if r.State != runstate.Done || d.err != nil {
						inner := errf(CodeUpstreamError, 0, "host-tool chat ended: %s", r.Reason)
						_ = errors.As(d.err, &inner)
						q.fail(inner)
						return
					}
					if stream {
						raw, _ := json.Marshal(map[string]any{"id": id, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": d.finish}}, "usage": d.output["usage"]})
						q.armWrite()
						fmt.Fprintf(q.w, "data: %s\n\ndata: [DONE]\n\n", raw)
						_ = q.rc.Flush()
					} else {
						q.writeJSON(d.output)
					}
					return
				}
				select {
				case <-q.r.Context().Done():
					return
				case <-deadline.C:
					q.fail(errf(CodeUpstreamError, 0, "chat completion was not confirmed"))
					return
				case <-poll.C:
				}
			}
		}
	}
}
func (q *request) chatDelta(id string, delta chatDelta) error {
	raw, err := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
	if err != nil {
		return err
	}
	q.armWrite()
	if _, err = fmt.Fprintf(q.w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return q.rc.Flush()
}

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

type Adapter struct {
	runtime        *Runtime
	ctx            context.Context
	cancel         context.CancelFunc
	status         RuntimeStatus
	perRun, closed bool
	workers        sync.WaitGroup
	manager        *runstate.Manager
	keys           keys.Store
	dir            string
	searchKey      string
	mu             sync.Mutex
	live           map[string]*session
}
type frame struct {
	Purpose string            `json:"purpose"`
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	RunID   string            `json:"run_id"`
	Stage   string            `json:"stage"`
	Events  []json.RawMessage `json:"events"`
	Request json.RawMessage   `json:"request"`
	Failed  bool              `json:"failed"`
	Frame   struct {
		Type      string
		AttemptID string
		Chunk     struct{ Type, Text string }
	} `json:"frame"`
}
type session struct {
	runtime    *Runtime
	generation uint64
	frames     chan json.RawMessage
	lost       chan struct{}
	bytes      atomic.Int64
	once       sync.Once
}

func StartAdapter(ctx context.Context, dir string, m *runstate.Manager, ks keys.Store, searchKey string) *Adapter {
	a := &Adapter{manager: m, keys: ks, dir: dir, searchKey: searchKey, live: map[string]*session{}}
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.perRun = true
	a.status = RuntimeStatus{State: "starting"}
	a.workers.Add(1)
	go func() { defer a.workers.Done(); _ = a.availability(a.ctx) }()
	if err := m.Register("agent", m.Consumer(a.run), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: a.stopRun, Validate: ValidateInput, Admission: func(ctx context.Context, key string) (runstate.BatchAdmission, error) {
		err := a.availability(ctx)
		return runstate.BatchAdmission{QueueLimit: runstate.MaxLiveKey}, err
	}}); err != nil {
		a.Close()
		a.mu.Lock()
		a.status = RuntimeStatus{State: "failed", LastError: "agent registration failed"}
		a.mu.Unlock()
	}
	return a
}
func (a *Adapter) stopRun(id string) error {
	a.mu.Lock()
	s := a.live[id]
	a.mu.Unlock()
	if s != nil {
		return s.runtime.StopGeneration(s.generation)
	}
	return nil
}
func workspaceFor(dataDir, key string) (string, error) {
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, "/\\") {
		return "", runstate.ErrAgentUnavailable
	}
	path := filepath.Join(dataDir, "agent", "workspaces", key)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", runstate.ErrAgentUnavailable
	}
	f, err := os.CreateTemp(path, ".admission-*")
	if err != nil {
		return "", runstate.ErrAgentUnavailable
	}
	name := f.Name()
	err = f.Close()
	_ = os.Remove(name)
	if err != nil {
		return "", runstate.ErrAgentUnavailable
	}
	return path, nil
}

// Validate before creation: preserve supported input exactly, refuse unknown parts.
func ValidateInput(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if len(raw) > runstate.MaxInput || json.Unmarshal(raw, &fields) != nil {
		return runstate.ErrInvalid
	}
	for k := range fields {
		if k != "model" && k != "messages" && k != "temperature" && k != "chat_template_kwargs" {
			return runstate.ErrInvalid
		}
	}
	var input struct {
		Model    string
		Messages []struct {
			Role    string
			Content json.RawMessage
		}
		Temperature  *float64
		ChatTemplate json.RawMessage `json:"chat_template_kwargs"`
	}
	if json.Unmarshal(raw, &input) != nil || input.Model == "" || len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != "user" {
		return runstate.ErrInvalid
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		return runstate.ErrInvalid
	}
	if len(input.ChatTemplate) > 0 {
		var settings map[string]any
		if json.Unmarshal(input.ChatTemplate, &settings) != nil || len(settings) != 1 {
			return runstate.ErrInvalid
		}
		if _, ok := settings["enable_thinking"].(bool); !ok {
			return runstate.ErrInvalid
		}
	}
	for _, m := range input.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return runstate.ErrInvalid
		}
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			continue
		}
		var parts []struct {
			Type  string
			Text  string
			Image struct{ URL string } `json:"image_url"`
		}
		if json.Unmarshal(m.Content, &parts) != nil || len(parts) == 0 {
			return runstate.ErrInvalid
		}
		for _, p := range parts {
			if p.Type == "text" {
				continue
			}
			if p.Type != "image_url" || !strings.HasPrefix(p.Image.URL, "data:image/") || !strings.Contains(p.Image.URL, ";base64,") {
				return runstate.ErrInvalid
			}
		}
	}
	return nil
}

func (a *Adapter) run(ctx context.Context, w *runstate.Work) (out json.RawMessage, err error) {
	runtime, workspace, cleanup, err := a.runtimeForRun(ctx, w.Run.KeyID)
	defer func() { cleanup(err) }()
	if err != nil {
		return nil, err
	}
	if err = waitChild(ctx, runtime); err != nil {
		return nil, err
	}
	send := func(v any) error {
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		return runtime.Send(raw)
	}
	s := &session{runtime: runtime, generation: runtime.Generation(), frames: make(chan json.RawMessage, 256), lost: make(chan struct{})}
	a.mu.Lock()
	a.live[w.Run.ID] = s
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.live, w.Run.ID); a.mu.Unlock() }()
	start, _ := json.Marshal(map[string]any{"type": "start", "id": w.Run.ID, "cwd": workspace, "input": w.Run.Input})
	if err := runtime.SendGeneration(s.generation, start); err != nil {
		return nil, runstate.Failure("runtime_lost")
	}
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	steps := map[string][]runstate.StepEvent{}
	writes := map[string]string{}
	before := map[string][]byte{}
	beforeBytes := 0
	text := ""
	modelDone := make(chan error, 1)
	modelActive := false
	defer func() {
		if modelActive {
			w.Abort()
			<-modelDone
		}
	}()
	var modelID string
	var failure error
	done := ctx.Done()
	flush := func(events []json.RawMessage, outputs map[string]runstate.Captured, updates ...runstate.StepEvent) error {
		for i := range updates {
			updates[i].Result = oneLine(updates[i].Result)
			if !updates[i].Valid() {
				updates[i] = invalidStepNote(updates[i])
			}
		}
		err := w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, events, outputs, updates...)
		if errors.Is(err, runstate.ErrInvalid) {
			events, outputs = nil, nil
			for i := range updates {
				updates[i] = invalidStepNote(updates[i])
			}
			if len(updates) == 0 {
				updates = []runstate.StepEvent{invalidStepNote()}
			}
			err = w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, events, outputs, updates...)
		}
		// Queued native deltas can arrive after Work.Step released its lease.
		if errors.Is(err, runstate.ErrLimit) && release == nil && ctx.Err() == nil {
			release, err = w.Store.Admit(w.Run.KeyID, w.Run.ID, 2*runstate.MaxOutput+8192)
			if err == nil {
				err = w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, events, outputs, updates...)
			}
		}
		return err
	}
	lastThink := time.Time{}
	var thinking *runstate.StepEvent
	for {
		select {
		case <-done:
			done = nil
			if failure == nil {
				failure = ctx.Err()
			}
			if err := send(map[string]any{"type": "cancel", "id": w.Run.ID}); err != nil {
				log.Printf("agent cancel lost for run %s: %v", w.Run.ID, err)
				go a.stopRun(w.Run.ID)
			}
		case <-s.lost:
			return nil, runstate.Failure("runtime_lost")
		case err := <-modelDone:
			modelActive = false
			response := map[string]any{"type": "model_result", "id": modelID}
			if err != nil {
				response["error"] = adapterCause(err).Error()
				if failure == nil {
					failure = adapterCause(err)
				}
			}
			if e := send(response); e != nil {
				return nil, runstate.Failure("runtime_lost")
			}
		case raw := <-s.frames:
			s.bytes.Add(-int64(len(raw)))
			var f frame
			_ = json.Unmarshal(raw, &f)
			var err error
			switch f.Type {
			case "checkpoint":
				if modelActive {
					e := <-modelDone
					modelActive = false
					response := map[string]any{"type": "model_result", "id": modelID}
					if e != nil {
						response["error"] = adapterCause(e).Error()
						if failure == nil {
							failure = adapterCause(e)
						}
					}
					if e = send(response); e != nil {
						return nil, runstate.Failure("runtime_lost")
					}
				}
				if ctx.Err() != nil {
					response := map[string]any{"type": "reply", "id": f.ID}
					if f.Stage == "terminal" {
						// One bounded final note is allowed even in a lease-free cancel window.
						var end []json.RawMessage
						for _, raw := range f.Events {
							var e struct{ Type string }
							_ = json.Unmarshal(raw, &e)
							if e.Type == "turn/end" {
								end = append(end, raw)
							}
						}
						err = flush(end, nil)
					} else {
						err = ctx.Err()
					}
					if err != nil {
						response["error"] = "cancelled settlement retention refused"
					}
					if e := send(response); e != nil {
						return nil, runstate.Failure("runtime_lost")
					}
					break
				}
				err = flush(f.Events, nil)
				if err != nil {
					_ = send(map[string]any{"type": "reply", "id": f.ID, "error": adapterCause(err).Error()})
					break
				}
				for _, ev := range f.Events {
					var e struct {
						Type string
						Time int64
						Seq  int64
						Data struct {
							CallID, Name, Arguments string
							Message                 struct {
								Content []struct {
									Type, Text, ToolCallID string
									IsError                bool
									Content                []struct{ Text string }
								}
							}
						}
					}
					if json.Unmarshal(ev, &e) != nil {
						err = flush(nil, nil, invalidStepNote())
						if err != nil {
							break
						}
						continue
					}
					var update *runstate.StepEvent
					outputs := map[string]runstate.Captured{}
					if e.Type == "assistant/message" {
						text = ""
						for _, b := range e.Data.Message.Content {
							if b.Type == "text" {
								text += b.Text
							}
						}
					}
					if e.Type == "tool/call" {
						kind := map[string]string{"web_search": "search", "bash": "run", "read": "read", "write": "write"}[e.Data.Name]
						if kind == "" {
							kind = "other"
						}
						v := runstate.StepEvent{ID: fmt.Sprintf("tool_%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", w.Run.ID, e.Seq, e.Data.CallID)))), Type: "step", At: time.UnixMilli(e.Time).UTC(), Kind: kind, Name: e.Data.Name, Tool: e.Data.Name, Status: "running"}
						steps[e.Data.CallID] = append(steps[e.Data.CallID], v)
						update = &v
						if e.Data.Name == "write" {
							var args struct {
								Path string `json:"file_path"`
							}
							_ = json.Unmarshal([]byte(e.Data.Arguments), &args)
							writes[v.ID] = args.Path
							if len(before) < 64 && beforeBytes < diffLimit {
								prior, e := captureLimit(workspace, args.Path, diffLimit-beforeBytes)
								if e == nil || errors.Is(e, os.ErrNotExist) {
									before[v.ID] = prior.Data
									beforeBytes += len(prior.Data)
								}
							}
						}
					}
					if e.Type == "tool/result" {
						for _, b := range e.Data.Message.Content {
							if b.Type != "tool-result" {
								continue
							}
							pending := steps[b.ToolCallID]
							if len(pending) == 0 {
								continue
							}
							v := pending[0]
							steps[b.ToolCallID] = pending[1:]
							result := ""
							for _, part := range b.Content {
								result += part.Text
							}
							v.Status = "done"
							if b.IsError {
								v.Status = "failed"
							}
							v.Result = oneLine(result)
							v.OutputID = "result_" + v.ID
							outputs[v.OutputID] = runstate.Captured{Name: v.Tool + " result", MIME: "text/plain; charset=utf-8", Data: []byte(result)}
							if path := writes[v.ID]; path != "" && !b.IsError {
								captured, e := capture(workspace, path)
								if e != nil {
									v.Result = v.Result[:min(len(v.Result), 160)] + " · not captured: outside the workspace or unavailable"
								} else {
									outputs["file_"+v.ID] = captured
									if prior, ok := before[v.ID]; ok {
										if diff := writeDiff(prior, captured.Data); diff != nil {
											v.OutputID = "diff_" + v.ID
											outputs[v.OutputID] = runstate.Captured{Kind: "diff", Name: "Write diff", MIME: "text/x-diff", Data: diff}
										}
									}
								}
							}
							beforeBytes -= len(before[v.ID])
							delete(before, v.ID)
							err = flush(nil, outputs, v)
							outputs = map[string]runstate.Captured{}
							if err != nil {
								break
							}
						}
					}
					if err != nil {
						break
					}
					var updates []runstate.StepEvent
					if update != nil {
						updates = append(updates, *update)
					}
					if len(outputs) > 0 || len(updates) > 0 {
						err = flush(nil, outputs, updates...)
						if err != nil {
							break
						}
					}
				}
				if err == nil && thinking != nil {
					thinking.Status = "done"
					err = flush(nil, nil, *thinking)
					thinking = nil
				}
				if release != nil {
					release()
					release = nil
				}
				if err == nil && f.Stage == "tool" {
					all, e := a.keys.List(ctx)
					err = e
					allowed := false
					for _, k := range all {
						if k.ID == w.Run.KeyID && k.Agent && k.Status == keys.Active {
							allowed = true
						}
					}
					if !allowed {
						err = runstate.Failure("key_revoked")
					}
				}
				if err == nil && f.Stage == "tool" {
					release, err = w.Store.Admit(w.Run.KeyID, w.Run.ID, 2*runstate.MaxOutput+8192)
				}
				response := map[string]any{"type": "reply", "id": f.ID}
				if err != nil {
					response["error"] = adapterCause(err).Error()
				}
				if e := send(response); e != nil {
					return nil, runstate.Failure("runtime_lost")
				}
			case "model":
				if release != nil {
					release()
					release = nil
				}
				if modelActive {
					err = runstate.ErrConflict
					break
				}
				modelID = f.ID
				modelActive = true
				go func(id, purpose string, input json.RawMessage) {
					_, e := w.Step(runstate.Step{RequireAgent: true, Purpose: purpose, Route: "/v1/chat/completions", Input: input, Observe: func(p []byte) error { return send(map[string]any{"type": "model_data", "id": id, "data": string(p)}) }})
					modelDone <- e
				}(f.ID, f.Purpose, f.Request)
			case "stream":
				if ctx.Err() != nil {
					break
				}
				if f.Frame.Type == "chunk" && f.Frame.Chunk.Type == "reasoning-delta" {
					if thinking == nil {
						thinking = &runstate.StepEvent{ID: fmt.Sprintf("think_%x", sha256.Sum256([]byte(f.Frame.AttemptID))), Type: "step", At: time.Now().UTC(), Kind: "think", Status: "running"}
					}
					thinking.Text += f.Frame.Chunk.Text
					if len(thinking.Text) > runstate.MaxOutput {
						err = runstate.ErrLimit
						break
					}
					if time.Since(lastThink) >= time.Second {
						err = flush(nil, nil, *thinking)
						lastThink = time.Now()
					}
				}
			case "approval", "approval_error":
				var request string
				valid := json.Unmarshal(f.Request, &request) == nil && strings.TrimSpace(request) != "" && len(request) <= runstate.MaxOutput && f.Type == "approval"
				wait := runstate.StepEvent{ID: f.ID, Type: "step", At: time.Now().UTC(), Kind: "wait", Status: "waiting"}
				if !valid {
					wait.Status = "failed"
					wait.Result = "Unsupported native approval request"
					_ = flush(nil, nil, wait)
					_ = send(map[string]any{"type": "reply", "id": f.ID, "error": wait.Result})
					err = runstate.Failure("approval_invalid")
					break
				}
				wait.Text = request
				if err = flush(nil, nil, wait); err != nil {
					_ = send(map[string]any{"type": "reply", "id": f.ID, "error": "retention refused"})
					break
				}
				if release != nil {
					release()
					release = nil
				}
				allow, e := w.Approval(f.ID, request)
				wait.Status = "done"
				if e != nil {
					wait.Status = "cancelled"
				}
				if ctx.Err() == nil {
					err = flush(nil, nil, wait)
				}
				if err != nil {
					e = err
				}
				// The resumed tool retains this lease through its result checkpoint.
				response := map[string]any{"type": "reply", "id": f.ID, "allow": allow}
				if e != nil {
					response["error"] = "approval unavailable"
				}
				err = send(response)
			case "model_cancel":
				w.Abort()
			case "error":
				if failure == nil {
					failure = runstate.Failure("runtime_lost")
				}
			case "settled":
				if f.Failed {
					if failure == nil {
						failure = runstate.Failure("runtime_lost")
					}
				}
				output, _ := json.Marshal(map[string]string{"text": text})
				return output, failure
			}
			if err != nil && failure == nil {
				failure = adapterCause(err)
				w.Abort()
			}
		}
	}
}

func capture(workspace, path string) (runstate.Captured, error) {
	return captureLimit(workspace, path, runstate.MaxOutput)
}

func captureLimit(workspace, path string, limit int) (runstate.Captured, error) {
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(workspace, path)
		if err != nil {
			return runstate.Captured{}, err
		}
		path = relative
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return runstate.Captured{}, err
	}
	defer root.Close()
	f, err := root.Open(path)
	if err != nil {
		return runstate.Captured{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return runstate.Captured{}, runstate.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return runstate.Captured{}, runstate.ErrLimit
	}
	return runstate.Captured{Name: filepath.Base(path), MIME: "application/octet-stream", Data: raw}, nil
}

func oneLine(s string) string {
	s = strings.SplitN(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", ""), "\n", 2)[0]
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}
func invalidStepNote(old ...runstate.StepEvent) runstate.StepEvent {
	note := runstate.StepEvent{ID: fmt.Sprintf("note_%d", time.Now().UnixNano()), Type: "step", At: time.Now().UTC(), Kind: "other", Status: "failed", Result: "step_invalid: unsupported native step"}
	if len(old) > 0 {
		replacement := note
		replacement.ID, replacement.At = old[0].ID, old[0].At
		if replacement.Valid() {
			return replacement
		}
	}
	return note
}
func adapterCause(err error) runstate.Failure {
	var cause runstate.Failure
	if errors.As(err, &cause) {
		return cause
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, runstate.ErrLimit) {
		return "retention_refused"
	}
	if errors.Is(err, runstate.ErrInvalid) {
		return "step_invalid"
	}
	return "runtime_lost"
}

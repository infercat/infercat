package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

// SetRuns is startup wiring only, before any gateway listener is exposed.
func (g *Gateway) SetRuns(m *runstate.Manager) error {
	g.runs = m
	if g.cfg.Images != nil {
		return m.Register("image", runstate.ImageKind, runstate.Policy{Serial: true, DeferredCancel: true, Validate: runstate.ValidateImage, Admission: g.prepareImageBatch, Release: g.releaseImageReservation})
	}
	return nil
}

// ExecuteStep reuses the request owner; finish settles once before the result is observed.
func (g *Gateway) ExecuteStep(ctx context.Context, keyID string, step runstate.Step, acquired func() error) (result runstate.StepResult, err error) {
	if len(step.Input) > runstate.MaxInput || !json.Valid(step.Input) {
		return result, runstate.ErrInvalid
	}
	if step.Route != string(chatEndpoint) && step.Route != string(embeddingsEndpoint) && step.Route != string(imagesEndpoint) {
		return result, runstate.ErrInvalid
	}
	sink := &runSink{ctx: ctx, header: make(http.Header)}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://host"+step.Route, bytes.NewReader(step.Input))
	q := g.newRequest(sink, r)
	q.onAcquired = acquired
	if step.Route == string(imagesEndpoint) {
		q.image = &imageRequest{runID: step.RunID}
	}
	defer func() {
		if recover() != nil {
			err = errors.New("run step pipeline failed")
		}
		q.finish()
		result.Usage = q.ev
		result.Settled = true
		result.Dispatched = q.dispatched.Load() || q.resp != nil
		if q.image != nil {
			result.Dispatched = q.dispatched.Load()
		}
		if sink.err != nil {
			err = sink.err
		}
		if err == nil && q.outcome != outcomeServed {
			err = errors.New("run step ended: " + q.ev.Code + ": " + sink.String())
		}
		if q.image != nil && len(q.image.failure) > 0 {
			result.Output = append(json.RawMessage(nil), q.image.failure...)
		}
		if err == nil {
			result.Output = append(json.RawMessage(nil), sink.Bytes()...)
			if q.n.stream {
				result.Output, _ = json.Marshal(string(result.Output))
			}
			if len(result.Output) > runstate.MaxOutput {
				result.Output = nil
				err = runstate.ErrLimit
			}
		}
	}()
	// Re-resolve by ID on every step. The run never stores or synthesizes a bearer.
	all, e := g.store.List(ctx)
	if e != nil {
		q.fail(errf(CodeUpstreamDown, 1, "key store unavailable"))
		return result, e
	}
	for _, k := range all {
		if k.ID == keyID {
			copy := *k
			q.key = &copy
			break
		}
	}
	if q.key == nil {
		q.noEvent = true
		return result, runstate.ErrNotFound
	}
	q.ev.KeyID = keyID
	if q.key.Status != keys.Active {
		code := CodeKeyPaused
		if q.key.Status == keys.Revoked {
			code = CodeKeyRevoked
		}
		q.fail(errf(code, 0, "key is not active"))
		return result, runstate.ErrInvalid
	}
	if step.Route == string(imagesEndpoint) {
		q.proxyImage(step.RunID, step.Input)
	} else {
		q.proxy(endpoint(step.Route))
	}
	return result, nil
}

type runSink struct {
	bytes.Buffer
	ctx    context.Context
	header http.Header
	err    error
}

func (s *runSink) Header() http.Header              { return s.header }
func (s *runSink) WriteHeader(int)                  {}
func (s *runSink) Flush()                           {}
func (s *runSink) SetWriteDeadline(time.Time) error { return nil }
func (s *runSink) SetReadDeadline(time.Time) error  { return nil }
func (s *runSink) Write(p []byte) (int, error) {
	if s.ctx.Err() != nil {
		s.err = s.ctx.Err()
	} else if s.Len()+len(p) > runstate.MaxOutput {
		s.err = runstate.ErrLimit
	}
	if s.err != nil {
		return 0, s.err
	}
	return s.Buffer.Write(p)
}
func runError(err error) *gwError {
	var gatewayError *gwError
	if errors.As(err, &gatewayError) {
		return gatewayError
	}
	switch {
	case errors.Is(err, runstate.ErrStopping):
		return errf(CodeUpstreamDown, retryAfterUpstreamDown, "runtime stopping; retry shortly")
	case errors.Is(err, runstate.ErrQuarantined):
		return errf(CodeUpstreamDown, retryAfterUpstreamDown, "runtime quarantined; retry after recovery")
	case errors.Is(err, runstate.ErrQueueLimit):
		return errf(CodeImageQueueFull, 1, "image queue is full")
	case errors.Is(err, runstate.ErrNotFound):
		return errf(CodeNotFound, 0, "run not found")
	case errors.Is(err, runstate.ErrLimit):
		return errf(CodeConcurrencyLimited, 1, "run storage, live-run or subscriber limit reached")
	case errors.Is(err, runstate.ErrInvalid), errors.Is(err, runstate.ErrConflict):
		return errf(CodeInvalidRequest, 0, "invalid run request or cursor")
	default:
		return errf(CodeUpstreamDown, 1, "run store unavailable")
	}
}
func (q *request) runRoute() {
	m := q.g.runs
	if m == nil {
		q.fail(errf(CodeNotFound, 0, "runs unavailable"))
		return
	}
	q.w.Header().Set("Cache-Control", "no-store")
	if q.r.Method == http.MethodGet && q.r.URL.Path == "/v1/events" {
		q.runEvents()
		return
	}
	var value any
	var err error
	status := http.StatusOK
	switch {
	case q.r.Method == http.MethodPost && q.r.URL.Path == "/v1/runs":
		// Bound control-request bodies without taking an engine admission from the new step.
		q.buffered = true
		if q.g.bodies.Add(1) > runstate.MaxLiveHost {
			q.fail(errf(CodeConcurrencyLimited, 1, "run submissions are busy"))
			return
		}
		var input struct {
			Kind     string          `json:"kind"`
			Priority string          `json:"priority"`
			Input    json.RawMessage `json:"input"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(q.w, q.r.Body, runstate.MaxInput+1024))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&input); err == nil {
			if dec.Decode(new(any)) != io.EOF {
				err = runstate.ErrInvalid
			}
		}
		_ = q.rc.SetReadDeadline(time.Time{})
		if err != nil {
			q.fail(errf(CodeInvalidRequest, 0, "expected one bounded run request"))
			return
		}
		var r runstate.Run
		r, err = m.Submit(q.key.ID, input.Kind, input.Priority, input.Input)
		value = map[string]string{"id": r.ID}
		status = http.StatusAccepted
	case strings.HasPrefix(q.r.URL.Path, "/v1/runs/"):
		id := strings.TrimPrefix(q.r.URL.Path, "/v1/runs/")
		if q.r.Method == http.MethodGet || q.r.Method == http.MethodDelete {
			q.kind = endpoint(q.r.URL.Path)
			if e := q.admitImageHTTP(); e != nil {
				q.fail(e)
				return
			}
		}
		switch q.r.Method {
		case http.MethodGet:
			value, err = m.Store.Get(q.key.ID, id)
		case http.MethodDelete:
			value, err = m.Cancel(q.key.ID, id)
			if errors.Is(err, runstate.ErrConflict) {
				var r runstate.Run
				r, err = m.Store.Get(q.key.ID, id)
				if err == nil && r.State != runstate.Done && r.State != runstate.Failed && r.State != runstate.Cancelled {
					err = runstate.ErrConflict
				}
				value = r
			}
		default:
			err = runstate.ErrNotFound
		}
	default:
		err = runstate.ErrNotFound
	}
	var ended *runstate.CommittedTerminal
	if errors.As(err, &ended) {
		value, err, status = ended.Run, nil, http.StatusOK
	}
	if err != nil {
		q.fail(runError(err))
		return
	}
	q.outcome = outcomeServed
	if r, ok := value.(runstate.Run); ok && r.Kind == "image" {
		position := 0
		if r.State == runstate.Queued {
			position = q.g.runs.Position("image", r.ID)
		}
		value = imageJob{r, position}
	}
	q.w.Header().Set("Content-Type", "application/json")
	q.writeHeader(status)
	_ = json.NewEncoder(q.w).Encode(value)
}
func (q *request) runEvents() {
	cursor := q.r.Header.Get("Last-Event-ID")
	query := q.r.URL.Query().Get("cursor")
	if cursor == "" {
		cursor = query
	} else if query != "" && query != cursor {
		q.fail(runError(runstate.ErrInvalid))
		return
	}
	if len(cursor) > 100 {
		q.fail(runError(runstate.ErrInvalid))
		return
	}
	replay, ch, stop, err := q.g.runs.Store.Subscribe(q.key.ID, cursor)
	if err != nil {
		q.fail(runError(err))
		return
	}
	defer stop()
	q.w.Header().Set("Content-Type", "text/event-stream")
	q.w.Header().Set("Cache-Control", "no-store")
	q.w.Header().Set("X-Accel-Buffering", "no")
	q.writeHeader(http.StatusOK)
	write := func(e runstate.Event) error {
		raw, _ := json.Marshal(e)
		q.armWrite()
		if _, e := io.WriteString(q.w, "id: "+e.Cursor+"\nevent: run\ndata: "+string(raw)+"\n\n"); e != nil {
			return e
		}
		return q.rc.Flush()
	}
	for _, e := range replay {
		if write(e) != nil {
			return
		}
	}
	tick := time.NewTicker(q.g.queuedEvery)
	defer tick.Stop()
	for {
		select {
		case <-q.g.runs.Done():
			return
		case <-q.r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := q.authenticate(); err != nil {
				q.fail(err)
				return
			}
			if write(e) != nil {
				return
			}
		case <-tick.C:
			if err := q.authenticate(); err != nil {
				q.fail(err)
				return
			}
			q.armWrite()
			if _, err := io.WriteString(q.w, ": keepalive\n\n"); err != nil {
				return
			}
			if q.rc.Flush() != nil {
				return
			}
		}
	}
}

func isRunRoute(path string) bool {
	return path == "/v1/runs" || strings.HasPrefix(path, "/v1/runs/") || path == "/v1/events"
}

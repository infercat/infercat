package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/infercat/infercat/internal/keys"
)

const ChunkSize = 512 << 10 // Base64 + metadata remains below the 1 MiB message cap.
const MaxBody = 4 << 20
const MaxFrame = 1 << 20
const maxSlots = 64

type frame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Status  int               `json:"status,omitempty"`
	Hashes  []string          `json:"hashes,omitempty"`
	Slots   int               `json:"slots,omitempty"`
	Data    []byte            `json:"data,omitempty"`
}

func headers(h http.Header, names ...string) map[string]string {
	out := map[string]string{}
	for _, k := range names {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}
func Register(ctx context.Context, endpoint, code string) (Config, error) {
	c := Config{Endpoint: endpoint, Host: "check", Token: strings.Repeat("0", 64)}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	b, _ := json.Marshal(map[string]string{"code": code})
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint+"/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		return Config{}, errors.New("registration outcome uncertain; ask the PM for recovery, do not retry the code")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Config{}, refusal(res.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&c); err != nil {
		return Config{}, errors.New("registration response unreadable; ask the PM for recovery")
	}
	c.Endpoint = endpoint
	return c, c.Validate()
}

type keySync struct {
	logf            func(string, ...any)
	state           *connectionState
	slots           func() int
	capacity        int
	store           *keys.FileStore
	reload, changed <-chan struct{}
}

func (k keySync) publish(ctx context.Context, send func(frame) error) error {
	hashes := []string{}
	if k.store != nil {
		list, err := k.store.List(ctx)
		if err != nil {
			_ = send(frame{Type: "keys"})
			return err
		}
		for _, key := range list {
			if key.Status == keys.Active {
				h := strings.TrimPrefix(key.SecretHash, "sha256:")
				if !strings.HasPrefix(key.SecretHash, "sha256:") || !tokenPattern.MatchString(h) {
					_ = send(frame{Type: "keys"})
					return errors.New("invalid stored key hash")
				}
				hashes = append(hashes, h)
			}
		}
	}
	return send(frame{Type: "keys", Hashes: hashes, Slots: k.capacity})
}
func Run(ctx context.Context, c Config, h http.Handler, logf func(string, ...any), syncKeys ...keySync) {
	if len(syncKeys) == 0 {
		syncKeys = []keySync{{}}
	}
	syncKeys[0].logf = logf
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := session(ctx, c, h, syncKeys...)
		if ctx.Err() != nil {
			return
		}
		if len(syncKeys) > 0 {
			message := "bridge connection failed; reconnecting"
			if errors.Is(err, context.DeadlineExceeded) {
				message = "bridge connection timed out; reconnecting"
			} else if code := websocket.CloseStatus(err); code != -1 {
				message = fmt.Sprintf("bridge connection closed (%d); reconnecting", code)
			}
			// Peer close reasons and transport errors may contain arbitrary text; expose only facts.
			syncKeys[0].state.connection(false, message)
		}
		logf("bridge connection ended (%T); reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		} else if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
func session(ctx context.Context, c Config, h http.Handler, syncKeys ...keySync) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(c.Endpoint, "https")+"/h/"+c.Host+"/socket", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + c.Token}}})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(MaxFrame)
	send := func(f frame) error {
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		if len(b) > MaxFrame {
			return errors.New("bridge frame exceeds cap")
		}
		wctx, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		return conn.Write(wctx, websocket.MessageText, b)
	}
	snapshot := keySync{}
	if len(syncKeys) > 0 {
		snapshot = syncKeys[0]
	}
	defer snapshot.state.connection(false, "")
	snapshot.capacity = 1
	if snapshot.slots != nil {
		snapshot.capacity = max(1, snapshot.slots())
	}
	if err := snapshot.publish(ctx, send); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-snapshot.reload:
				if snapshot.publish(ctx, send) != nil {
					cancel()
					return
				}
			case <-snapshot.changed:
				if snapshot.publish(ctx, send) != nil {
					cancel()
					return
				}
			case <-ticker.C:
				if send(frame{Type: "ping"}) != nil {
					cancel()
					return
				}
			}
		}
	}()
	// Only this dispatcher owns the map. Joining a cancelled handler is asynchronous,
	// so another job can still receive the acknowledgements needed to finish.
	type job struct {
		meta     frame
		body     bytes.Buffer
		done     chan struct{}
		ack      chan struct{}
		cancel   context.CancelFunc
		settling bool
	}
	jobs := map[string]*job{}
	var handlers sync.WaitGroup
	defer func() { cancel(); handlers.Wait() }()
	type incoming struct {
		f   frame
		err error
	}
	frames := make(chan incoming)
	settled := make(chan string)
	go func() {
		for {
			readCtx, stop := context.WithTimeout(ctx, 60*time.Second)
			_, b, err := conn.Read(readCtx)
			stop()
			var f frame
			if err == nil && (len(b) > MaxFrame || json.Unmarshal(b, &f) != nil) {
				err = errors.New("invalid bridge frame")
			}
			select {
			case frames <- incoming{f, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	limit, negotiated := 1, false
	for {
		var f frame
		select {
		case <-ctx.Done():
			return ctx.Err()
		case id := <-settled:
			delete(jobs, id)
			if err := send(frame{Type: "ready", ID: id}); err != nil {
				return err
			}
			continue
		case in := <-frames:
			if in.err != nil {
				return in.err
			}
			f = in.f
		}
		if f.Type == "keys_ready" {
			accepted := max(1, f.Slots) // An old Worker does not echo slots.
			if f.Slots < 0 || accepted > min(maxSlots, snapshot.capacity) || (negotiated && accepted != limit) {
				return errors.New("invalid bridge slots")
			}
			first := !negotiated
			limit, negotiated = accepted, true
			snapshot.state.connection(true, "", accepted)
			if first && snapshot.logf != nil {
				snapshot.logf("bridge connected: negotiated slots=%d", accepted)
			}
			continue
		}
		if f.Type == "pong" {
			continue
		}
		j := jobs[f.ID]
		if f.Type == "request" {
			if !negotiated || len(jobs) >= limit || j != nil || f.ID == "" || len(f.ID) > 64 || (f.Method != "GET" && f.Method != "POST") || !strings.HasPrefix(f.Path, "/v1/") || strings.ContainsAny(f.Path, "\r\n#") || len(f.Path) > 2048 {
				return errors.New("invalid bridge request")
			}
			jobs[f.ID] = &job{meta: f}
			continue
		}
		if j == nil {
			return errors.New("unexpected bridge frame")
		}
		switch f.Type {
		case "ack":
			if j.ack == nil {
				return errors.New("unexpected ack")
			}
			select {
			case j.ack <- struct{}{}:
			default:
				return errors.New("unexpected ack")
			}
		case "body":
			if j.done != nil || len(f.Data) > ChunkSize || j.body.Len()+len(f.Data) > MaxBody {
				return errors.New("bridge body exceeds cap")
			}
			j.body.Write(f.Data)
		case "end":
			if j.done != nil {
				return errors.New("duplicate request end")
			}
			requestCtx, stopRequest := context.WithCancel(context.WithValue(ctx, viaKey{}, true))
			req, err := http.NewRequestWithContext(requestCtx, j.meta.Method, j.meta.Path, bytes.NewReader(j.body.Bytes()))
			if err != nil {
				stopRequest()
				return err
			}
			j.body = bytes.Buffer{} // Request owns the bytes until its handler settles.
			j.cancel = stopRequest
			for _, k := range []string{"authorization", "content-type", "accept"} {
				req.Header.Set(k, j.meta.Headers[k])
			}
			req.RemoteAddr = "bridge"
			j.ack, j.done = make(chan struct{}, 1), make(chan struct{})
			id, completed, acks := f.ID, j.done, j.ack
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer close(completed)
				defer stopRequest()
				w := newResponse(req.Context(), id, send, acks)
				w.cancel = stopRequest
				h.ServeHTTP(w, req)
				if w.finish() != nil {
					stopRequest()
					if send(frame{Type: "cancel", ID: id}) != nil {
						cancel()
					}
				}
			}()
		case "cancel":
			if j.settling {
				continue
			}
			j.settling = true
			if j.cancel != nil {
				j.cancel()
			}
			if j.done == nil {
				delete(jobs, f.ID)
				if err := send(frame{Type: "ready", ID: f.ID}); err != nil {
					return err
				}
				continue
			}
			go func(id string, done <-chan struct{}) {
				select {
				case <-done:
				case <-ctx.Done():
					return
				}
				select {
				case settled <- id:
				case <-ctx.Done():
				}
			}(f.ID, j.done)
		case "ready":
			if j.done == nil || j.settling {
				return errors.New("unexpected ready")
			}
			// Normal ready follows our end frame, so no further ack is needed.
			select {
			case <-j.done:
			case <-ctx.Done():
				return ctx.Err()
			}
			j.cancel()
			delete(jobs, f.ID)
		default:
			return errors.New("unexpected frame type")
		}
	}
}

type response struct {
	mu      sync.Mutex
	ctx     context.Context
	id      string
	send    func(frame) error
	ack     <-chan struct{}
	header  http.Header
	sent    bool
	buf     []byte
	err     error
	stop    chan struct{}
	stopped chan struct{}
	cancel  context.CancelFunc
	ackWait time.Duration
}

func newResponse(ctx context.Context, id string, send func(frame) error, ack <-chan struct{}) *response {
	w := &response{ctx: ctx, id: id, send: send, ack: ack, header: make(http.Header), stop: make(chan struct{}), stopped: make(chan struct{}), cancel: func() {}, ackWait: 30 * time.Second}
	go func() {
		defer close(w.stopped)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				w.mu.Lock()
				w.flush()
				w.mu.Unlock()
			}
		}
	}()
	return w
}
func (w *response) Header() http.Header    { return w.header }
func (w *response) WriteHeader(status int) { w.mu.Lock(); defer w.mu.Unlock(); w.start(status) }
func (w *response) start(status int) {
	if w.sent {
		return
	}
	w.sent = true
	w.err = w.send(frame{Type: "response", ID: w.id, Status: status, Headers: headers(w.header, "content-type", "cache-control", "retry-after", "x-request-id")})
}
func (w *response) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.start(200)
	n := len(b)
	for len(b) > 0 && w.err == nil {
		take := min(len(b), ChunkSize-len(w.buf))
		w.buf = append(w.buf, b[:take]...)
		b = b[take:]
		if len(w.buf) == ChunkSize {
			w.flush()
		}
	}
	if w.err != nil {
		return 0, w.err
	}
	return n, nil
}

// Flush coalesces gateway token flushes; the timer and a full chunk perform actual writes.
func (w *response) Flush()            { _ = w.FlushError() }
func (w *response) FlushError() error { w.mu.Lock(); defer w.mu.Unlock(); return w.err }
func (w *response) flush() {
	if len(w.buf) == 0 || w.err != nil {
		return
	}
	w.err = w.send(frame{Type: "data", ID: w.id, Data: w.buf})
	w.buf = nil
	if w.err == nil {
		select {
		case <-w.ack:
		case <-w.ctx.Done():
			w.err = w.ctx.Err()
		case <-time.After(w.ackWait):
			w.cancel()
			w.err = errors.New("bridge response acknowledgement timed out")
		}
	}
}
func (w *response) finish() error {
	close(w.stop)
	<-w.stopped
	w.mu.Lock()
	defer w.mu.Unlock()
	w.start(200)
	w.flush()
	if w.err == nil {
		w.err = w.send(frame{Type: "end", ID: w.id})
	}
	return w.err
}

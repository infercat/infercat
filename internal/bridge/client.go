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

type frame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Status  int               `json:"status,omitempty"`
	Hashes  []string          `json:"hashes,omitempty"`
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
	state           *connectionState
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
	return send(frame{Type: "keys", Hashes: hashes})
}
func Run(ctx context.Context, c Config, h http.Handler, logf func(string, ...any), syncKeys ...keySync) {
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
	sync := keySync{}
	if len(syncKeys) > 0 {
		sync = syncKeys[0]
	}
	if err := sync.publish(ctx, send); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sync.reload:
				if sync.publish(ctx, send) != nil {
					cancel()
					return
				}
			case <-sync.changed:
				if sync.publish(ctx, send) != nil {
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
	var meta *frame
	var body bytes.Buffer
	var done chan struct{}
	var ack chan struct{}
	var requestCancel context.CancelFunc
	defer func() {
		cancel()
		if done != nil {
			<-done
		}
	}()
	for {
		readCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		_, b, err := conn.Read(readCtx)
		stop()
		if err != nil {
			return err
		}
		var f frame
		if len(b) > MaxFrame || json.Unmarshal(b, &f) != nil {
			return errors.New("invalid bridge frame")
		}
		if f.Type == "keys_ready" {
			sync.state.connection(true, "")
			continue
		}
		if f.Type == "pong" {
			continue
		}
		if f.Type == "ack" && meta != nil && f.ID == meta.ID && ack != nil {
			select {
			case ack <- struct{}{}:
			default:
				return errors.New("unexpected ack")
			}
			continue
		}
		if f.Type == "request" {
			if done != nil {
				select {
				case <-done:
					done = nil
				default:
					return errors.New("concurrent bridge request")
				}
			}
			if meta != nil || f.ID == "" || len(f.ID) > 64 || (f.Method != "GET" && f.Method != "POST") || !strings.HasPrefix(f.Path, "/v1/") || strings.ContainsAny(f.Path, "\r\n#") || len(f.Path) > 2048 {
				return errors.New("invalid bridge request")
			}
			meta = &f
			body.Reset()
			continue
		}
		if meta == nil || f.ID != meta.ID {
			return errors.New("unexpected bridge frame")
		}
		switch f.Type {
		case "body":
			if done != nil || len(f.Data) > ChunkSize || body.Len()+len(f.Data) > MaxBody {
				return errors.New("bridge body exceeds cap")
			}
			body.Write(f.Data)
		case "end":
			if done != nil {
				return errors.New("duplicate request end")
			}
			requestCtx, stopRequest := context.WithCancel(context.WithValue(ctx, viaKey{}, true))
			req, err := http.NewRequestWithContext(requestCtx, meta.Method, meta.Path, bytes.NewReader(body.Bytes()))
			if err != nil {
				stopRequest()
				return err
			}
			requestCancel = stopRequest
			for _, k := range []string{"authorization", "content-type", "accept"} {
				req.Header.Set(k, meta.Headers[k])
			}
			req.RemoteAddr = "bridge"
			ack = make(chan struct{}, 1)
			done = make(chan struct{})
			// Handler completion is acknowledged explicitly; the next request may only follow ready.
			id, completed, acks := meta.ID, done, ack
			go func() {
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
		case "cancel", "ready":
			if done == nil {
				return errors.New("unexpected ready")
			}
			if f.Type == "cancel" {
				requestCancel()
			}
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
			requestCancel()
			if f.Type == "cancel" {
				if err := send(frame{Type: "ready", ID: meta.ID}); err != nil {
					return err
				}
			}
			meta = nil
			ack = nil
			done = nil
		default:
			return fmt.Errorf("unexpected frame type")
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

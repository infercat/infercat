package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// FileName is the append-only event log inside the data dir.
const FileName = "usage.jsonl"

// bufferedEvents is how many events may be in flight before Record starts dropping. The request
// path must never block on disk, so a full buffer drops and counts rather than waits.
const bufferedEvents = 1024

// FileRecorder appends one JSON object per line to usage.jsonl from a single writer goroutine.
type FileRecorder struct {
	ch   chan Event
	done chan struct{}
	f    *os.File
	w    *bufio.Writer
	enc  *json.Encoder
	logf func(string, ...any)

	dropped  atomic.Int64
	warned   atomic.Int64 // unix seconds of the last drop warning
	closeOne sync.Once
	closeErr error

	mu     sync.RWMutex // Record holds it shared while it sends; Close takes it exclusively
	closed bool
}

// NewFileRecorder opens (creating) usage.jsonl under dataDir for appending.
func NewFileRecorder(dataDir string, logf func(string, ...any)) (*FileRecorder, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, FileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	r := &FileRecorder{
		ch:   make(chan Event, bufferedEvents),
		done: make(chan struct{}),
		f:    f,
		logf: logf,
	}
	r.w = bufio.NewWriter(f)
	r.enc = json.NewEncoder(r.w)
	go r.loop()
	return r, nil
}

// Record queues one event. It never blocks: if the buffer is full the event is dropped, counted,
// and warned about at most once every ten seconds.
func (r *FileRecorder) Record(ctx context.Context, e Event) {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed { // a late Record after Close is dropped and counted, never a panic (005 fix 10i)
		r.drop("recorder is closed")
		return
	}
	select {
	case r.ch <- e:
	default:
		r.drop("disk cannot keep up")
	}
}

func (r *FileRecorder) drop(why string) {
	n := r.dropped.Add(1)
	now := time.Now().Unix()
	if last := r.warned.Load(); now-last >= 10 && r.warned.CompareAndSwap(last, now) {
		r.logf("usage: dropped %d event(s) so far; %s", n, why)
	}
}

// Dropped is how many events were lost to a full buffer.
func (r *FileRecorder) Dropped() int64 { return r.dropped.Load() }

// loop is the only writer. It flushes whenever the queue drains so `usage` run from another
// process sees recent events without waiting for the process to exit.
func (r *FileRecorder) loop() {
	defer close(r.done)
	for {
		e, ok := <-r.ch
		if !ok {
			r.w.Flush()
			return
		}
		r.write(e)
		for drained := false; !drained; {
			select {
			case e, ok := <-r.ch:
				if !ok {
					r.w.Flush()
					return
				}
				r.write(e)
			default:
				drained = true
			}
		}
		if err := r.w.Flush(); err != nil {
			r.logf("usage: write failed: %v", err)
		}
	}
}

func (r *FileRecorder) write(e Event) {
	if err := r.enc.Encode(e); err != nil {
		r.logf("usage: encode failed: %v", err)
	}
}

// Close drains the queue, flushes, and closes the file. It is safe to call twice.
func (r *FileRecorder) Close() error {
	r.closeOne.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.ch)
		r.mu.Unlock()
		<-r.done
		r.closeErr = r.f.Close()
	})
	return r.closeErr
}

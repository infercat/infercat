package admin

// The event stream (ticket 029; the one new concept). Every usage event the gateway records is
// also handed, live, to whoever is listening: `serve --log-requests` on the host's terminal and
// `status --watch` over the admin socket (GET /events). usage.jsonl stays the durable record;
// this is the running one. Prompt and completion text never enter it, whatever the file holds
// (Protection 3): the terminal and the socket show counts and timings only.

import (
	"context"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/usage"
)

// tokenWindow is the span the tokens-per-second figure is averaged over.
const tokenWindow = time.Minute

// Events fans events out to subscribers and keeps the last minute of completions. It implements
// usage.Recorder in front of the durable recorder so the gateway needs no second seam.
type Events struct {
	next usage.Recorder // the durable recorder; nil for a bridge, which keeps no file
	mu   sync.Mutex
	subs map[chan usage.Event]struct{}
	done []tokenSample // completions in the last tokenWindow: when each finished, how many tokens
}

type tokenSample struct {
	at time.Time
	n  int
}

func NewEvents(next usage.Recorder) *Events {
	return &Events{next: next, subs: map[chan usage.Event]struct{}{}}
}

// Record implements usage.Recorder: the event goes to the file first, then — content removed —
// to every subscriber that is keeping up. One that is not is skipped, never waited for: the
// request path does not block on a terminal or a socket.
func (h *Events) Record(ctx context.Context, e usage.Event) {
	if h.next != nil {
		h.next.Record(ctx, e)
	}
	e.Prompt, e.Completion = "", ""
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if e.CompletionTokens > 0 {
		h.done = append(h.done, tokenSample{now, e.CompletionTokens})
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe returns a channel of every event from now on and the call that ends it.
func (h *Events) Subscribe() (<-chan usage.Event, func()) {
	ch := make(chan usage.Event, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// TokensPerSecond is the engine's completion throughput over the last minute: the completion
// tokens of every request that finished in the window, over the window. A request is counted
// where it ended, so one long stream lands whole in the minute it finished.
func (h *Events) TokensPerSecond() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-tokenWindow)
	i := 0
	for i < len(h.done) && h.done[i].at.Before(cutoff) {
		i++
	}
	h.done = h.done[i:]
	n := 0
	for _, s := range h.done {
		n += s.n
	}
	return float64(n) / tokenWindow.Seconds()
}

// ProcessStats is the host process as the Go runtime sees it, for a leak check across a load
// run (ticket 028): goroutines, live heap, and memory taken from the OS. Resident set size is
// the kernel's number; the runtime has no portable way to it, so it stays zero unless a platform
// file fills it in.
func ProcessStats() Process {
	samples := []metrics.Sample{
		{Name: "/sched/goroutines:goroutines"},
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
	}
	metrics.Read(samples)
	return Process{
		Goroutines: int(samples[0].Value.Uint64()),
		HeapBytes:  samples[1].Value.Uint64(),
		SysBytes:   samples[2].Value.Uint64(),
		RSSBytes:   rss(),
	}
}

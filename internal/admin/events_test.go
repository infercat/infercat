package admin

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/usage"
)

type memRecorder struct {
	mu  sync.Mutex
	got []usage.Event
}

func (m *memRecorder) Record(_ context.Context, e usage.Event) {
	m.mu.Lock()
	m.got = append(m.got, e)
	m.mu.Unlock()
}

// Ticket 029 promise 1: GET /events tails the recorder — every event the gateway records
// reaches a watcher, in order, without the prompt text the file may carry (Protection 3), and
// the file recorder behind the hub still gets everything, prompt included.
func TestEventStreamTailsTheRecorder(t *testing.T) {
	dir := shortDir(t)
	file := &memRecorder{}
	hub := NewEvents(file)
	s, err := Serve(dir, sample, nil, hub)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan usage.Event, 16)
	werr := make(chan error, 1)
	go func() { werr <- Watch(ctx, dir, func(e usage.Event) { seen <- e }) }()
	time.Sleep(100 * time.Millisecond) // the watcher must be subscribed before the first event

	for i := range 3 {
		hub.Record(ctx, usage.Event{KeyID: "k_1", Endpoint: "/v1/chat/completions", Status: 200, CompletionTokens: 100 + i,
			Prompt: "the friend's secret question", Completion: "and the model's answer"})
	}
	for i := range 3 {
		select {
		case e := <-seen:
			if e.CompletionTokens != 100+i || e.KeyID != "k_1" {
				t.Fatalf("event %d = %+v", i, e)
			}
			if e.Prompt != "" || e.Completion != "" {
				t.Fatalf("the stream carried prompt content: %+v", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("event %d never reached the watcher", i)
		}
	}
	file.mu.Lock()
	if len(file.got) != 3 || file.got[0].Prompt == "" {
		t.Fatalf("the durable recorder got %+v; want all three with the prompt", file.got)
	}
	file.mu.Unlock()
	if tps := hub.TokensPerSecond(); tps < 5 || tps > 5.1 { // 303 tokens over a minute
		t.Fatalf("TokensPerSecond = %v; want 303/60", tps)
	}

	// A watcher that stops watching ends cleanly; one that outlives the host sees the stream end.
	cancel()
	if err := <-werr; err != nil {
		t.Fatalf("Watch after cancel = %v; want nil", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { werr <- Watch(ctx2, dir, func(usage.Event) {}) }()
	time.Sleep(100 * time.Millisecond)
	s.Close()
	select {
	case err := <-werr:
		if err != nil {
			t.Fatalf("Watch when the host stopped = %v; want nil (the stream ended)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after the host closed")
	}
	if err := Watch(ctx2, dir, nil); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("Watch with no host = %v; want ErrNoDaemon", err)
	}
}

// A subscriber that does not keep up is skipped, never waited for: Record must stay cheap on
// the request path, and a process with no hub answers /events with a clear refusal.
func TestEventStreamNeverBlocksTheRequestPath(t *testing.T) {
	hub := NewEvents(nil)
	ch, stop := hub.Subscribe()
	defer stop()
	done := make(chan struct{})
	go func() {
		for i := range cap(ch) + 50 {
			hub.Record(context.Background(), usage.Event{Status: i})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked on a subscriber that was not reading")
	}
	if n := len(ch); n != cap(ch) {
		t.Fatalf("buffered %d events; want the channel full (%d) and the rest dropped", n, cap(ch))
	}

	dir := shortDir(t)
	s, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := Watch(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Watch on a process without a hub = %v; want a 503", err)
	}
}

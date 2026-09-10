package bridge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlotsSnapshotAndOldWorkerFallback(t *testing.T) {
	for _, accepted := range []int{0, 48} {
		t.Run(map[int]string{0: "old-worker", 48: "current-worker"}[accepted], func(t *testing.T) {
			c, peers := bridgeServer(t)
			var slots atomic.Int32
			slots.Store(48)
			reload := make(chan struct{}, 1)
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			done := make(chan error, 1)
			go func() {
				done <- session(ctx, c, http.NotFoundHandler(), keySync{slots: func() int { return int(slots.Load()) }, reload: reload})
			}()
			conn := connectRaw(t, peers)
			if f := receiveFrame(t, conn); f.Type != "keys" || f.Slots != 48 {
				t.Fatalf("announce: %+v", f)
			}
			transmit(t, conn, frame{Type: "keys_ready", Slots: accepted})
			slots.Store(4)
			reload <- struct{}{}
			if f := receiveFrame(t, conn); f.Slots != 48 {
				t.Fatalf("reload changed connection capacity: %+v", f)
			}
			transmit(t, conn, frame{Type: "keys_ready", Slots: accepted})
			// Metadata alone occupies the negotiated slot, before any body allocation.
			transmit(t, conn, frame{Type: "request", ID: "one", Method: "POST", Path: "/v1/chat/completions"})
			transmit(t, conn, frame{Type: "request", ID: "two", Method: "POST", Path: "/v1/chat/completions"})
			if accepted == 0 {
				select {
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), "invalid bridge request") {
						t.Fatalf("fallback: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("old Worker allowed more than one slot")
				}
			} else {
				transmit(t, conn, frame{Type: "cancel", ID: "two"})
				if f := receiveFrame(t, conn); f.Type != "ready" || f.ID != "two" {
					t.Fatalf("second slot: %+v", f)
				}
				stop()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("session did not stop")
				}
			}
		})
	}
}

func TestConcurrentCancelDoesNotBlockOtherJobs(t *testing.T) {
	c, peers := bridgeServer(t)
	started, cancelled := make(chan string, 3), make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	startSession(t, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		started <- string(body)
		if string(body) == "first" {
			<-r.Context().Done()
			close(cancelled)
			<-release // Model a handler taking time to settle after cancellation.
			return
		}
		_, _ = w.Write(body)
	}), keySync{slots: func() int { return 2 }})
	conn := connectRaw(t, peers)
	if f := receiveFrame(t, conn); f.Slots != 2 {
		t.Fatalf("announce: %+v", f)
	}
	transmit(t, conn, frame{Type: "keys_ready", Slots: 2})
	for _, id := range []string{"first", "second"} {
		transmit(t, conn, frame{Type: "request", ID: id, Method: "POST", Path: "/v1/chat/completions"})
	}
	// Interleave request bodies as well as responses.
	for _, id := range []string{"second", "first"} {
		transmit(t, conn, frame{Type: "body", ID: id, Data: []byte(id)})
		transmit(t, conn, frame{Type: "end", ID: id})
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("handler did not start")
		}
	}
	transmit(t, conn, frame{Type: "cancel", ID: "first"})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("wrong cancellation context")
	}
	// This ack is essential: a dispatcher joining first synchronously would deadlock second.
	for {
		f := receiveFrame(t, conn)
		if f.ID != "second" {
			t.Fatalf("cancelled job settled too early: %+v", f)
		}
		if f.Type == "data" {
			if string(f.Data) != "second" {
				t.Fatal("crossed response bodies")
			}
			transmit(t, conn, frame{Type: "ack", ID: f.ID})
		}
		if f.Type == "end" {
			transmit(t, conn, frame{Type: "ready", ID: f.ID})
			break
		}
	}
	_, body := request(t, conn, "third", "friend", []byte("third"))
	if string(body) != "third" {
		t.Fatal("free slot could not serve the next request")
	}
}

func TestDisconnectJoinsEveryConcurrentHandler(t *testing.T) {
	c, peers := bridgeServer(t)
	started, stopped := make(chan struct{}, 3), make(chan struct{}, 3)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- session(ctx, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			<-r.Context().Done()
			stopped <- struct{}{}
		}), keySync{slots: func() int { return 3 }})
	}()
	conn := connectRaw(t, peers)
	_ = receiveFrame(t, conn)
	transmit(t, conn, frame{Type: "keys_ready", Slots: 3})
	for _, id := range []string{"a", "b", "c"} {
		transmit(t, conn, frame{Type: "request", ID: id, Method: "GET", Path: "/v1/models"})
		transmit(t, conn, frame{Type: "end", ID: id})
	}
	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("handler missing")
		}
	}
	conn.CloseNow()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not join handlers")
	}
	if len(stopped) != 3 {
		t.Fatalf("only %d handlers stopped", len(stopped))
	}
}

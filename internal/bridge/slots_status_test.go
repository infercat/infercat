package bridge

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNegotiatedSlotsStatusAndLog(t *testing.T) {
	for _, accepted := range []int{0, 48, 65} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			c, peers := bridgeServer(t)
			state := connectionState{}
			logs := make(chan string, 3)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- session(ctx, c, http.NotFoundHandler(), keySync{state: &state, slots: func() int { return 48 }, logf: func(f string, a ...any) { logs <- fmt.Sprintf(f, a...) }})
			}()
			conn := connectRaw(t, peers)
			if f := receiveFrame(t, conn); f.Slots != 48 {
				t.Fatal(f)
			}
			transmit(t, conn, frame{Type: "keys_ready", Slots: accepted})
			if accepted == 65 {
				select {
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), "invalid bridge slots") {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("invalid negotiation did not close")
				}
			} else {
				want := max(1, accepted)
				select {
				case line := <-logs:
					if line != fmt.Sprintf("bridge connected: negotiated slots=%d", want) {
						t.Fatal(line)
					}
				case <-time.After(time.Second):
					t.Fatal("no connected log")
				}
				state.mu.Lock()
				st := state.s
				state.mu.Unlock()
				if !st.Connected || st.Slots != want {
					t.Fatal(st)
				}
				// A key refresh repeats keys_ready, then an ordered request/cancel is a barrier.
				transmit(t, conn, frame{Type: "keys_ready", Slots: accepted})
				transmit(t, conn, frame{Type: "request", ID: "barrier", Method: "POST", Path: "/v1/chat/completions"})
				transmit(t, conn, frame{Type: "cancel", ID: "barrier"})
				if f := receiveFrame(t, conn); f.Type != "ready" {
					t.Fatal(f)
				}
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("disconnect did not complete")
				}
			}
			state.mu.Lock()
			st := state.s
			state.mu.Unlock()
			if st.Connected || st.Slots != 0 {
				t.Fatal("stale negotiated capacity", st)
			}
			select {
			case line := <-logs:
				t.Fatal("unexpected duplicate/invalid success log", line)
			default:
			}
		})
	}
}

package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/usage"
)

func TestInProcessCancelSettlesOnceOnReturn(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint(late), func(t *testing.T) {
			s := store(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			var settlements, releases atomic.Int32
			logs := make(chan string, 8)
			s.Log = func(f string, args ...any) { logs <- fmt.Sprintf(f, args...) }
			m := manager(t, s, func(ctx context.Context, key string, _ Step, acquired func() error) (StepResult, error) {
				if err := acquired(); err != nil {
					return StepResult{}, err
				}
				if key == "a" {
					close(entered)
					<-ctx.Done()
					if late {
						<-release
					}
					settlements.Add(1)
				}
				return StepResult{Output: json.RawMessage(`{}`), Dispatched: true, Settled: true, Usage: usage.Event{Meters: []usage.Meter{{Class: "tokens", Unit: "tokens", Measured: 7, Charged: 7}}}}, ctx.Err()
			}, nil)
			m.joinTimeout = 5 * time.Second // Cooperative settlement must tolerate scheduler/disk jitter.
			if late {
				m.joinTimeout = 20 * time.Millisecond // This case exercises the bounded wait.
			}
			if err := m.Register("chat", m.Consumer(func(_ context.Context, w *Work) (json.RawMessage, error) {
				result, err := w.Step(Step{Input: json.RawMessage(`{}`)})
				return result.Output, err
			}), Policy{JoinCancel: true, InProcess: true, Release: func(key, _ string) {
				if key == "a" {
					releases.Add(1)
				}
			}}); err != nil {
				t.Fatal(err)
			}
			a, err := m.Submit("a", "chat", "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			m.mu.Lock()
			joined := m.active[a.ID].joined
			m.mu.Unlock()
			if _, err = m.Cancel("a", a.ID); err != nil {
				t.Fatal(err)
			}
			await(t, func() bool { return state(s, a) == Cancelled })
			if late {
				s.mu.Lock()
				lease := s.reserved["a"][a.ID]
				held := lease != nil && lease.late
				s.mu.Unlock()
				if !held || settlements.Load() != 0 || releases.Load() != 0 {
					t.Fatal("premature release/settlement", held, settlements.Load(), releases.Load())
				}
				m.mu.Lock()
				active := m.active[a.ID] != nil
				available := m.availability("chat")
				m.mu.Unlock()
				if !active || available != nil {
					t.Fatal("late consumer blocked kind", active, available)
				}
				b, err := m.Submit("b", "chat", "", json.RawMessage(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				await(t, func() bool { return state(s, b) == Done })
				close(release)
			}
			await(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active[a.ID] == nil })
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("cancel join observation timed out")
			}
			got, _ := s.Get("a", a.ID)
			if got.State != Cancelled || len(got.Attempts) != 1 || !got.Attempts[0].Settled || got.Attempts[0].Usage.Meters[0].Charged != 7 || settlements.Load() != 1 || releases.Load() != 1 {
				t.Fatal(got, settlements.Load(), releases.Load())
			}
			s.mu.Lock()
			held := s.reserved["a"][a.ID] != nil
			s.mu.Unlock()
			if held {
				t.Fatal("returned lease retained")
			}
			if _, err = m.Cancel("a", a.ID); err != nil {
				t.Fatal(err)
			}
			if releases.Load() != 1 || settlements.Load() != 1 {
				t.Fatal("double settlement/release")
			}
			if late {
				select {
				case line := <-logs:
					if line != "in-process consumer late: run "+a.ID+" kind chat" {
						t.Fatal(line)
					}
				default:
					t.Fatal("late log missing")
				}
				if len(logs) != 0 {
					t.Fatal("duplicate late log")
				}
			} else if len(logs) > 0 {
				t.Fatal("cooperative consumer logged late")
			}
		})
	}
}

func TestInProcessTwoKeysExecuteConcurrently(t *testing.T) {
	s := store(t)
	entered := make(chan string, 2)
	release := make(chan struct{})
	defer close(release)
	m := manager(t, s, nil, nil)
	if err := m.Register("chat", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		entered <- w.Run.KeyID
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`{}`), ctx.Err()
	}), Policy{JoinCancel: true, InProcess: true}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		if _, err := m.Submit(key, "chat", "", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	seen := ""
	for range 2 {
		select {
		case key := <-entered:
			seen += key
		case <-time.After(time.Second):
			t.Fatal("chat kind serialized", seen)
		}
	}
	if !strings.Contains(seen, "a") || !strings.Contains(seen, "b") {
		t.Fatal(seen)
	}
}

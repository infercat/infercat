package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

func TestDestinationRouterTable(t *testing.T) {
	audio, _ := audioEngine(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{}`) })
	h := newHarness(t, Config{Transcribe: audio, Speech: audio, ModelsPinned: []string{"m1", "unloaded"}}, nil)
	for _, tc := range []struct {
		route, model, id string
		code             Code
	}{
		{string(chatEndpoint), "m1", "text", ""}, {string(embeddingsEndpoint), "m1", "text", ""},
		{string(modelsEndpoint), "", "text", ""}, {string(transcribeEndpoint), "m1", "transcribe", ""},
		{string(speechEndpoint), "m1", "speech", ""}, {string(chatEndpoint), "unloaded", "text", ""},
		{string(chatEndpoint), "private", "", CodeModelNotAllowed}, {string(speechEndpoint), "private", "", CodeModelNotAllowed},
		{"/v1/completions", "m1", "", CodeNotFound},
	} {
		t.Run(tc.route+tc.model, func(t *testing.T) {
			d, err := h.gw.router.Resolve(h.key, tc.route, tc.model)
			if tc.code != "" {
				if err == nil || err.Code != tc.code {
					t.Fatalf("got %v, want %s", err, tc.code)
				}
				return
			}
			if err != nil || d.ID != tc.id || d.Kind != "engine" || d.Origin != "local" {
				t.Fatalf("destination=%+v error=%v", d, err)
			}
		})
	}
	absent := newRouter(h.up, Config{})
	if _, err := absent.Resolve(h.key, string(speechEndpoint), "m1"); err == nil || err.Code != CodeNotFound {
		t.Fatalf("unconfigured route: %v", err)
	}
}

func TestDestinationQueuesAreIndependentFIFO(t *testing.T) {
	audio, _ := audioEngine(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{}`) })
	h := newHarness(t, Config{Transcribe: audio}, nil)
	for _, d := range h.gw.router.destinations {
		t.Run(d.ID, func(t *testing.T) {
			q := &d.Queue
			if _, err := q.acquire(context.Background(), time.Second, time.Second, nil); err != nil {
				t.Fatal(err)
			}
			got := make(chan int, 2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					_, err := q.acquire(context.Background(), 3*time.Second, time.Second, nil)
					if err != nil {
						got <- -1
					} else {
						got <- i
					}
				}(i)
				waitUntil(t, time.Second, "waiter queued", func() bool { _, n := q.counts(); return n == i+1 })
			}
			for _, other := range h.gw.router.destinations {
				if other != d {
					if a, b := other.Queue.counts(); a != 0 || b != 0 {
						t.Fatalf("other queue changed: %s %d/%d", other.ID, a, b)
					}
				}
			}
			for i := 0; i < 2; i++ {
				q.release()
				select {
				case n := <-got:
					if n != i {
						t.Fatalf("FIFO got %d want %d", n, i)
					}
				case <-time.After(4 * time.Second):
					t.Fatal("queue stalled")
				}
			}
			q.release()
		})
	}
}

func TestAudioInFlightDoesNotOccupyTextDestination(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	audio, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"text":"hello","duration":1}`)
	})
	h := newHarness(t, Config{Transcribe: audio}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 2 })
	raw, ct := multipartAudio(t, wave(1))
	done := make(chan resp, 1)
	go func() { done <- postAudio(t, h, string(transcribeEndpoint), ct, raw) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("audio never entered")
	}
	snapshots := h.gw.Destinations()
	if snapshots[0].InFlight != 0 || snapshots[1].InFlight != 1 || snapshots[1].Slots != 1 {
		t.Fatalf("snapshots: %+v", snapshots)
	}
	if a, b := h.gw.Queue(); a != 0 || b != 0 {
		t.Fatalf("text count %d/%d", a, b)
	}
	response := h.do("POST", string(chatEndpoint), "bearer", chatBody("m1", 1, ""))
	unblock()
	if response.status != 200 {
		t.Fatalf("chat blocked: %+v", response)
	}
	select {
	case r := <-done:
		if r.status != 200 {
			t.Fatalf("audio: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("audio stuck")
	}
	h.clean()
	events := h.rec.waitFor(t, 2)
	seen := map[string]bool{}
	for _, e := range events {
		if e.Status == 200 {
			seen[e.Destination] = true
		}
	}
	if !seen["text"] || !seen["transcribe"] {
		t.Fatalf("events: %+v", events)
	}
}

func TestDestinationModelContextAndMeFiltering(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.setInfo(func(i *upstream.Info) {
		i.Models = []string{"small", "large"}
		i.ModelContext = 4
		i.ModelDetails = map[string]upstream.ModelInfo{"small": {Context: 4}, "large": {Context: 100}}
	})
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"large"} })
	r := h.do("POST", string(chatEndpoint), "bearer", chatBody("large", 10, ""))
	if r.status != 200 {
		t.Fatalf("wrong model context: %+v", r)
	}
	h.up.mu.Lock()
	model := h.up.lastModel
	h.up.mu.Unlock()
	if model != "large" {
		t.Fatalf("counted model %q", model)
	}
	h.setKey(func(k *keys.Key) { k.Limits.MaxContext = 4 })
	h.expectErr(h.do("POST", string(chatEndpoint), "bearer", chatBody("large", 10, "")), CodeContextTooLong)
	response := h.do("GET", "/me", "bearer", "")
	var m meResponse
	if err := json.Unmarshal(response.body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Host.Upstream.ModelContext != 4 || !reflect.DeepEqual(m.Host.Models, []string{"large"}) {
		t.Fatalf("me: %+v", m.Host)
	}
	if !reflect.DeepEqual(h.gw.Destinations()[0].Models, []string{"small", "large"}) {
		t.Fatal("admin inventory was key filtered")
	}
}

func TestDestinationAttributedBeforePausedKeyRefusal(t *testing.T) {
	audio, _ := audioEngine(t, func(http.ResponseWriter, *http.Request) { t.Error("paused key reached engine") })
	h := newHarness(t, Config{Transcribe: audio}, nil)
	h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
	raw, ct := multipartAudio(t, wave(1))
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeKeyPaused)
	event := h.rec.waitFor(t, 1)[0]
	if event.Destination != "transcribe" || event.Code != string(CodeKeyPaused) {
		t.Fatalf("event %+v", event)
	}
}

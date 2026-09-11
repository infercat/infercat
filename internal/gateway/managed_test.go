package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedAuthAdmissionAndRefusedStartup(t *testing.T) {
	var calls atomic.Int32
	h := newHarness(t, Config{Managed: map[string]ManagedMember{"text": {Acquire: func(context.Context) (func(), error) { calls.Add(1); return nil, fmt.Errorf("cold failure") }, Offered: func() bool { return false }}}}, nil)
	h.expectErr(h.do("POST", string(chatEndpoint), "", `{"model":"m1","messages":[]}`), CodeInvalidKey)
	h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
	h.expectErr(h.post(string(chatEndpoint), `{"model":"m1","messages":[]}`), CodeKeyPaused)
	if calls.Load() != 0 {
		t.Fatal("unauthorized start")
	}
	h.setKey(func(k *keys.Key) { k.Status = keys.Active })
	h.expectErr(h.post(string(chatEndpoint), `{"model":"m1","messages":[]}`), CodeUpstreamDown)
	h.rec.waitFor(t, 2)
	c := h.gw.Counters(h.key.ID)
	if calls.Load() != 1 || c.RPMUsed != 0 || c.InFlight != 0 || c.TodayTokens != 0 {
		t.Fatal(c, calls.Load())
	}
}
func TestManagedEmbeddingHasOwnLeaseAndModelPins(t *testing.T) {
	embed := newFakeUpstream()
	defer embed.srv.Close()
	embed.setInfo(func(i *upstream.Info) { i.Models = []string{"embedding"} })
	released := make(chan struct{}, 1)
	var starts atomic.Int32
	h := newHarness(t, Config{Embed: embed, ModelsPinned: []string{"m1"}, Managed: map[string]ManagedMember{"embed": {Model: "embedding", Offered: func() bool { return true }, Acquire: func(context.Context) (func(), error) { starts.Add(1); return func() { released <- struct{}{} }, nil }}}}, nil)
	before := embed.requests.Load()
	r := h.post(string(embeddingsEndpoint), `{"model":"embedding","input":"hello"}`)
	if r.status != http.StatusOK {
		t.Fatalf("%+v", r)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("lease leaked")
	}
	ev := h.rec.waitFor(t, 1)[0]
	if ev.Destination != "embed" || embed.requests.Load() != before+1 || starts.Load() != 1 {
		t.Fatal(ev, starts.Load())
	}
}

type coldManagedAudio struct{}

func (coldManagedAudio) Info() upstream.Info           { return upstream.Info{Slots: 1} }
func (coldManagedAudio) Refresh(context.Context) error { return nil }
func (coldManagedAudio) AudioDo(context.Context, string, string, []byte) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected engine call")
}
func TestMeDormantOffersNeverAcquire(t *testing.T) {
	audio := coldManagedAudio{}
	var calls atomic.Int32
	var offered atomic.Bool
	offered.Store(true)
	h := newHarness(t, Config{Speech: audio, SpeechModel: "m1", Managed: map[string]ManagedMember{"speech": {Model: "m1", Offered: offered.Load, Acquire: func(context.Context) (func(), error) { calls.Add(1); return func() {}, nil }}}}, nil)
	for _, visible := range []bool{true, false} {
		offered.Store(visible)
		r := h.do("GET", "/me", "bearer", "")
		var me meResponse
		if r.status != 200 || json.Unmarshal(r.body, &me) != nil || calls.Load() != 0 || audio.Info().Health.OK {
			t.Fatal(r, calls.Load())
		}
		if (me.Host.Audio.Speech != nil) != visible {
			t.Fatal("dormant/failed offer", visible, string(r.body))
		}
	}
}

// The cold start exceeds the production body deadline, not a scaled approximation.
func TestManagedColdStartRenewsBodyDeadline(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprint("stalled=", stalled), func(t *testing.T) {
			t.Parallel()
			ready := make(chan struct{})
			var released atomic.Int32
			h := newHarness(t, Config{Managed: map[string]ManagedMember{"text": {
				Offered: func() bool { return true }, Acquire: func(ctx context.Context) (func(), error) {
					select {
					case <-time.After(40 * time.Second):
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					close(ready)
					return func() { released.Add(1) }, nil
				},
			}}}, nil)
			if stalled {
				h.gw.readTimeout = 200 * time.Millisecond
			}
			conn := rawConn(t, h.srv.URL)
			conn.SetDeadline(time.Now().Add(50 * time.Second))
			body := chatBody("m1", 1, "")
			fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", testSecret, len(body))
			<-ready
			start := time.Now()
			if !stalled {
				fmt.Fprint(conn, body)
			}
			res, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if stalled {
				if res.StatusCode != 400 || !strings.Contains(string(b), "invalid_request") || time.Since(start) > 2*time.Second {
					t.Fatalf("deadline not rearmed: %d %s", res.StatusCode, b)
				}
			} else if res.StatusCode != 200 {
				t.Fatalf("cold start consumed body window: %d %s", res.StatusCode, b)
			}
			h.rec.waitFor(t, 1)
			deadline := time.Now().Add(time.Second)
			for released.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if released.Load() != 1 || h.gw.Counters(h.key.ID).InFlight != 0 {
				t.Fatal("managed lease leaked")
			}
		})
	}
}

func TestManagedAudioLeaseThroughSettlement(t *testing.T) {
	for _, speech := range []bool{false, true} {
		t.Run(fmt.Sprint("speech=", speech), func(t *testing.T) {
			var active atomic.Int32
			var released atomic.Int32
			entered, finish := make(chan struct{}), make(chan struct{})
			engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if active.Load() != 1 {
					t.Error("no lease at engine")
				}
				close(entered)
				<-finish
				if speech {
					w.Header().Set("Content-Type", "audio/wav")
					w.Write(wave(1))
				} else {
					fmt.Fprint(w, `{"text":"fixture","duration":1}`)
				}
			})
			cfg := Config{Transcribe: engine, Speech: engine}
			id, path := "transcribe", string(transcribeEndpoint)
			body, ct := multipartAudio(t, wave(1))
			if speech {
				id, path = "speech", string(speechEndpoint)
				ct = "application/json"
				body = []byte(`{"model":"m1","input":"hello","voice":"alloy"}`)
			}
			cfg.Managed = map[string]ManagedMember{id: {Model: "m1", Offered: func() bool { return true }, Acquire: func(context.Context) (func(), error) {
				active.Add(1)
				return func() { active.Add(-1); released.Add(1) }, nil
			}}}
			h := newHarness(t, cfg, nil)
			done := make(chan resp, 1)
			go func() { done <- postAudio(t, h, path, ct, body) }()
			<-entered
			if active.Load() != 1 {
				t.Fatal("early release")
			}
			close(finish)
			if r := <-done; r.status != 200 {
				t.Fatal(r)
			}
			waitUntil(t, time.Second, "audio release", func() bool { return released.Load() == 1 })
			ev := h.rec.waitFor(t, 1)[0]
			if active.Load() != 0 || ev.Status != 200 || ev.SettledAt.IsZero() || h.gw.Counters(h.key.ID).InFlight != 0 {
				t.Fatal(ev)
			}
		})
	}
}

func TestManagedImageLeaseThroughOutputAndFinish(t *testing.T) {
	var active, starts atomic.Int32
	entered, finish := make(chan struct{}), make(chan struct{})
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if active.Load() != 1 {
			t.Error("no image lease")
		}
		close(entered)
		fmt.Fprint(w, `{"data":[{"b64_json":"`)
		w.(http.Flusher).Flush()
		<-finish
		fmt.Fprint(w, tinyImage()+`"}]}`)
	})
	released := make(chan struct{})
	h.gw.cfg.Managed = map[string]ManagedMember{"images": {Model: "image-model", Offered: func() bool { return true }, Acquire: func(context.Context) (func(), error) {
		active.Add(1)
		starts.Add(1)
		return func() {
			if h.gw.Counters(h.key.ID).TodayImages != 1 {
				t.Error("released before settlement")
			}
			files, _ := filepath.Glob(filepath.Join(h.gw.cfg.DataDir, "runs", h.key.ID, "images", "*"))
			if len(files) == 0 {
				t.Error("released before output stored")
			}
			active.Add(-1)
			close(released)
		}, nil
	}}}
	// Capability reads and admission's offer check do not start engines.
	h.get("/me")
	if _, e := h.gw.imageOffer(h.key); e != nil {
		t.Fatal(e)
	}
	if starts.Load() != 0 {
		t.Fatal("read/admission woke image")
	}
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["fixture"]}`))
	<-entered
	if active.Load() != 1 {
		t.Fatal("lease lost during output transfer")
	}
	close(finish)
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("lease leaked")
	}
	waitImageJob(t, h, rows[0].ID, runstate.Done)
	if active.Load() != 0 || starts.Load() != 1 {
		t.Fatal(active.Load(), starts.Load())
	}
}

func TestManagedFailureOverridesStaleHealthyProbe(t *testing.T) {
	var offered atomic.Bool
	member := ManagedMember{Model: "m1", Offered: offered.Load, Acquire: func(context.Context) (func(), error) {
		t.Error("read woke member")
		return nil, fmt.Errorf("unexpected")
	}}
	audio, _ := audioEngine(t, func(http.ResponseWriter, *http.Request) { t.Error("read dispatched audio") })
	embed := newFakeUpstream()
	defer embed.srv.Close()
	h := newHarness(t, Config{Speech: audio, Embed: embed, Managed: map[string]ManagedMember{"speech": member, "embed": member}}, nil)
	for _, want := range []bool{true, false} {
		offered.Store(want)
		var me meResponse
		r := h.get("/me")
		if r.status != 200 || json.Unmarshal(r.body, &me) != nil {
			t.Fatal(r)
		}
		if (me.Host.Audio.Speech != nil) != want || (me.Host.Embeddings != nil) != want {
			t.Fatal("stale health overrode managed state", string(r.body))
		}
	}
	image := imagesHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("read dispatched image") })
	image.gw.cfg.Managed = map[string]ManagedMember{"images": member}
	if _, e := image.gw.imageOffer(image.key); e == nil {
		t.Fatal("failed image advertised via stale health")
	}
	offered.Store(true)
	if _, e := image.gw.imageOffer(image.key); e != nil {
		t.Fatal(e)
	}
}

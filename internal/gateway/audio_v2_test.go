package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
)

func TestAudioBusyPreservesRetry(t *testing.T) {
	for _, route := range []endpoint{speechEndpoint, transcribeEndpoint} {
		for _, retry := range []string{"30", time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)} {
			engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", retry)
				w.WriteHeader(429)
				io.WriteString(w, `{"error":{"message":"busy"}}`)
			})
			h := newHarness(t, Config{Speech: engine, Transcribe: engine}, nil)
			body, ct := []byte(`{"model":"m1","input":"hi"}`), "application/json"
			if route == transcribeEndpoint {
				body, ct = multipartAudio(t, wave(1))
			}
			r := postAudio(t, h, string(route), ct, body)
			if r.status != 503 || r.errCode != CodeUpstreamDown || r.header.Get("Retry-After") == "" {
				t.Fatalf("%+v", r)
			}
			h.rec.waitFor(t, 1)
			if h.gw.Counters(h.key.ID).RPMUsed != 0 {
				t.Fatal("busy refusal counted RPM")
			}
			if retry == "30" && r.header.Get("Retry-After") != "30" {
				t.Fatal(r.header)
			}
			if route == speechEndpoint && h.rec.waitFor(t, 1)[0].Characters != 2 {
				t.Fatal("busy speech lost measured characters")
			}
		}
	}
}

func TestSpeechChargesServedOnly(t *testing.T) {
	for _, status := range []int{200, 400, 429, 500, 0} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if status == 0 {
					w.Header().Set("Content-Type", "audio/wav")
					w.Write([]byte("RIFF"))
					w.(http.Flusher).Flush()
					panic(http.ErrAbortHandler)
				}
				w.WriteHeader(status)
				io.WriteString(w, "audio")
			})
			h := newHarness(t, Config{Speech: engine}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.DailySpeechChars = 3 })
			req, _ := http.NewRequest("POST", h.srv.URL+string(speechEndpoint), strings.NewReader(`{"model":"m1","input":"你好🦊"}`))
			req.Header.Set("Authorization", "Bearer "+testSecret)
			r, err := h.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.ReadAll(r.Body)
			if status == 0 && readErr == nil {
				t.Fatal("gateway hid interrupted audio")
			}
			r.Body.Close()
			ev := h.rec.waitFor(t, 1)[0]
			want := 0
			if status == 200 {
				want = 3
			}
			if ev.Characters != 3 || len(ev.Meters) != 1 || ev.Meters[0].Measured != 3 || ev.Meters[0].Charged != float64(want) || h.gw.Counters(h.key.ID).TodaySpeechChars != want {
				t.Fatalf("status=%d event=%+v", status, ev)
			}
			if status == 200 {
				h.expectErr(h.post(string(speechEndpoint), `{"model":"m1","input":"a"}`), CodeSpeechBudgetExhausted)
			}
		})
	}
}

func TestAudioMultiBatchAbortReachesFriend(t *testing.T) {
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		wav := wave(4)
		w.Write(wav[:44])
		for i := 0; i < 3; i++ {
			w.Write(wav[44+i*32000 : 44+(i+1)*32000])
			w.(http.Flusher).Flush()
		}
		panic(http.ErrAbortHandler)
	})
	h := newHarness(t, Config{Speech: engine}, nil)
	req, _ := http.NewRequest("POST", h.srv.URL+string(speechEndpoint), strings.NewReader(`{"model":"m1","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	r, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if r.StatusCode != 200 || len(body) == 0 || err == nil {
		t.Fatalf("status=%d bytes=%d err=%v", r.StatusCode, len(body), err)
	}
	ev := h.rec.waitFor(t, 1)[0]
	if ev.Characters != 5 || len(ev.Meters) != 1 || ev.Meters[0].Measured != 5 || ev.Meters[0].Charged != 0 || h.gw.Counters(h.key.ID).TodaySpeechChars != 0 {
		t.Fatal("cut speech charged")
	}
}

func TestSpeechClientCutBeforeFirstByteKeepsMeasurementOnly(t *testing.T) {
	entered := make(chan struct{})
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	})
	h := newHarness(t, Config{Speech: engine}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+string(speechEndpoint), strings.NewReader(`{"input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := h.srv.Client().Do(req)
		if r != nil {
			r.Body.Close()
		}
	}()
	<-entered
	cancel()
	<-done
	ev := h.rec.waitFor(t, 1)[0]
	if ev.Characters != 5 || len(ev.Meters) != 1 || ev.Meters[0].Measured != 5 || ev.Meters[0].Charged != 0 || h.gw.Counters(h.key.ID).TodaySpeechChars != 0 {
		t.Fatalf("%+v", ev)
	}
}

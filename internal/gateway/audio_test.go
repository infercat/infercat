package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func wave(seconds int) []byte {
	b := make([]byte, 44+seconds*32000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}
func multipartAudio(t *testing.T, audio []byte) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("model", "m1")
	_ = w.WriteField("language", "en")
	_ = w.WriteField("prompt", "PRIVATE PROMPT")
	f, _ := w.CreateFormFile("file", "note.wav")
	_, _ = f.Write(audio)
	_ = w.Close()
	return b.Bytes(), w.FormDataContentType()
}
func audioEngine(t *testing.T, handler http.HandlerFunc) (upstream.AudioEngine, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer engine-key" {
			t.Error("engine bearer absent")
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"m1"}]}`)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	engine, err := upstream.OpenAudio(context.Background(), server.URL, "engine-key")
	if err != nil {
		t.Fatal(err)
	}
	return engine, server
}
func postAudio(t *testing.T, h *harness, path, ct string, b []byte) resp {
	t.Helper()
	req, _ := http.NewRequest("POST", h.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	req.Header.Set("Content-Type", ct)
	r, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := resp{status: r.StatusCode, header: r.Header, body: raw}
	var e errorBody
	if json.Unmarshal(raw, &e) == nil {
		out.errCode = e.Error.Code
		out.errType = e.Error.Type
		out.message = e.Error.Message
		if out.errCode != "" {
			seenCodesMu.Lock()
			seenCodes[out.errCode] = true
			seenCodesMu.Unlock()
		}
	}
	return out
}
func TestAudioDurationHeaders(t *testing.T) {
	if n, ok := containerSeconds(wave(10)); !ok || n != 10 {
		t.Fatalf("WAV %v %v", n, ok)
	}
	flac := make([]byte, 43)
	copy(flac, "fLaC")
	flac[7] = 34
	binary.BigEndian.PutUint64(flac[18:], uint64(48000)<<44|480000)
	if n, ok := containerSeconds(flac); !ok || n != 10 {
		t.Fatalf("FLAC %v %v", n, ok)
	}
	malformed := wave(10)
	binary.LittleEndian.PutUint32(malformed[28:], 1)
	for _, b := range [][]byte{[]byte("compressed"), wave(10)[:45], malformed} {
		if _, ok := containerSeconds(b); ok {
			t.Fatal("invalid/unknown header became measured")
		}
	}
}
func TestAudioReservationsAndUsage(t *testing.T) {
	for _, tc := range []struct {
		name             string
		body             []byte
		budget           int
		duration         float64
		want             Code
		charge, reserved float64
	}{
		{"measured", wave(10), 15, 10, "", 10, 10},
		{"five", wave(10), 5, 10, CodeAudioBudgetExhausted, 0, 10},
		{"unknown200", []byte("unmeasurable"), 200, 10, CodeAudioBudgetExhausted, 0, 300},
		{"unknown400", []byte("unmeasurable"), 400, 10, "", 10, 300},
		{"overrun", []byte("unmeasurable"), 400, 600, "", 600, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, ct := multipartAudio(t, tc.body)
			var calls atomic.Int32
			engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if err := r.ParseMultipartForm(25 << 20); err != nil {
					t.Fatal(err)
				}
				f, header, err := r.FormFile("file")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				got, _ := io.ReadAll(f)
				if !bytes.Equal(got, tc.body) || header.Filename != "note.wav" || r.FormValue("language") != "en" || r.FormValue("prompt") != "PRIVATE PROMPT" || r.FormValue("model") != "m1" || r.FormValue("response_format") != "verbose_json" {
					t.Error("multipart values or reconciliation format wrong")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"text": "PRIVATE TRANSCRIPT", "duration": tc.duration})
			})
			h := newHarness(t, Config{Transcribe: engine, LogPrompts: true}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.DailyAudioSeconds = tc.budget })
			r := postAudio(t, h, string(transcribeEndpoint), ct, raw)
			if tc.want != "" {
				h.expectErr(r, tc.want)
				if calls.Load() != 0 || r.header.Get("Retry-After") == "" {
					t.Fatal("budget refusal dispatched or omitted retry")
				}
			} else if r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			event := h.rec.waitFor(t, 1)[0]
			if event.Seconds != tc.charge || event.ReservedSeconds != tc.reserved || event.Prompt != "" || event.Completion != "" {
				t.Fatalf("event %+v", event)
			}
			if event.OverrunSeconds != max(0, tc.charge-tc.reserved) {
				t.Fatal("overrun not recorded truthfully")
			}
			if h.gw.Counters(h.key.ID).TodayAudioSeconds != tc.charge {
				t.Fatal("counter did not reconcile")
			}
			if tc.name == "measured" || tc.name == "overrun" {
				h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeAudioBudgetExhausted)
				if calls.Load() != 1 {
					t.Fatal("second call reached engine")
				}
			}
		})
	}
}
func TestAudioSpeechClientCutChargesAfterFirstByte(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["input"] != "你好🦊" || b["voice"] != "voice" {
			t.Error("JSON values changed")
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFF"))
		w.(http.Flusher).Flush()
		select {
		case <-gate:
		case <-r.Context().Done():
		}
	})
	h := newHarness(t, Config{Speech: engine, LogPrompts: true}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.DailySpeechChars = 3 })
	req, _ := http.NewRequest("POST", h.srv.URL+string(speechEndpoint), strings.NewReader(`{"model":"m1","input":"你好🦊","voice":"voice"}`))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	r, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 4)
	_, err = io.ReadFull(r.Body, first)
	if err != nil || string(first) != "RIFF" || r.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("first audio bytes %q %v", first, err)
	}
	r.Body.Close()
	ev := h.rec.waitFor(t, 1)[0]
	if ev.Characters != 3 || len(ev.Meters) != 1 || ev.Meters[0].Measured != 3 || ev.Meters[0].Charged != 3 || ev.Prompt != "" || ev.Completion != "" {
		t.Fatalf("partial speech settlement %+v", ev)
	}
	if h.gw.Counters(h.key.ID).TodaySpeechChars != 3 {
		t.Fatal("client cut speech not charged")
	}
}
func TestAudioConcurrentReservations(t *testing.T) {
	gate, entered := make(chan struct{}), make(chan struct{})
	defer close(gate)
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-gate:
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, `{"text":"hello","duration":10}`)
	})
	h := newHarness(t, Config{Transcribe: engine}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 2; k.Limits.DailyAudioSeconds = 400 })
	raw, ct := multipartAudio(t, []byte("unknown"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+string(transcribeEndpoint), bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	req.Header.Set("Content-Type", ct)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := h.srv.Client().Do(req)
		if r != nil {
			r.Body.Close()
		}
	}()
	<-entered
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeAudioBudgetExhausted)
	cancel()
	<-done
}
func TestAudioRefusalsAndRestart(t *testing.T) {
	var calls atomic.Int32
	engine, server := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":"This model maximum context length rejected PRIVATE AUDIO TEXT"}`, 400)
	})
	h := newHarness(t, Config{Transcribe: engine, MaxTranscriptionSeconds: 5}, nil)
	raw, ct := multipartAudio(t, wave(10))
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeInvalidRequest)
	h.gw.cfg.MaxTranscriptionSeconds = 300
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"other"} })
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeModelNotAllowed)
	if calls.Load() != 0 {
		t.Fatal("refusal reached engine")
	}
	h.setKey(func(k *keys.Key) { k.Limits.Models = nil })
	server.Close()
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeUpstreamDown)
	if h.gw.Counters(h.key.ID).TodayAudioSeconds != 0 {
		t.Fatal("dial failure was charged")
	}
	dir := t.TempDir()
	event := usage.Event{TS: time.Now(), KeyID: h.key.ID, Endpoint: string(transcribeEndpoint), Seconds: 10}
	b, _ := json.Marshal(event)
	_ = os.WriteFile(filepath.Join(dir, usage.FileName), append(b, '\n'), 0600)
	restored := New(Config{DataDir: dir}, h.up, h.store, nil, nil)
	if restored.Counters(h.key.ID).TodayAudioSeconds != 10 {
		t.Fatal("audio budget did not survive restart")
	}
}

func TestAudioFailureChargingAndHistoryRefusal(t *testing.T) {
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":"This model maximum context length rejected PRIVATE AUDIO TEXT"}`)
	})
	h := newHarness(t, Config{Transcribe: engine}, nil)
	raw, ct := multipartAudio(t, wave(10))
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeInvalidRequest)
	event := h.rec.waitFor(t, 1)[0]
	if event.Seconds != 10 || event.SecondsEstimated {
		t.Fatalf("after-dispatch failure %+v", event)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, usage.FileName), []byte("broken event\n"), 0600)
	var logs logBuf
	base := newHarness(t, Config{}, nil)
	_ = New(Config{DataDir: dir, Transcribe: engine}, base.up, base.store, base.rec, logs.logf)
	if !strings.Contains(logs.String(), "audio refused: usage history has 1 malformed rows") {
		t.Fatal("missing startup refusal", logs.String())
	}
	bad := newHarness(t, Config{DataDir: dir, Transcribe: engine}, nil)
	h.expectErr(postAudio(t, bad, string(transcribeEndpoint), ct, raw), CodeUpstreamDown)
}
func TestAudioCapabilityAndUnconfiguredRoutes(t *testing.T) {
	engine, server := audioEngine(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	h := newHarness(t, Config{Transcribe: engine}, nil)
	var me meResponse
	_ = json.Unmarshal(h.get("/me").body, &me)
	if me.Host.Audio.Transcriptions == nil || *me.Host.Audio.Transcriptions != "m1" || me.Host.Audio.Speech != nil {
		t.Fatal("configured route capabilities wrong")
	}
	h.expectErr(h.post(string(speechEndpoint), `{}`), CodeNotFound)
	server.Close()
	_ = engine.Refresh(context.Background())
	_ = json.Unmarshal(h.get("/me").body, &me)
	if me.Host.Audio.Transcriptions != nil {
		t.Fatal("unhealthy engine advertised")
	}
}

func TestAudioBodyCapAndQueueRelease(t *testing.T) {
	var calls atomic.Int32
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	h := newHarness(t, Config{Transcribe: engine}, nil)
	req := httptest.NewRequest("POST", string(transcribeEndpoint), nil)
	req.ContentLength = (25 << 20) + 1
	req.Header.Set("Authorization", "Bearer "+testSecret)
	response := httptest.NewRecorder()
	h.gw.Handler().ServeHTTP(response, req)
	if response.Code != 413 || calls.Load() != 0 {
		t.Fatal("25 MiB body cap failed before dispatch")
	}
	if _, err := h.gw.router.route(string(transcribeEndpoint)).Queue.acquire(context.Background(), time.Second, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	defer h.gw.router.route(string(transcribeEndpoint)).Queue.release()
	h.gw.queueTimeout = 5 * time.Millisecond
	raw, ct := multipartAudio(t, wave(10))
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeQueueTimeout)
	counts := h.gw.Counters(h.key.ID)
	if counts.TodayAudioSeconds != 0 || counts.InFlight != 0 || counts.RPMUsed != 1 || calls.Load() != 0 {
		t.Fatalf("queue-lost settlement %+v calls=%d", counts, calls.Load())
	}
}

func TestAudioBudgetRollsAtUTCMidnight(t *testing.T) {
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"text":"hello","duration":10}`)
	})
	h := newHarness(t, Config{Transcribe: engine}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.DailyAudioSeconds = 15 })
	now := time.Date(2026, 9, 9, 23, 59, 55, 0, time.UTC)
	h.gw.lim.now = func() time.Time { return now }
	raw, ct := multipartAudio(t, wave(10))
	if r := postAudio(t, h, string(transcribeEndpoint), ct, raw); r.status != 200 {
		t.Fatal(r.status)
	}
	r := postAudio(t, h, string(transcribeEndpoint), ct, raw)
	h.expectErr(r, CodeAudioBudgetExhausted)
	if r.header.Get("Retry-After") != "5" {
		t.Fatalf("UTC retry: %s", r.header.Get("Retry-After"))
	}
	if ev := h.rec.waitFor(t, 2)[0]; !ev.SettledAt.Equal(now) {
		t.Fatal("event and charge use different UTC days")
	}
	now = now.Add(6 * time.Second)
	if r := postAudio(t, h, string(transcribeEndpoint), ct, raw); r.status != 200 {
		t.Fatal("new UTC day retained old charge")
	}
}

func audioFields(t *testing.T, fields map[string]string) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for key, value := range fields {
		_ = w.WriteField(key, value)
	}
	f, _ := w.CreateFormFile("file", "phone.mp3")
	_, _ = f.Write([]byte("unmeasurable audio"))
	_ = w.Close()
	return b.Bytes(), w.FormDataContentType()
}

func TestAudioJSONReconcilesWithoutClientOptIn(t *testing.T) {
	for _, format := range []string{"", "json", "verbose_json", "text", "srt", "vtt"} {
		t.Run("format="+format, func(t *testing.T) {
			wantFormat, response, charge := format, "plain transcript", float64(300)
			if format == "" || format == "json" || format == "verbose_json" {
				wantFormat, response, charge = "verbose_json", `{"text":"hello","duration":2.5,"segments":[]}`, 2.5
			}
			engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				if r.FormValue("response_format") != wantFormat || r.FormValue("model") != "m1" {
					t.Error("wrong normalized fields", r.MultipartForm.Value)
				}
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Length", strconv.Itoa(len(response)))
				_, _ = io.WriteString(w, response)
			})
			h := newHarness(t, Config{Transcribe: engine}, nil)
			fields := map[string]string{}
			if format != "" {
				fields["response_format"] = format
			}
			raw, ct := audioFields(t, fields)
			r := postAudio(t, h, string(transcribeEndpoint), ct, raw)
			want := response
			if format == "" || format == "json" {
				want = `{"text":"hello"}`
			}
			if r.status != 200 || string(r.body) != want || r.header.Get("Content-Length") != strconv.Itoa(len(want)) {
				t.Fatalf("response %d %q %v", r.status, r.body, r.header)
			}
			ev := h.rec.waitFor(t, 1)[0]
			if ev.Seconds != charge || ev.ReservedSeconds != 300 || ev.SecondsEstimated != (charge == 300) {
				t.Fatalf("settlement %+v", ev)
			}
			if format == "" {
				// Thirteen short SDK-default clips cost their real duration, not a 3600 s day.
				for range 12 {
					if r := postAudio(t, h, string(transcribeEndpoint), ct, raw); r.status != 200 {
						t.Fatalf("short clip refused: %d", r.status)
					}
				}
				_ = h.rec.waitFor(t, 13)
				if n := h.gw.Counters(h.key.ID).TodayAudioSeconds; n != 32.5 {
					t.Fatalf("short clips charged %v", n)
				}
			}
		})
	}
}

func TestAudioHostModelSelection(t *testing.T) {
	for _, kind := range []endpoint{transcribeEndpoint, speechEndpoint} {
		for _, tc := range []struct{ name, configured, requested, want string }{
			{"first", "", "", "m1"}, {"configured", "host-model", "", "host-model"},
			{"unknown", "host-model", "chat-model", "host-model"}, {"listed", "host-model", "m1", "m1"},
		} {
			t.Run(string(kind)+tc.name, func(t *testing.T) {
				var calls atomic.Int32
				engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var model string
					if kind == speechEndpoint {
						var b map[string]any
						_ = json.NewDecoder(r.Body).Decode(&b)
						model, _ = b["model"].(string)
					} else {
						_ = r.ParseMultipartForm(1 << 20)
						model = r.FormValue("model")
					}
					if model != tc.want {
						t.Errorf("model %q, want %q", model, tc.want)
					}
					_, _ = io.WriteString(w, `{"text":"hello","duration":1}`)
				})
				h := newHarness(t, Config{Transcribe: engine, Speech: engine, TranscribeModel: tc.configured, SpeechModel: tc.configured}, nil)
				raw, ct := audioFields(t, map[string]string{"model": tc.requested})
				if kind == speechEndpoint {
					raw, _ = json.Marshal(map[string]string{"model": tc.requested, "input": "hello"})
					ct = "application/json"
				}
				if r := postAudio(t, h, string(kind), ct, raw); r.status != 200 {
					t.Fatalf("%d %s", r.status, r.body)
				}
				h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"not-shared"} })
				h.expectErr(postAudio(t, h, string(kind), ct, raw), CodeModelNotAllowed)
				if calls.Load() != 1 {
					t.Fatal("unshared selected model dispatched")
				}
				var me meResponse
				_ = json.Unmarshal(h.get("/me").body, &me)
				if me.Host.Audio.Transcriptions != nil || me.Host.Audio.Speech != nil {
					t.Fatal("unshared capability advertised")
				}
			})
		}
	}
}

func TestAudioHealthOnlyRequiresHostModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = io.WriteString(w, "ok")
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	engine, err := upstream.OpenAudio(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if audioModel(engine, "") != nil {
		t.Fatal("health-only engine advertised without model")
	}
	if model := audioModel(engine, "chosen"); model == nil || *model != "chosen" {
		t.Fatal("host model was lost")
	}
	h := newHarness(t, Config{Transcribe: engine}, nil)
	raw, ct := audioFields(t, nil)
	h.expectErr(postAudio(t, h, string(transcribeEndpoint), ct, raw), CodeUpstreamDown)
	if n := h.gw.Counters(h.key.ID).TodayAudioSeconds; n != 0 {
		t.Fatal("model-less request charged")
	}
}

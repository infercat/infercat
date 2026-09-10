package speech

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	r, err := http.Post(url+"/v1/audio/speech", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}

func TestFormatsAndVoice(t *testing.T) {
	for _, tc := range []struct {
		name, body, mime string
		voice, header    int
		speed            float32
	}{
		{"default", `{"input":"Hello"}`, "audio/wav", 0, 44, 1},
		{"Chinese", `{"input":"中文"}`, "audio/wav", 3, 44, 1},
		{"wav", `{"model":"kokoro","input":"Hello","response_format":"wav","voice":"af_sol","speed":0.5}`, "audio/wav", 1, 44, .5},
		{"pcm", `{"input":"Hello","response_format":"pcm","voice":"bf_vale","speed":2}`, "audio/pcm", 2, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(New(func(_ context.Context, _ string, voice int, speed float32, emit func([]float32) error) error {
				if voice != tc.voice || speed != tc.speed {
					t.Errorf("voice=%d speed=%f", voice, speed)
				}
				return emit([]float32{-2, 0, 1})
			}))
			defer s.Close()
			r := post(t, s.URL, tc.body)
			b, err := io.ReadAll(r.Body)
			if err != nil || r.StatusCode != 200 || r.Header.Get("Content-Type") != tc.mime || len(b) != tc.header+6 {
				t.Fatalf("response %d %s %x %v", r.StatusCode, r.Header, b, err)
			}
			if tc.header != 0 && (string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || binary.LittleEndian.Uint32(b[24:]) != SampleRate) {
				t.Fatalf("header %x", b[:44])
			}
			pcm := b[tc.header:]
			if int16(binary.LittleEndian.Uint16(pcm)) != -32767 || binary.LittleEndian.Uint16(pcm[4:]) != 32767 {
				t.Fatalf("PCM %x", pcm)
			}
		})
	}
}

func TestRefusalsNeverGenerate(t *testing.T) {
	s := httptest.NewServer(New(func(context.Context, string, int, float32, func([]float32) error) error {
		t.Error("unexpected generation")
		return nil
	}))
	defer s.Close()
	for _, body := range []string{
		`null`, `{}`, `{"input":" "}`, `{"input":"x\u0000y"}`, `{"input":"x","model":"other"}`,
		`{"input":"x","response_format":"mp3"}`, `{"input":"x","voice":"missing"}`,
		`{"input":"x","speed":0}`, `{"input":"x","speed":0.1}`, `{"input":"x","speed":3}`, `{"input":"x","extra":true}`,
		`{"input":"x"} {}`, `{"input":"` + strings.Repeat("x", 4097) + `"}`, strings.Repeat(" ", 33<<10),
	} {
		t.Run(body[:min(35, len(body))], func(t *testing.T) {
			r := post(t, s.URL, body)
			if r.StatusCode != 400 {
				t.Fatalf("status=%d", r.StatusCode)
			}
		})
	}
}

func TestFirstChunkPrecedesCompletionAndBusyRefuses(t *testing.T) {
	finish := make(chan struct{})
	batchDone := make(chan struct{})
	s := httptest.NewServer(New(func(ctx context.Context, _ string, _ int, _ float32, emit func([]float32) error) error {
		if err := emit([]float32{.5}); err != nil {
			return err
		}
		close(batchDone)
		select {
		case <-finish:
			return emit([]float32{-.5})
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	defer s.Close()
	defer close(finish)
	r := post(t, s.URL, `{"input":"First. Second."}`)
	first := make([]byte, 46)
	if _, err := io.ReadFull(r.Body, first); err != nil {
		t.Fatal(err)
	}
	<-batchDone
	busy := post(t, s.URL, `{"input":"Busy"}`)
	if busy.StatusCode != 429 || busy.Header.Get("Retry-After") != "1" {
		t.Fatal(busy.Status)
	}
	if r.Header.Get("Content-Length") != "" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(r.Header)
	}
}

func TestPinnedVoiceOrder(t *testing.T) {
	// model.onnx SHA acc4adc1… at csukuangfj/kokoro-multi-lang-v1_1
	// 914313412b607d95400bcd12446233fbd1248801: n_speakers=103,
	// speaker_names exported in voices.bin ID order by generate_voices_bin.py.
	if len(Voices) != 103 || Voices[0] != "af_maple" || Voices[3] != "zf_001" || Voices[102] != "zm_100" || fmt.Sprintf("%x", sha256.Sum256([]byte(voiceNames))) != "3dddf1709b1c96a24c32480a5de4bd17726748eff256635fcd9ee94e30cf1217" {
		t.Fatal("pinned voice order changed")
	}
	for id, name := range Voices {
		s := httptest.NewServer(New(func(_ context.Context, _ string, voice int, _ float32, emit func([]float32) error) error {
			if voice != id {
				t.Errorf("%s: got %d want %d", name, voice, id)
			}
			return emit([]float32{0})
		}))
		r := post(t, s.URL, fmt.Sprintf(`{"input":"hi","voice":%q}`, name))
		if r.StatusCode != 200 {
			t.Fatal(r.Status)
		}
		r.Body.Close()
		s.Close()
	}
}

func TestWeightedAdmissionBeforeNativeCall(t *testing.T) {
	for _, tc := range []struct {
		text     string
		speed    float32
		accepted bool
	}{
		{strings.Repeat("a", 600), 1, true}, {strings.Repeat("a", 601), 1, false},
		{strings.Repeat("中", 150), 1, true}, {strings.Repeat("中", 151), 1, false},
		{strings.Repeat("中", 75), .5, true}, {strings.Repeat("中", 76), .5, false},
		{strings.Repeat("a", 780), 2, true}, {strings.Repeat("a", 781), 2, false},
		{strings.Repeat("a", 450) + strings.Repeat("中", 113), 1, false},
		{strings.Repeat("1", 113), 1, false},
	} {
		calls := 0
		s := httptest.NewServer(New(func(context.Context, string, int, float32, func([]float32) error) error { calls++; return nil }))
		r := post(t, s.URL, fmt.Sprintf(`{"input":%q,"speed":%v}`, tc.text, tc.speed))
		r.Body.Close()
		s.Close()
		if (calls == 1) != tc.accepted || !tc.accepted && r.StatusCode != 400 {
			t.Fatalf("weight=%d speed=%v status=%d calls=%d", inputWeight(tc.text), tc.speed, r.StatusCode, calls)
		}
	}
}

func TestJSONRefusalsNameCause(t *testing.T) {
	s := httptest.NewServer(New(nil))
	defer s.Close()
	for body, detail := range map[string]string{`{"input":"hi","instructions":"be calm"}`: `unknown field`, `{"input":"hi","speed":1e40}`: `cannot unmarshal`, `{"input":"hi"} {}`: `trailing content`, strings.Repeat(" ", 33<<10): `body too large`} {
		r := post(t, s.URL, body)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 400 || !strings.Contains(string(b), detail) {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
}

func TestMultiBatchSafetyCapAbortsAfterAudio(t *testing.T) {
	s := httptest.NewServer(New(func(_ context.Context, _ string, _ int, _ float32, emit func([]float32) error) error {
		chunk := make([]float32, 30*SampleRate)
		for range 5 {
			if err := emit(chunk); err != nil {
				return err
			}
		}
		return nil
	}))
	defer s.Close()
	r := post(t, s.URL, `{"input":"hi"}`)
	b, err := io.ReadAll(r.Body)
	if r.StatusCode != 200 || len(b) < 44+90*SampleRate*2 || err == nil {
		t.Fatalf("status=%d bytes=%d error=%v", r.StatusCode, len(b), err)
	}
}

func TestCancellationReleasesSlot(t *testing.T) {
	stopped := make(chan struct{})
	var calls atomic.Int32
	s := httptest.NewServer(New(func(ctx context.Context, _ string, _ int, _ float32, emit func([]float32) error) error {
		if calls.Add(1) > 1 {
			return emit([]float32{0})
		}
		if err := emit([]float32{0}); err != nil {
			return err
		}
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}))
	defer s.Close()
	r := post(t, s.URL, `{"input":"Cancel"}`)
	r.Body.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("generation not cancelled")
	}
	deadline := time.Now().Add(time.Second)
	for {
		r = post(t, s.URL, `{"input":"Next"}`)
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot not released")
		}
	}
}

func TestFailedGenerationAndTruncatedStream(t *testing.T) {
	for _, started := range []bool{false, true} {
		s := httptest.NewServer(New(func(_ context.Context, _ string, _ int, _ float32, emit func([]float32) error) error {
			if started {
				if err := emit([]float32{0}); err != nil {
					return err
				}
			}
			return errors.New("native error")
		}))
		r := post(t, s.URL, `{"input":"fail"}`)
		_, err := io.ReadAll(r.Body)
		if started && err == nil || !started && r.StatusCode != 500 {
			t.Fatalf("started=%v status=%d err=%v", started, r.StatusCode, err)
		}
		s.Close()
	}
}

func TestOutputBound(t *testing.T) {
	s := httptest.NewServer(New(func(_ context.Context, _ string, _ int, _ float32, emit func([]float32) error) error {
		return emit(make([]float32, 120*SampleRate+1))
	}))
	defer s.Close()
	if r := post(t, s.URL, `{"input":"long"}`); r.StatusCode != 500 {
		t.Fatal(r.Status)
	}
}

func TestDiscovery(t *testing.T) {
	s := httptest.NewServer(New(nil))
	defer s.Close()
	for _, path := range []string{"/health", "/v1/models"} {
		r, err := http.Get(s.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != 200 {
			t.Fatal(r.Status, err)
		}
		if path == "/health" && string(b) != "ready" || path == "/v1/models" && !strings.Contains(string(b), `"id":"kokoro"`) {
			t.Fatalf("%s", b)
		}
	}
}

func TestSimpleCrossOriginPostRefused(t *testing.T) {
	s := httptest.NewServer(New(func(context.Context, string, int, float32, func([]float32) error) error {
		t.Error("unexpected generation")
		return nil
	}))
	defer s.Close()
	r, err := http.Post(s.URL+"/v1/audio/speech", "text/plain", strings.NewReader(`{"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 415 || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal(r.Status, r.Header)
	}
}

func TestBusyReportsObservedBatchDuration(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    string
	}{{0, "3"}, {100 * time.Millisecond, "1"}, {2100 * time.Millisecond, "3"}, {40 * time.Second, "30"}} {
		s := &Server{batchStart: time.Now(), observed: tc.elapsed}
		r := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{"input":"hello"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.speech(w, r)
		if w.Code != 429 || w.Header().Get("Retry-After") != tc.want {
			t.Fatalf("%v: %d %v", tc.elapsed, w.Code, w.Header())
		}
	}
}

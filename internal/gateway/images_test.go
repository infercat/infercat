package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
)

func tinyImage() string {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func imagesHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer engine-key" {
			t.Error("missing engine key")
		}
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	engine, e := upstream.OpenImages(context.Background(), server.URL, "engine-key")
	if e != nil {
		t.Fatal(e)
	}
	h := newHarness(t, Config{Images: engine, DataDir: t.TempDir()}, nil)
	store, e := runstate.NewStore(h.gw.cfg.DataDir)
	if e != nil {
		t.Fatal(e)
	}
	m, e := runstate.New(store, h.gw.ExecuteStep, nil)
	if e != nil {
		t.Fatal(e)
	}
	h.gw.SetRuns(m)
	t.Cleanup(m.Close)
	return h
}
func submittedImages(t *testing.T, r resp) []runstate.Run {
	t.Helper()
	if r.status != 202 {
		t.Fatalf("submit %d %s", r.status, r.body)
	}
	var v struct {
		Jobs []runstate.Run `json:"jobs"`
	}
	if e := json.Unmarshal(r.body, &v); e != nil {
		t.Fatal(e)
	}
	return v.Jobs
}
func waitImageJob(t *testing.T, h *harness, id string, want runstate.State) runstate.Run {
	t.Helper()
	var row runstate.Run
	waitUntil(t, 3*time.Second, "image state", func() bool { row, _ = h.gw.runs.Store.Get(h.key.ID, id); return row.State == want })
	return row
}
func TestImageRoutesBatchCancellationOutputsAndMeter(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["n"] != float64(1) || in["model"] != "image-model" || in["response_format"] != "b64_json" {
			t.Error(in)
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = 2; k.Limits.MaxQueuedImages = 1 })
	a := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["first"],"conversation":"chat"}`))[0]
	<-entered
	b := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["second"]}`))[0]
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["third","fourth"]}`), CodeImageQueueFull)
	if h.do("DELETE", "/v1/runs/"+a.ID, "bearer", "").status != 200 {
		t.Fatal("cancel")
	}
	if r := waitImageJob(t, h, a.ID, runstate.Running); !r.CancelRequested {
		t.Fatal("cancel not retained")
	}
	_ = h.do("DELETE", "/v1/runs/"+b.ID, "bearer", "")
	waitImageJob(t, h, b.ID, runstate.Cancelled)
	release <- struct{}{}
	r := waitImageJob(t, h, a.ID, runstate.Done)
	if len(r.Attempts) != 1 || r.Attempts[0].Usage.Meters[0].Charged != 1 {
		t.Fatal(r)
	}
	if got := h.gw.Counters(h.key.ID); got.TodayImages != 1 || got.TodayTokens != 0 || got.InFlight != 0 {
		t.Fatal(got)
	}
	var list struct {
		Jobs []imageJob `json:"jobs"`
	}
	_ = json.Unmarshal(h.get("/v1/images/jobs").body, &list)
	if len(list.Jobs) != 2 {
		t.Fatal("batch cap mutated list")
	}
	out := h.get("/v1/images/outputs/" + a.ID + "?download=1")
	if out.status != 200 || out.header.Get("Content-Type") != "image/png" || !strings.HasPrefix(out.header.Get("Content-Disposition"), "attachment;") {
		t.Fatal(out)
	}
	var me map[string]any
	_ = json.Unmarshal(h.get("/me").body, &me)
	if me["host"].(map[string]any)["images"].(map[string]any)["model"] != "image-model" {
		t.Fatal(me)
	}
	_ = h.do("DELETE", "/v1/images/outputs/"+a.ID, "bearer", "")
	h.expectErr(h.get("/v1/images/outputs/"+a.ID), CodeNotFound)
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = 1 })
	h.expectErr(h.post("/v1/images/generations", `{"prompt":"budget refused"}`), CodeImageBudgetExhausted)
}
func TestImageSettlementFailureAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		body              string
		charged, measured float64
	}{
		{"complete", 200, `{"data":[{"b64_json":"` + tinyImage() + `"}]}`, 1, 1},
		{"empty", 200, `{"data":[]}`, 0, 0},
		{"null", 200, `{"data":null}`, 0, 0},
		{"absent", 200, `{}`, 0, 0},
		{"filter", 200, `{"error":{"message":"content filter"}}`, 0, 0},
		{"empty-body", 200, ``, 0, 0},
		{"empty-base64", 200, `{"data":[{"b64_json":""}]}`, 0, 0},
		{"definitive", 500, `{"error":{"message":"could not make it"}}`, 0, 0},
		{"invalid", 200, `{"data":[{"b64_json":"bad"}]}`, 1, 0},
		{"url-only", 200, `{"data":[{"url":"https://invalid.example/PRIVATE"}]}`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			r := h.post("/v1/images/generations", `{"prompt":"make one"}`)
			if tc.measured == 1 && r.status != 200 {
				t.Fatal(r)
			}
			if bytes.Contains(r.body, []byte("PRIVATE")) {
				t.Fatal("output URL exposed")
			}
			rows, _ := h.gw.runs.Store.List(h.key.ID)
			if len(rows) != 1 || calls.Load() != 1 {
				t.Fatal(rows, calls.Load())
			}
			job, _ := h.gw.runs.Store.Get(h.key.ID, rows[0].ID)
			m := job.Attempts[0].Usage.Meters
			if len(m) != 1 || m[0].Class != "images" || m[0].Measured != tc.measured || m[0].Charged != tc.charged || h.gw.Counters(h.key.ID).TodayImages != int(tc.charged) {
				t.Fatal(m)
			}
		})
	}
}
func TestImageJobRefusalsBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":[" "]}`), CodeInvalidRequest)
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["<sd_cpp_extra_args>{}</sd_cpp_extra_args>"]}`), CodeInvalidRequest)
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m1"} })
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["not shared"]}`), CodeNotFound)
	if calls.Load() != 0 {
		t.Fatal("refused prompt dispatched")
	}
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	if len(rows) != 0 {
		t.Fatal("refusal created job")
	}
}

func TestImageDoesNotConsumeChatConcurrency(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1 })
	a := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["first"]}`))[0]
	<-entered
	if h.gw.Counters(h.key.ID).InFlight != 0 {
		t.Error("image spent text concurrency")
	}
	r := h.post("/v1/chat/completions", `{"model":"m1","messages":[{"role":"user","content":"hello"}],"max_tokens":20}`)
	if r.status != 200 {
		t.Errorf("chat blocked: %d %s", r.status, r.body)
	}
	close(release)
	waitImageJob(t, h, a.ID, runstate.Done)
	if h.gw.Counters(h.key.ID).InFlight != 0 {
		t.Fatal("capacity leaked")
	}
}
func TestImageMeterRestartsWithoutReset(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	if r := h.post("/v1/images/generations", `{"prompt":"one"}`); r.status != 200 {
		t.Fatal(r)
	}
	waitUntil(t, time.Second, "usage recorded", func() bool { g := New(h.gw.cfg, h.up, h.store, nil, nil); return g.Counters(h.key.ID).TodayImages == 1 })
}

func TestImageBatchCorrelationDoesNotReplayOrDeduplicate(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	body := `{"prompts":["one","two"],"conversation":"chat-1","client_request_id":"local-1"}`
	a := submittedImages(t, h.post("/v1/images/jobs", body))
	b := submittedImages(t, h.post("/v1/images/jobs", body))
	if len(a) != 2 || len(b) != 2 || a[0].Batch.ID == b[0].Batch.ID {
		t.Fatal("correlation became idempotency")
	}
	for _, r := range append(a, b...) {
		var in runstate.ImageInput
		_ = json.Unmarshal(r.Input, &in)
		if in.ClientRequestID != "local-1" || in.Conversation != "chat-1" {
			t.Fatal(in)
		}
		waitImageJob(t, h, r.ID, runstate.Done)
	}
	var list struct {
		Jobs []imageJob `json:"jobs"`
	}
	_ = json.Unmarshal(h.get("/v1/images/jobs").body, &list)
	if len(list.Jobs) != 4 {
		t.Fatal(list)
	}
	for _, r := range list.Jobs {
		if r.Position != 0 {
			t.Fatal("terminal rank", r.Position)
		}
	}
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["one"],"client_request_id":"`+strings.Repeat("x", 129)+`"}`), CodeInvalidRequest)
}

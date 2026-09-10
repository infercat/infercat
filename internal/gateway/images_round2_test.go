package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func reviewedImageHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	engine, e := upstream.OpenImages(context.Background(), server.URL, "")
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
func TestOwnedImageBodyMakesBusyProbeUnknown(t *testing.T) {
	var busy atomic.Bool
	entered, release := make(chan struct{}, 3), make(chan struct{})
	defer close(release)
	h := reviewedImageHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if busy.Load() {
				w.WriteHeader(503)
			} else {
				io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
			}
			return
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b","c"]}`))
	<-entered
	before := h.gw.cfg.Images.Info().ProbedAt
	busy.Store(true)
	if e := h.gw.cfg.Images.Refresh(context.Background()); e == nil {
		t.Fatal("expected failed busy probe")
	}
	info := h.gw.cfg.Images.Info()
	if !info.Health.OK || !info.ProbedAt.Equal(before) {
		t.Fatal("busy failure became down or fresh success", info)
	}
	busy.Store(false)
	for range 3 {
		release <- struct{}{}
	}
	for _, r := range rows {
		waitImageJob(t, h, r.ID, runstate.Done)
	}
	busy.Store(true)
	h.gw.cfg.Images.Refresh(context.Background())
	if h.gw.cfg.Images.Info().Health.OK {
		t.Fatal("idle failed probe was ignored")
	}
}
func TestPollingCannotSpendAnImageWorkersRPM(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 3 })
	for range 3 {
		if response := h.get("/v1/images/jobs"); response.status != 200 {
			t.Fatal(response.status)
		}
	}
	// An internal tool submission has no HTTP RPM cost; its admitted job must run.
	rows, e := h.gw.runs.SubmitBatch(h.key.ID, "image", "interactive", []json.RawMessage{imageInputForTest("one")})
	if e != nil {
		t.Fatal(e)
	}
	waitImageJob(t, h, rows[0].ID, runstate.Done)
	if h.gw.Counters(h.key.ID).RPMUsed != 3 {
		t.Fatal("worker spent HTTP RPM")
	}
}
func TestParallelImageReadsDuringChat(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	r, e := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.StdEncoding.DecodeString(tinyImage())
	if _, e = h.gw.runs.Store.PutImage(h.key.ID, r.ID, raw, "image/png", 2, 2); e != nil {
		t.Fatal(e)
	}
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1; k.Limits.RPM = 20 })
	held, err := h.gw.lim.admit(h.key.ID, h.key.Limits)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan int, 4)
	for range 4 {
		go func() { results <- h.get("/v1/images/outputs/" + r.ID).status }()
	}
	for range 4 {
		if status := <-results; status != 200 {
			t.Fatal("grid blocked by chat", status)
		}
	}
	h.gw.lim.settle(held, false, 0)
	if h.gw.Counters(h.key.ID).RPMUsed != 0 || h.gw.Counters(h.key.ID).InFlight != 0 {
		t.Fatal(h.gw.Counters(h.key.ID))
	}
}
func TestImageHostStopAfterAndBeforeDispatch(t *testing.T) {
	for _, sent := range []bool{true, false} {
		t.Run(map[bool]string{true: "sent", false: "not-sent"}[sent], func(t *testing.T) {
			entered := make(chan struct{}, 1)
			h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				entered <- struct{}{}
				<-r.Context().Done()
			})
			if !sent {
				h.gw.router.route(string(imagesEndpoint)).imageProbeAfter.Store(time.Now().UnixNano())
			}
			row := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["stop"]}`))[0]
			if sent {
				<-entered
			} else {
				waitUntil(t, time.Second, "pending attempt", func() bool { r, _ := h.gw.runs.Store.Get(h.key.ID, row.ID); return len(r.Attempts) == 1 })
			}
			h.gw.runs.Close()
			r, e := h.gw.runs.Store.Get(h.key.ID, row.ID)
			if e != nil {
				t.Fatal(e)
			}
			code, want := Code("interrupted"), 0
			if sent {
				code, want = CodeImageAbandoned, 1
			}
			if r.Reason != string(code) || r.Attempts[0].Usage.Code != string(code) || h.gw.Counters(h.key.ID).TodayImages != want {
				t.Fatal(r, h.gw.Counters(h.key.ID))
			}
			// Interrupted is a durable job error, exposed inside the successful run GET,
			// rather than an HTTP error to a client whose host connection has stopped.
			response := h.get("/v1/runs/" + row.ID)
			var exposed runstate.Run
			if response.status != 200 || json.Unmarshal(response.body, &exposed) != nil {
				t.Fatal(response)
			}
			if sent {
				var failure errorBody
				if json.Unmarshal(exposed.Attempts[0].Output, &failure) != nil || failure.Error.Code != code || failure.Error.Message == "" {
					t.Fatal(exposed)
				}
			} else if exposed.Reason != "interrupted" {
				t.Fatal(exposed)
			}
			if sent && !strings.Contains(string(r.Attempts[0].Output), "host stopped while the engine was working") {
				t.Fatal(string(r.Attempts[0].Output))
			}
		})
	}
}
func TestRunPollsAndImageDiscardSpendRPM(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	r, e := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.StdEncoding.DecodeString(tinyImage())
	h.gw.runs.Store.PutImage(h.key.ID, r.ID, raw, "image/png", 2, 2)
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 2 })
	if h.get("/v1/runs/"+r.ID).status != 200 {
		t.Fatal("first poll")
	}
	if h.do("DELETE", "/v1/images/outputs/"+r.ID, "bearer", "").status != 200 {
		t.Fatal("discard")
	}
	for range 40 {
		h.expectErr(h.get("/v1/runs/"+r.ID), CodeRateLimited)
	}
	if h.gw.Counters(h.key.ID).RPMUsed != 2 {
		t.Fatal("unrated poll/discard")
	}
}
func TestDownImagesAreRetryableButUnsharedImagesAreNot(t *testing.T) {
	var down atomic.Bool
	h := reviewedImageHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
	})
	down.Store(true)
	h.gw.cfg.Images.Refresh(context.Background())
	response := h.post("/v1/images/jobs", `{"prompts":["one"]}`)
	h.expectErr(response, CodeUpstreamDown)
	if response.header.Get("Retry-After") == "" {
		t.Fatal("missing retry")
	}
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"text-only"} })
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["one"]}`), CodeNotFound)
}
func TestUnlimitedImageQueueReportsItsLiveBound(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("over-cap batch dispatched") })
	h.setKey(func(k *keys.Key) { k.Limits.MaxQueuedImages = -1 })
	var me meResponse
	json.Unmarshal(h.get("/me").body, &me)
	if me.Host.Images.QueueCap != 16 || me.Limits.MaxQueuedImages != 16 {
		t.Fatal(me.Host.Images, me.Limits)
	}
	body, _ := json.Marshal(map[string]any{"prompts": strings.Fields(strings.Repeat("image ", 17))})
	response := h.post("/v1/images/jobs", string(body))
	h.expectErr(response, CodeImageQueueFull)
	var envelope errorBody
	json.Unmarshal(response.body, &envelope)
	if envelope.Error.Limit != 16 {
		t.Fatal(string(response.body))
	}
	h.expectErr(h.post("/v1/images/generations", `{"prompt":"too many","n":17}`), CodeImageQueueFull)
}

func TestAbandonedEngineCannotParkTheImageWorker(t *testing.T) {
	var down atomic.Bool
	var calls atomic.Int32
	h := reviewedImageHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if down.Load() {
				w.WriteHeader(503)
			} else {
				io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
			}
			return
		}
		io.Copy(io.Discard, r.Body)
		calls.Add(1)
		down.Store(true)
		conn, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		conn.Close()
	})
	h.gw.imageRecoveryTimeout = 120 * time.Millisecond
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["abandoned","wait once","fail fast"]}`))
	waitImageJob(t, h, rows[0].ID, runstate.Failed)
	h.gw.cfg.Images.Refresh(context.Background())
	for _, row := range rows[1:] {
		r := waitImageJob(t, h, row.ID, runstate.Failed)
		if r.Reason != string(CodeUpstreamDown) {
			t.Fatal(r.Reason)
		}
		var failure errorBody
		if json.Unmarshal(r.Attempts[0].Output, &failure) != nil || failure.Error.RetryAfter != 3 || failure.Error.Message == "" {
			t.Fatal("missing async recovery detail", r)
		}
	}
	waitUntil(t, time.Second, "unspent reservations released", func() bool { return h.gw.Counters(h.key.ID).TodayImages == 1 })
	if calls.Load() != 1 {
		t.Fatal("dead engine received another generation", calls.Load())
	}
	response := h.post("/v1/images/jobs", `{"prompts":["still down"]}`)
	h.expectErr(response, CodeUpstreamDown)
	if response.header.Get("Retry-After") == "" {
		t.Fatal("missing retry")
	}
}

func TestHTTPImageBatchSpendsOnceAndAcceptedJobsSurvivePolling(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	defer close(release)
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 3 })
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["one","two"]}`))
	<-entered
	for range 2 {
		if r := h.get("/v1/images/jobs"); r.status != 200 {
			t.Fatal(r)
		}
	}
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["no room"]}`), CodeRateLimited)
	release <- struct{}{}
	<-entered
	release <- struct{}{}
	for _, row := range rows {
		waitImageJob(t, h, row.ID, runstate.Done)
	}
	stored, _ := h.gw.runs.Store.List(h.key.ID)
	if len(stored) != 2 || h.gw.Counters(h.key.ID).RPMUsed != 3 {
		t.Fatal(stored, h.gw.Counters(h.key.ID))
	}
}

func TestDispatchHealthRetryKeepsOriginalHold(t *testing.T) {
	var down atomic.Bool
	var calls atomic.Int32
	h := reviewedImageHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if down.Load() {
				w.WriteHeader(503)
			} else {
				io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
			}
			return
		}
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.gw.imageRecoveryTimeout = time.Second
	h.gw.imageProbeEvery = 10 * time.Millisecond
	execute := h.gw.runs.Execute
	h.gw.runs.Execute = func(ctx context.Context, key string, step runstate.Step, start func() error) (runstate.StepResult, error) {
		down.Store(true)
		h.gw.cfg.Images.Refresh(ctx) // Admission already committed, but no owned dispatch yet.
		return execute(ctx, key, step, start)
	}
	row := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["recover"]}`))[0]
	waitUntil(t, time.Second, "bounded health retry", func() bool { return h.gw.router.route(string(imagesEndpoint)).imageProbeAfter.Load() != 0 })
	st := h.gw.lim.state(h.key.ID)
	st.mu.Lock()
	held, reserved, charged := st.imageHolds[row.ID], st.meter("images").reserved, st.meter("images").today
	st.mu.Unlock()
	if !held || reserved != 1 || charged != 0 || calls.Load() != 0 {
		t.Fatal(held, reserved, charged, calls.Load())
	}
	down.Store(false)
	// The gateway drives recovery itself; no test-owned refresh loop.
	waitImageJob(t, h, row.ID, runstate.Done)
	if calls.Load() != 1 || h.gw.Counters(h.key.ID).TodayImages != 1 {
		t.Fatal(calls.Load(), h.gw.Counters(h.key.ID))
	}
}

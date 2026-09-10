package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
)

func TestImagesOwnRequestBeyondTextIdle(t *testing.T) {
	var active, peak atomic.Int32
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		if n > peak.Load() {
			peak.Store(n)
		}
		defer active.Add(-1)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(80 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.gw.idleTimeout = 5 * time.Millisecond
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b"]}`))
	for _, r := range rows {
		waitImageJob(t, h, r.ID, runstate.Done)
	}
	if peak.Load() != 1 || h.gw.Counters(h.key.ID).TodayImages != 2 {
		t.Fatal("request overlap or idle abandonment", peak.Load())
	}
}

func TestAbandonedImageWaitsForFreshProbe(t *testing.T) {
	var calls atomic.Int32
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			conn, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			conn.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	response := h.post("/v1/images/generations", `{"prompt":"reset"}`)
	h.expectErr(response, CodeImageAbandoned)
	if !strings.Contains(string(response.body), "connection") || strings.Contains(string(response.body), "in time") {
		t.Fatal(string(response.body))
	}
	if h.gw.Counters(h.key.ID).TodayImages != 1 {
		t.Fatal("abandoned request not charged")
	}
	failed, _ := h.gw.runs.Store.List(h.key.ID)
	ended, _ := h.gw.runs.Store.Get(h.key.ID, failed[0].ID)
	if ended.Reason != string(CodeImageAbandoned) {
		t.Fatal(ended.Reason)
	}
	row := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["after probe"]}`))[0]
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("dispatched before a fresh successful probe")
	}
	if e := h.gw.cfg.Images.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	waitImageJob(t, h, row.ID, runstate.Done)
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}

type failedImageWrite struct{ upstream.ImageEngine }

func (e failedImageWrite) ImageDo(ctx context.Context, _ []byte) (*http.Response, error) {
	trace := httptrace.ContextClientTrace(ctx)
	if trace.WroteHeaders != nil {
		trace.WroteHeaders()
	}
	err := errors.New("request body write failed")
	if trace.WroteRequest != nil {
		trace.WroteRequest(httptrace.WroteRequestInfo{Err: err})
	}
	return nil, err
}
func TestImageFailedWriteDoesNotCharge(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("should not dispatch") })
	d := h.gw.router.route(string(imagesEndpoint))
	d.Images = failedImageWrite{d.Images}
	h.post("/v1/images/generations", `{"prompt":"never sent"}`)
	if h.gw.Counters(h.key.ID).TodayImages != 0 {
		t.Fatal("charged headers without a sent body")
	}
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	r, _ := h.gw.runs.Store.Get(h.key.ID, rows[0].ID)
	if r.Attempts[0].Dispatched {
		t.Fatal("marked failed write dispatched")
	}
}
func TestImageReadsAdmitAndSpendRPM(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	r, e := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.StdEncoding.DecodeString(tinyImage())
	if _, e = h.gw.runs.Store.PutImage(h.key.ID, r.ID, raw, "image/png", 2, 2); e != nil {
		t.Fatal(e)
	}
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 2; k.Limits.MaxConcurrent = 1 })
	a, err := h.gw.lim.admit(h.key.ID, h.key.Limits)
	if err != nil {
		t.Fatal(err)
	}
	h.expectErr(h.get("/v1/images/outputs/"+r.ID), CodeConcurrencyLimited)
	h.gw.lim.settle(a, false, 0)
	if h.get("/v1/images/jobs").status != 200 || h.get("/v1/images/outputs/"+r.ID).status != 200 {
		t.Fatal("read failed")
	}
	h.expectErr(h.get("/v1/images/jobs"), CodeRateLimited)
	if h.gw.Counters(h.key.ID).RPMUsed != 2 {
		t.Fatal("reads did not spend RPM")
	}
}
func imageInputForTest(prompt string) json.RawMessage {
	b, _ := json.Marshal(runstate.ImageInput{Prompt: prompt})
	return b
}
func TestImagePinAndPromptLogging(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
		})
		h.gw.cfg.ModelsPinned = []string{"m1"}
		h.gw.router.pinned = []string{"m1"}
		h.gw.cfg.LogPrompts = enabled
		if h.gw.imageOffer(h.key) == nil {
			t.Fatal("text pin hid images")
		}
		row := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["retained prompt"]}`))[0]
		r := waitImageJob(t, h, row.ID, runstate.Done)
		want := ""
		if enabled {
			want = "retained prompt"
		}
		if r.Attempts[0].Usage.Prompt != want {
			t.Fatal("log opt-in not honored")
		}
		h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m1"} })
		if h.gw.imageOffer(h.key) != nil {
			t.Fatal("key allowlist ignored")
		}
	}
}

func TestImageDailyBatchRefusalCreatesNoRows(t *testing.T) {
	var calls atomic.Int32
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = 1 })
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["a","b","c"]}`), CodeImageBudgetExhausted)
	h.expectErr(h.post("/v1/images/generations", `{"prompt":"same atomic path","n":2}`), CodeImageBudgetExhausted)
	rows, e := h.gw.runs.Store.List(h.key.ID)
	if e != nil || len(rows) != 0 || calls.Load() != 0 || h.gw.Counters(h.key.ID).TodayImages != 0 {
		t.Fatal(rows, e, calls.Load())
	}
	// The non-HTTP caller used by tools gets the same refusal.
	_, e = h.gw.runs.SubmitBatch(h.key.ID, "image", "interactive", []json.RawMessage{imageInputForTest("a"), imageInputForTest("b")})
	var refusal *gwError
	if !errors.As(e, &refusal) || refusal.Code != CodeImageBudgetExhausted {
		t.Fatal(e)
	}
}

func TestImageConcurrentBatchesReserveWholeDay(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = 2 })
	results := make(chan resp, 2)
	for range 2 {
		go func() { results <- h.post("/v1/images/jobs", `{"prompts":["a","b"]}`) }()
	}
	a, b := <-results, <-results
	if a.status != 202 {
		a, b = b, a
	}
	if a.status != 202 {
		t.Fatal(a.status, b.status)
	}
	h.expectErr(b, CodeImageBudgetExhausted)
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	if len(rows) != 2 || h.gw.Counters(h.key.ID).TodayImages != 2 {
		t.Fatal("partial or over-budget batch", rows)
	}
}

func TestImageQueuedCancelReleasesAcrossMidnight(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	var now atomic.Int64
	now.Store(time.Date(2026, 9, 10, 23, 59, 59, 0, time.UTC).UnixNano())
	h.gw.lim.now = func() time.Time { return time.Unix(0, now.Load()) }
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = 2 })
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["running","queued"]}`))
	<-entered
	now.Add(int64(2 * time.Second))
	if h.gw.Counters(h.key.ID).TodayImages != 2 {
		t.Fatal("midnight discarded reservations")
	}
	if _, e := h.gw.runs.Cancel(h.key.ID, rows[1].ID); e != nil {
		t.Fatal(e)
	}
	if h.gw.Counters(h.key.ID).TodayImages != 1 {
		t.Fatal("queued cancel leaked reservation")
	}
	extra := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["replacement"]}`))[0]
	if _, e := h.gw.runs.Cancel(h.key.ID, extra.ID); e != nil {
		t.Fatal(e)
	}
	release <- struct{}{}
	r := waitImageJob(t, h, rows[0].ID, runstate.Done)
	if h.gw.Counters(h.key.ID).TodayImages != 1 || r.Attempts[0].Usage.SettledAt.UTC().Day() != 11 {
		t.Fatal("wrong settlement day", r)
	}
}

func TestImageUnlimitedEffectiveValues(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = -99; k.Limits.MaxQueuedImages = -2 })
	var me meResponse
	if e := json.Unmarshal(h.get("/me").body, &me); e != nil {
		t.Fatal(e)
	}
	if me.Limits.DailyImages != -1 || me.Limits.MaxQueuedImages != -1 || me.Host.Images.QueueCap != -1 {
		t.Fatal(me.Limits, me.Host.Images)
	}
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b"]}`))
	for _, r := range rows {
		waitImageJob(t, h, r.ID, runstate.Done)
	}
}

func TestImageListAtRetainedBound(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 100 })
	for i := 0; i < 98; i++ {
		r, e := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest(strings.Repeat("p", 100)))
		if e != nil {
			t.Fatal(e)
		}
		if i < 90 {
			if _, e = h.gw.runs.Cancel(h.key.ID, r.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	start := time.Now()
	for range 20 {
		response := h.get("/v1/images/jobs")
		if response.status != 200 {
			t.Fatal(response.status, string(response.body))
		}
		var list struct {
			Jobs []imageJob `json:"jobs"`
		}
		if e := json.Unmarshal(response.body, &list); e != nil {
			t.Fatal(e)
		}
		ranked := 0
		for _, r := range list.Jobs {
			if r.Position > 0 {
				ranked++
			}
		}
		if len(list.Jobs) != 98 || ranked != 8 {
			t.Fatal(len(list.Jobs), ranked)
		}
	}
	t.Logf("98 retained rows/8 queued: %.2f ms/list over 20 requests", float64(time.Since(start).Microseconds())/20000)
}

func TestImageQueuedKeyRevocationReleasesReservation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.setKey(func(k *keys.Key) { k.Limits.DailyImages = 2 })
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["sent","not sent"]}`))
	<-entered
	h.setKey(func(k *keys.Key) { k.Status = keys.Revoked })
	release <- struct{}{}
	waitImageJob(t, h, rows[0].ID, runstate.Done)
	waitImageJob(t, h, rows[1].ID, runstate.Failed)
	waitUntil(t, time.Second, "unused hold released", func() bool { return h.gw.Counters(h.key.ID).TodayImages == 1 })
}

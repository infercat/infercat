package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func TestHealthyProbesBrokenGenerationBacksOffAndRefundsSuspect(t *testing.T) {
	var mu sync.Mutex
	var stamps []time.Time
	var status []DestinationStatus
	var h *harness
	h = reviewedImageHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
			return
		}
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		stamps = append(stamps, time.Now())
		n := len(stamps)
		for _, d := range h.gw.Destinations() {
			if d.ID == "images" {
				status = append(status, d)
			}
		}
		mu.Unlock()
		if n <= 6 {
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.gw.imageBackoffBase = 10 * time.Millisecond
	h.gw.imageProbeEvery = 5 * time.Millisecond
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b","c","d","e","f","g"]}`))
	for i, row := range rows {
		state := runstate.Failed
		if i == 6 {
			state = runstate.Done
		}
		r := waitImageJob(t, h, row.ID, state)
		want := float64(0)
		if i < 2 || i == 6 {
			want = 1
		}
		if got := r.Attempts[0].Usage.Meters[0].Charged; got != want {
			t.Fatal(i, got, want)
		}
		if i >= 2 && i < 6 && !strings.Contains(string(r.Attempts[0].Output), "did not count") {
			t.Fatal(r)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 2; i < 7; i++ {
		minimum := time.Duration(min(1<<(i-2), 15)) * 10 * time.Millisecond
		if stamps[i].Sub(stamps[i-1]) < minimum {
			t.Fatal("missing backoff", i, stamps[i].Sub(stamps[i-1]))
		}
	}
	// Status belongs to the image destination, independent of destination order.
	for _, d := range h.gw.Destinations() {
		if d.ID == "images" && (d.ImageAbandons != 0 || d.ImageRetryAt != 0) {
			t.Fatal("success did not clear suspect", d)
		}
	}
	if h.gw.Counters(h.key.ID).TodayImages != 3 {
		t.Fatal(h.gw.Counters(h.key.ID))
	}
	found := false
	for _, d := range status {
		if d.ImageAbandons >= 2 && d.ImageRetryAt > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("suspect absent from status", status)
	}
}
func TestGalleryReadsCannotRateLimitChat(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	row, e := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.StdEncoding.DecodeString(tinyImage())
	h.gw.runs.Store.PutImage(h.key.ID, row.ID, raw, "image/png", 2, 2)
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 1 })
	for range 40 {
		if r := h.get("/v1/images/outputs/" + row.ID); r.status != 200 {
			t.Fatal(r)
		}
	}
	if h.gw.Counters(h.key.ID).RPMUsed != 0 {
		t.Fatal(h.gw.Counters(h.key.ID))
	}
	admission, err := h.gw.lim.admit(h.key.ID, h.key.Limits)
	if err != nil {
		t.Fatal("gallery blocked chat", err)
	}
	h.gw.lim.settle(admission, false, 0)
}
func TestNeverProbedImageEngineRespectsUnknownModelAllowlist(t *testing.T) {
	h := reviewedImageHarness(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"text-only"} })
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["one"]}`), CodeNotFound)
	h.setKey(func(k *keys.Key) { k.Limits.Models = nil })
	h.expectErr(h.post("/v1/images/jobs", `{"prompts":["one"]}`), CodeUpstreamDown)
}
func TestReleasedImageFailureIsNotRelabelledChargedOnStop(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := h.gw.newRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx))
	q.image = &imageRequest{definitiveFailure: true}
	q.dispatched.Store(true)
	q.fail(errf(CodeUpstreamError, 0, "no output"))
	if q.ev.Code != string(CodeUpstreamError) || strings.Contains(string(q.image.failure), "counted") {
		t.Fatal(q.ev, string(q.image.failure))
	}
}
func TestRunCancelIsRated(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	row, _ := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 1 })
	if r := h.do("DELETE", "/v1/runs/"+row.ID, "bearer", ""); r.status != 200 {
		t.Fatal(r)
	}
	h.expectErr(h.do("DELETE", "/v1/runs/"+row.ID, "bearer", ""), CodeRateLimited)
}

func TestDefinitiveFailureDoesNotAdvanceSuspectBackoff(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	d := h.gw.router.route(string(imagesEndpoint))
	h.gw.imageBackoffBase = time.Millisecond
	q := h.gw.newRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/images/generations", nil))
	q.destination = d
	q.dispatched.Store(true)
	q.image = &imageRequest{abandoned: true}
	q.recordImageResult()
	q.recordImageResult()
	if d.imageBackoff.Load() != int64(time.Millisecond) {
		t.Fatal("first suspect wait")
	}
	q.image = &imageRequest{suspect: true, definitiveFailure: true}
	q.recordImageResult()
	if d.imageAbandons.Load() != 2 || d.imageBackoff.Load() != int64(time.Millisecond) {
		t.Fatal("definitive failure advanced backoff")
	}
	q.image.measured = true
	q.recordImageResult()
	if d.imageBackoff.Load() != 0 || d.imageAbandons.Load() != 0 {
		t.Fatal("success did not clear")
	}
}

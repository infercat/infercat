package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runstate "github.com/infercat/infercat/internal/run"
)

func TestImageStorageFaultReleasesWithoutTrainingOrClearingBreaker(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	dir := filepath.Join(h.gw.cfg.DataDir, "runs", h.key.ID)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "images"), []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	d := h.gw.router.route(string(imagesEndpoint))
	d.imageAbandons.Store(2)
	retry := time.Now().Add(time.Millisecond).UnixNano()
	d.imageRetryAt.Store(retry)
	trained := make(chan time.Duration, 8)
	d.Images = observedImageSuccess{d.Images, trained}
	var logs logBuf
	h.gw.logf = logs.logf
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b","c","d"]}`))
	for _, row := range rows {
		r := waitImageJob(t, h, row.ID, runstate.Failed)
		if r.Reason != "storage_failed" || r.Attempts[0].Usage.Meters[0].Charged != 0 || r.Attempts[0].Usage.Meters[0].Measured != 0 {
			t.Fatal(r)
		}
		var failure errorBody
		if json.Unmarshal(r.Attempts[0].Output, &failure) != nil || failure.Error.Code != CodeStorageFailed || r.Attempts[0].Usage.Status != 500 || !strings.Contains(failure.Error.Message, "host could not store") {
			t.Fatal(r)
		}
	}
	response := h.post("/v1/images/generations", `{"prompt":"sync storage fault"}`)
	h.expectErr(response, CodeStorageFailed)
	if response.status != 500 || response.message != "the host could not store this image; it did not count" {
		t.Fatal(response)
	}
	if h.gw.Counters(h.key.ID).TodayImages != 0 || len(trained) != 0 || d.imageAbandons.Load() != 2 || d.imageRetryAt.Load() != retry {
		t.Fatal("storage fault changed accounting or engine history")
	}
	if !strings.Contains(logs.String(), "image storage failed") {
		t.Fatal("storage failure was silent")
	}
}
func TestOversizedResponsesAreFreeButTripBreaker(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("X", 12<<20))) })
	h.gw.imageBackoffBase = time.Millisecond
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b","c"]}`))
	for _, row := range rows {
		r := waitImageJob(t, h, row.ID, runstate.Failed)
		if r.Attempts[0].Usage.Meters[0].Charged != 0 {
			t.Fatal(r)
		}
	}
	if h.gw.Counters(h.key.ID).TodayImages != 0 || h.gw.router.route(string(imagesEndpoint)).imageAbandons.Load() != 3 {
		t.Fatal("oversize did not trip breaker free of charge")
	}
}
func TestImageOfferOmitsElapsedRetryInstant(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	d := h.gw.router.route(string(imagesEndpoint))
	d.imageAbandons.Store(2)
	d.imageRetryAt.Store(time.Now().Add(-time.Second).UnixNano())
	var me meResponse
	json.Unmarshal(h.get("/me").body, &me)
	if me.Host.Images.RetryAt != nil {
		t.Fatal("elapsed retry exposed")
	}
	d.imageRetryAt.Store(time.Now().Add(time.Minute).UnixNano())
	json.Unmarshal(h.get("/me").body, &me)
	if me.Host.Images.RetryAt == nil {
		t.Fatal("future retry omitted")
	}
}

package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type heldImageWriter struct {
	*httptest.ResponseRecorder
	entered chan<- struct{}
	release <-chan struct{}
}

func (w heldImageWriter) Write(p []byte) (int, error) {
	w.entered <- struct{}{}
	<-w.release
	return 0, io.ErrClosedPipe
}
func TestImageReadSlotsBoundDeliveryAndReleaseOnDisconnect(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	row, e := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.StdEncoding.DecodeString(tinyImage())
	h.gw.runs.Store.PutImage(h.key.ID, row.ID, raw, "image/png", 2, 2)
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 1; k.Limits.MaxConcurrent = 1 })
	entered, release := make(chan struct{}, 4), make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	defer releaseAll()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/v1/images/outputs/"+row.ID, nil)
			r.Header.Set("Authorization", "Bearer "+testSecret)
			h.gw.Handler().ServeHTTP(heldImageWriter{httptest.NewRecorder(), entered, release}, r)
		}()
	}
	for range 4 {
		<-entered
	}
	st := h.gw.lim.state(h.key.ID)
	st.mu.Lock()
	active := st.imageReads
	st.mu.Unlock()
	if active != 4 {
		t.Fatal(active)
	}
	for range 20 {
		r := h.get("/v1/images/outputs/" + row.ID)
		h.expectErr(r, CodeConcurrencyLimited)
		if r.header.Get("Retry-After") != "1" || r.message != "too many images loading at once; retry in a second" {
			t.Fatal(r)
		}
	}
	if h.gw.Counters(h.key.ID).RPMUsed != 0 || h.gw.Counters(h.key.ID).InFlight != 0 {
		t.Fatal("read slots spent chat capacity")
	}
	releaseAll()
	wg.Wait()
	st.mu.Lock()
	active = st.imageReads
	st.mu.Unlock()
	if active != 0 {
		t.Fatal("read slot leaked", active)
	}
	if r := h.get("/v1/images/outputs/" + row.ID); r.status != 200 {
		t.Fatal(r)
	}
}
func TestExpiredRunPollAndCancelRefundRPM(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 1 })
	for range 16 {
		for _, method := range []string{"GET", "DELETE"} {
			h.expectErr(h.do(method, "/v1/runs/r_missing", "bearer", ""), CodeNotFound)
		}
	}
	if h.gw.Counters(h.key.ID).RPMUsed != 0 {
		t.Fatal(h.gw.Counters(h.key.ID))
	}
	row, _ := h.gw.runs.Store.Create(h.key.ID, "image", "interactive", imageInputForTest("one"))
	if h.get("/v1/runs/"+row.ID).status != 200 {
		t.Fatal("404 polls consumed RPM")
	}
	h.expectErr(h.get("/v1/runs/"+row.ID), CodeRateLimited)
}
func TestSyncCompletionIsIndependentOfProbeCadence(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": tinyImage()}}})
	})
	h.gw.imageProbeEvery = time.Minute
	start := time.Now()
	response := h.post("/v1/images/generations", `{"prompt":"one","n":4}`)
	if response.status != 200 || time.Since(start) > 2*time.Second {
		t.Fatal(response.status, time.Since(start))
	}
}
func TestInvalidOutputsTripBreakerAndExposeRetryTime(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":[{"b64_json":"bad"}]}`) })
	// The worker waits 0.5 s then 1 s (at least 2 s below each row bound);
	// retry_at stays visible for 2 s instead of racing a 40 ms window.
	h.gw.imageBackoffBase = 500 * time.Millisecond
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["a","b","c","d"]}`))
	for i, row := range rows {
		r := waitImageJob(t, h, row.ID, runstate.Failed)
		want := float64(0)
		if i < 2 {
			want = 1
		}
		if got := r.Attempts[0].Usage.Meters[0].Charged; got != want {
			t.Fatal(i, got, want)
		}
	}
	if h.gw.router.route(string(imagesEndpoint)).imageAbandons.Load() != 4 {
		t.Fatal("invalid outputs not counted")
	}
	var me meResponse
	json.Unmarshal(h.get("/me").body, &me)
	if me.Host.Images.RetryAt == nil {
		t.Fatal("suspect missing retry_at")
	}
	if _, e := time.Parse(time.RFC3339Nano, me.Host.Images.RetryAt.Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
}
func TestOversizedUnparsedResponseReleasesAndErrorsDoNotPersist(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "snippet", true: "oversized"}[large], func(t *testing.T) {
			h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
				if large {
					io.WriteString(w, strings.Repeat("X", 12<<20))
					return
				}
				w.WriteHeader(500)
				io.WriteString(w, `{"error":"private engine diagnostic"}`)
			})
			row := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["one"]}`))[0]
			r := waitImageJob(t, h, row.ID, runstate.Failed)
			if r.Attempts[0].Usage.Meters[0].Charged != 0 {
				t.Fatal(r)
			}
			want := int64(0)
			if large {
				want = 1
				if len(r.Attempts[0].Output) == 0 {
					t.Fatal("missing authored limit error")
				}
			} else if text := string(r.Attempts[0].Output); !strings.Contains(text, "image engine HTTP 500: Internal Server Error") || strings.Contains(text, "private engine diagnostic") {
				t.Fatalf("authored status missing or engine snippet persisted: %s", text)
			}
			if got := h.gw.router.route(string(imagesEndpoint)).imageAbandons.Load(); got != want {
				t.Fatal(got, want)
			}
		})
	}
}

type observedImageSuccess struct {
	upstream.ImageEngine
	durations chan time.Duration
}

func (o observedImageSuccess) RecordSuccess(d time.Duration) {
	o.durations <- d
	o.ImageEngine.RecordSuccess(d)
}
func TestOnlyValidatedImageTrainsGenerationDuration(t *testing.T) {
	count := 0
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		time.Sleep(20 * time.Millisecond)
		data := "bad"
		if count == 2 {
			data = tinyImage()
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": data}}})
	})
	durations := make(chan time.Duration, 2)
	d := h.gw.router.route(string(imagesEndpoint))
	d.Images = observedImageSuccess{d.Images, durations}
	rows := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["bad","good"]}`))
	waitImageJob(t, h, rows[0].ID, runstate.Failed)
	waitImageJob(t, h, rows[1].ID, runstate.Done)
	if len(durations) != 1 {
		t.Fatal("unvalidated output trained duration", len(durations))
	}
	if elapsed := <-durations; elapsed < 20*time.Millisecond {
		t.Fatal("generation duration omitted HTTP wait", elapsed)
	}
}

func TestSyncLegacyHealthFailureKeepsRetryWithoutErrorText(t *testing.T) {
	h := imagesHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("must not dispatch") })
	h.gw.runs.Execute = func(context.Context, string, runstate.Step, func() error) (runstate.StepResult, error) {
		return runstate.StepResult{Settled: true, Usage: usage.Event{Status: 503, Code: string(CodeUpstreamDown)}}, io.ErrUnexpectedEOF
	}
	response := h.post("/v1/images/generations", `{"prompt":"one"}`)
	h.expectErr(response, CodeUpstreamDown)
	if response.header.Get("Retry-After") != "3" {
		t.Fatal(response)
	}
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	row, _ := h.gw.runs.Store.Get(h.key.ID, rows[0].ID)
	if len(row.Attempts[0].Output) != 0 {
		t.Fatal("error text was persisted", row)
	}
}

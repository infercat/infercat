package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestImageDeadlineCoversHeadersAndBody(t *testing.T) {
	for _, flush := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[flush], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					io.WriteString(w, `{"data":[{"id":"images"}]}`)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if flush {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			engine, e := OpenImages(context.Background(), server.URL, "")
			if e != nil {
				t.Fatal(e)
			}
			c := engine.(*imageClient).c
			if c.hc.Timeout != 15*time.Minute || c.hc.Transport.(*http.Transport).ResponseHeaderTimeout != 15*time.Minute {
				t.Fatal("image inherited text first-byte deadline")
			}
			c.hc.Timeout = 30 * time.Millisecond
			response, e := engine.ImageDo(context.Background(), []byte(`{}`))
			if e == nil {
				_, e = io.ReadAll(response.Body)
				response.Body.Close()
			}
			if e == nil {
				t.Fatal("generation ceiling did not cover response")
			}
		})
	}
}

func TestFailedProbeOverlappingWholeImageRequestStaysUnknown(t *testing.T) {
	probeEntered, releaseProbe := make(chan struct{}), make(chan struct{})
	var delay atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/v1/models" {
			if delay.Load() {
				close(probeEntered)
				<-releaseProbe
				w.WriteHeader(503)
			} else {
				io.WriteString(w, `{"data":[{"id":"images"}]}`)
			}
			return
		}
		io.WriteString(w, `{"data":[]}`)
	}))
	defer server.Close()
	engine, err := OpenImages(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	before := engine.Info().ProbedAt
	delay.Store(true)
	done := make(chan error, 1)
	go func() { done <- engine.Refresh(context.Background()) }()
	<-probeEntered
	response, err := engine.ImageDo(context.Background(), []byte(`{}`))
	if err != nil {
		close(releaseProbe)
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	close(releaseProbe)
	if <-done == nil {
		t.Fatal("expected failed probe")
	}
	if info := engine.Info(); !info.Health.OK || !info.ProbedAt.Equal(before) {
		t.Fatal("overlapping request was forgotten", info)
	}
}

func TestOwnedImageProbeSuppressionExpiresAtThreeMinutes(t *testing.T) {
	var down atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if down.Load() {
				w.WriteHeader(503)
			} else {
				io.WriteString(w, `{"data":[{"id":"images"}]}`)
			}
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	engine, _ := OpenImages(context.Background(), server.URL, "")
	response, e := engine.ImageDo(context.Background(), []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	c := engine.(*imageClient)
	down.Store(true)
	engine.Refresh(context.Background())
	if !engine.Info().Health.OK {
		t.Fatal("young owned request marked down")
	}
	c.mu.Lock()
	c.started = time.Now().Add(-3 * time.Minute)
	c.mu.Unlock()
	engine.Refresh(context.Background())
	if engine.Info().Health.OK {
		t.Fatal("old owned request kept offer alive")
	}
}

func TestImageGraceLearnsOnlyReportedSuccessAndCapsAtTenMinutes(t *testing.T) {
	var down atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if down.Load() {
				w.WriteHeader(503)
			} else {
				io.WriteString(w, `{"data":[{"id":"images"}]}`)
			}
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	engine, _ := OpenImages(context.Background(), server.URL, "")
	response, e := engine.ImageDo(context.Background(), []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	c := engine.(*imageClient)
	c.RecordSuccess(5 * time.Minute)
	c.RecordSuccess(time.Minute) // A faster success must not shrink the learned grace.
	c.mu.Lock()
	c.started = time.Now().Add(-6 * time.Minute)
	c.mu.Unlock()
	down.Store(true)
	engine.Refresh(context.Background())
	if !engine.Info().Health.OK {
		t.Fatal("5-minute engine lost learned 10-minute grace")
	}
	c.mu.Lock()
	c.started = time.Now().Add(-10 * time.Minute)
	c.mu.Unlock()
	engine.Refresh(context.Background())
	if engine.Info().Health.OK {
		t.Fatal("learned grace never expires")
	}
	c.RecordSuccess(14 * time.Minute)
	down.Store(false)
	engine.Refresh(context.Background())
	down.Store(true)
	c.mu.Lock()
	c.started = time.Now().Add(-10 * time.Minute)
	c.mu.Unlock()
	engine.Refresh(context.Background())
	if engine.Info().Health.OK {
		t.Fatal("grace exceeded ten minutes after a fourteen-minute success")
	}
}

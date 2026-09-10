package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/bridge"
)

func TestPreviewRequiresOwnedConnectedRegistrationAndBounds(t *testing.T) {
	c := bridge.Config{Endpoint: previewOrigin, Host: "fixture"}
	h := &hostSample{Bridge: &bridge.Status{Enabled: true, Connected: true, URL: c.URL()}}
	o := previewOptions{hostDir: "fixture", pid: 1, model: "fixture", n: 60, duration: 2 * time.Minute}
	if err := o.validate(c, h); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*previewOptions){func(o *previewOptions) { o.n = 61 }, func(o *previewOptions) { o.duration = 3 * time.Minute }, func(o *previewOptions) { o.earlyClose = true }, func(o *previewOptions) { o.pid = 0 }} {
		bad := o
		change(&bad)
		if bad.validate(c, h) == nil {
			t.Fatal("unbounded/invalid preview accepted")
		}
	}
	c.Endpoint = "https://example.com"
	if o.validate(c, h) == nil {
		t.Fatal("third-party origin accepted")
	}
	c.Endpoint = previewOrigin
	h.Bridge.Connected = false
	if o.validate(c, h) == nil {
		t.Fatal("offline registration accepted")
	}
}

func TestPreviewStreamMeasuresTokensAndRequiresDone(t *testing.T) {
	for _, kind := range []string{"complete", "truncated", "early-close"} {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing friend credential")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
				w.(http.Flusher).Flush()
				time.Sleep(25 * time.Millisecond)
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n")
				if kind != "truncated" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			s := &session{http: srv.Client(), baseURL: srv.URL, secret: "fixture", closeAfterToken: kind == "early-close"}
			rec, _, _ := s.chat(context.Background(), "fixture", []msg{{"user", "hello"}}, 16, true)
			if rec.Status != 200 || rec.TTFTMS < 20 {
				t.Fatalf("role frame counted as token: %+v", rec)
			}
			switch kind {
			case "complete":
				if !rec.Done || rec.Err != "" {
					t.Fatalf("complete: %+v", rec)
				}
			case "truncated":
				if rec.Done || rec.Err == "" {
					t.Fatalf("truncated stream counted as success: %+v", rec)
				}
			case "early-close":
				if rec.Done || !rec.Aborted || rec.Code != "client_closed" {
					t.Fatalf("early-close: %+v", rec)
				}
			}
		})
	}
}

func TestPreviewPercentilesUseNearestRank(t *testing.T) {
	v := []float64{40, 10, 30, 20}
	if previewPercentile(v, .5) != 20 || previewPercentile(v, .95) != 40 || previewPercentile(nil, .5) != 0 || v[0] != 40 {
		t.Fatal("percentiles changed samples or used the wrong rank")
	}
}

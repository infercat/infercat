package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExplicitAudioProbeAndTransport(t *testing.T) {
	var models, health atomic.Bool
	models.Store(true)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer audio-key" {
			t.Error("missing bearer")
		}
		switch r.URL.Path {
		case "/v1/models":
			if models.Load() {
				_, _ = io.WriteString(w, `{"data":[{"id":"voice"}]}`)
			} else {
				http.NotFound(w, r)
			}
		case "/health":
			if !health.Load() {
				http.Error(w, "down", 503)
			} else {
				_, _ = io.WriteString(w, "healthy")
			}
		case "/v1/audio/speech":
			calls++
			if r.Header.Get("Content-Type") != "application/custom" {
				t.Error("content type changed")
			}
			b, _ := io.ReadAll(r.Body)
			if string(b) != "payload" {
				t.Error("payload changed")
			}
			w.Header().Set("Location", "/must-not-follow")
			w.WriteHeader(307)
		default:
			t.Error("followed redirect")
		}
	}))
	defer server.Close()
	a, err := OpenAudio(context.Background(), server.URL+"/v1", "audio-key")
	if err != nil || !a.Info().Health.OK || a.Info().Models[0] != "voice" {
		t.Fatalf("probe %v %+v", err, a.Info())
	}
	models.Store(false)
	health.Store(true)
	if err := a.Refresh(context.Background()); err != nil || !a.Info().Health.OK {
		t.Fatal("health fallback failed")
	}
	health.Store(false)
	if err := a.Refresh(context.Background()); err == nil || a.Info().Health.OK {
		t.Fatal("offline probe was healthy")
	}
	r, err := a.AudioDo(context.Background(), "/v1/audio/speech", "application/custom", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 307 || calls != 1 {
		t.Fatal("redirect replayed")
	}
	if a, err := OpenAudio(context.Background(), "", ""); a != nil || err != nil {
		t.Fatal("unset route became configured")
	}
	if _, err := OpenAudio(context.Background(), "://broken", ""); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatal("invalid URL accepted")
	}
}

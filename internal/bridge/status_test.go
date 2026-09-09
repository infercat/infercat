package bridge

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/usage"
)

func TestStatusRestoresTodayWhileDisabled(t *testing.T) {
	dir := t.TempDir()
	c := Config{Endpoint: "https://example.test", Host: "h", Token: strings.Repeat("a", 64), Disabled: true}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	r, err := usage.NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, e := range []usage.Event{
		{TS: today, Via: "bridge", Status: 200, Endpoint: "/v1/models"},
		{TS: today, Via: "bridge", Status: 429, Code: "rate_limited", Endpoint: "/v1/chat/completions"},
		{TS: today, Status: 200},
		{TS: today.Add(-time.Second), Via: "bridge", Status: 200},
		{TS: today.Add(24 * time.Hour), Via: "bridge", Status: 200},
	} {
		r.Record(context.Background(), e)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 { // A fresh manager reads the same count after a restart.
		var m Manager
		if err := m.Reload(context.Background(), dir, http.NotFoundHandler(), t.Logf); err != nil {
			t.Fatal(err)
		}
		if s := m.Status(dir); s == nil || s.Enabled || s.Connected || s.RequestsToday != 2 || s.LastError != "" {
			t.Fatalf("restored status: %+v", s)
		}
		m.Close()
	}
}

func TestSwitchRefusalPreservesRegistration(t *testing.T) {
	dir := t.TempDir()
	if err := SetEnabled(dir, true); err == nil {
		t.Fatal("enabled without a stored token")
	}
	path := filepath.Join(dir, FileName)
	invalid := []byte(`{"token":"incomplete"}`)
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if SetEnabled(dir, enabled) == nil {
			t.Fatal("rewrote an unreadable registration")
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != string(invalid) {
			t.Fatal("refusal changed the saved state")
		}
	}
}

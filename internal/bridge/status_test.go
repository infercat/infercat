package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		if s := m.Status(); s == nil || s.Enabled || s.Connected || s.RequestsToday != 2 || s.LastError != "" {
			t.Fatalf("restored status: %+v", s)
		}
		m.Close()
	}
}

func counterFixture(t *testing.T) (*Manager, string, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	c := Config{Endpoint: "https://example.test", Host: "h", Token: strings.Repeat("a", 64), Disabled: true}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	m := &Manager{now: func() time.Time { return now }}
	t.Cleanup(m.Close)
	return m, dir, &now
}

func writeCounterHistory(t *testing.T, dir string, events ...usage.Event) {
	t.Helper()
	var b strings.Builder
	for _, e := range events {
		if err := json.NewEncoder(&b).Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, usage.FileName), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCounterStatusAndDayRollNeverReadHistory(t *testing.T) {
	m, dir, now := counterFixture(t)
	writeCounterHistory(t, dir,
		usage.Event{TS: now.Add(-time.Hour), Via: "bridge", Status: 200},
		usage.Event{TS: now.Add(-time.Hour), Via: "bridge", Status: 429, Code: "rate_limited"},
		usage.Event{TS: now.Add(-time.Hour), Status: 200})
	if err := m.Reload(context.Background(), dir, http.NotFoundHandler(), t.Logf); err != nil {
		t.Fatal(err)
	}
	// Any new read fails: neither polls nor successful-seed reloads may touch this path.
	path := filepath.Join(dir, usage.FileName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	events := make(chan usage.Event, 4)
	r := Recorder{Next: eventRecorder{events}, Count: m.Count}
	r.Record(context.WithValue(context.Background(), viaKey{}, true), usage.Event{TS: *now, Status: 200})
	r.Record(context.Background(), usage.Event{TS: *now, Status: 200})
	if (<-events).Via != "bridge" || (<-events).Via != "" {
		t.Fatal("counter hook changed event provenance")
	}
	for range 1000 {
		if s := m.Status(); s.RequestsToday != 3 || s.LastError != "" {
			t.Fatalf("poll read history or lost a live event: %+v", s)
		}
	}
	if err := m.Reload(context.Background(), dir, http.NotFoundHandler(), t.Logf); err != nil || m.Status().RequestsToday != 3 || m.Status().LastError != "" {
		t.Fatal("unchanged reload re-read history")
	}
	*now = now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	if s := m.Status(); s.RequestsToday != 0 || s.LastError != "" {
		t.Fatalf("midnight poll did not roll in memory: %+v", s)
	}
	// A completion that started yesterday remains yesterday's request.
	m.Count(usage.Event{TS: now.Add(-time.Second), Via: "bridge"})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 250 {
				m.Count(usage.Event{TS: *now, Via: "bridge"})
				_ = m.Status()
			}
		}()
	}
	wg.Wait()
	if got := m.Status().RequestsToday; got != 1000 {
		t.Fatalf("concurrent record/poll count = %d", got)
	}
	t.Log("1000 status polls with unreadable history: count 3; midnight: 0; 1000 concurrent increments: 1000")
}

func TestCounterSeedFailureServesAndReloadRecoversWithoutDoubleCount(t *testing.T) {
	m, dir, now := counterFixture(t)
	c, connections := bridgeServer(t)
	if err := save(dir, c, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, usage.FileName)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	logf := func(format string, args ...any) { fmt.Fprintf(&logs, format, args...) }
	reload := func() {
		t.Helper()
		if err := m.Reload(context.Background(), dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "served") }), logf); err != nil {
			t.Fatalf("display seed failure blocked startup: %v", err)
		}
	}
	reload()
	if s := m.Status(); s.RequestsToday != 0 || !strings.Contains(s.LastError, "counter incomplete") || !strings.Contains(logs.String(), "WARNING") {
		t.Fatalf("seed failure was silent: %+v, %s", s, logs.String())
	}
	conn := connect(t, connections)
	if head, body := request(t, conn, "seed-error", "friend", nil); head.Status != 200 || string(body) != "served" {
		t.Fatal("seed warning prevented bridge request handling")
	}
	*now = now.Add(time.Minute)
	live := usage.Event{TS: *now, Via: "bridge"}
	m.Count(live)
	m.Count(usage.Event{TS: now.Add(time.Second), Via: "bridge"}) // Not yet flushed to disk.
	m.state.connection(true, "")
	if !strings.Contains(m.Status().LastError, "counter incomplete") {
		t.Fatal("reconnect cleared the seed warning")
	}
	reload()
	if got := m.Status().RequestsToday; got != 2 {
		t.Fatalf("failed recovery reset live count to %d", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeCounterHistory(t, dir, usage.Event{TS: now.Add(-time.Hour), Via: "bridge"}, live)
	reload()
	if s := m.Status(); s.LastError != "" || s.RequestsToday != 3 {
		t.Fatalf("recovery lost/double-counted live events: %+v", s)
	}
	reload()
	if m.Status().RequestsToday != 3 {
		t.Fatal("successful reload re-seeded again")
	}
	t.Log("seed failure: reload succeeds, warning visible, live count retained; recovery: 1 historical + 2 live = 3")
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

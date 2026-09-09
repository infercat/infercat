package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

func TestStatusReportsPerKeySessionCounts(t *testing.T) {
	ctx := context.Background()
	store, err := keys.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	active, _, err := store.Add(ctx, "connected", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	idle, _, err := store.Add(ctx, "idle", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"m"}]}`)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	up, err := upstream.Open(ctx, engine.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	gw := newFakeGateway()
	gw.sessions = map[string]int{active.ID: 2}
	st := buildStatus(ctx, time.Now(), fakeTunnel{}, up, gw, store, &telemetry{events: admin.NewEvents(nil)})
	raw, err := json.Marshal(st.Keys)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("keys: %s", raw)
	}
	for _, row := range rows {
		wantConnected, wantSessions := row["id"] == active.ID, float64(0)
		if wantConnected {
			wantSessions = 2
		} else if row["id"] != idle.ID {
			t.Fatalf("unexpected key: %v", row["id"])
		}
		if row["connected"] != wantConnected || row["sessions"] != wantSessions {
			t.Fatalf("status fields: %s", raw)
		}
	}
}

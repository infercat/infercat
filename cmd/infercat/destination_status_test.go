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
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

func TestStatusContainsDestinationInventory(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"m"}]}`)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	ctx := context.Background()
	text, err := upstream.Open(ctx, engine.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := upstream.OpenAudio(ctx, engine.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := keys.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gw := gateway.New(gateway.Config{Transcribe: audio}, text, store, nil, nil)
	st := buildStatus(ctx, time.Now(), fakeTunnel{}, text, gw, store, &telemetry{events: admin.NewEvents(nil)})
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Destinations []gateway.DestinationStatus `json:"destinations"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Destinations) != 2 {
		t.Fatalf("status: %s", raw)
	}
	for i, id := range []string{"text", "transcribe"} {
		d := decoded.Destinations[i]
		if d.ID != id || d.Kind != "engine" || d.Slots != 1 || d.InFlight != 0 || d.Waiting != 0 || len(d.Models) != 1 || d.Models[0] != "m" {
			t.Fatalf("destination %+v", d)
		}
	}
	if st.Queue.InFlight != 0 || st.Queue.Waiting != 0 || st.Upstream.Slots != 1 {
		t.Fatalf("legacy fields: %+v", st)
	}
}

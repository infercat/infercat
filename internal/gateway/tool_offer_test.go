package gateway

import (
	"context"
	"encoding/json"
	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
	"os"
	"testing"
	"time"
)

func TestChatImageToolOffer(t *testing.T) {
	h := hostChatHarness(t, nil, nil)
	yes := true
	h.up.setInfo(func(i *upstream.Info) { i.Vision = map[string]*bool{"m1": &yes}; i.ModelContext = 8192 })
	h.gw.router.routes[string(embeddingsEndpoint)] = &Destination{ID: "embed", Up: h.up}
	if err := h.gw.runs.Register("agent", func(context.Context, runstate.Run) (runstate.Decision, error) { return runstate.Decision{}, nil }, runstate.Policy{}); err != nil {
		t.Fatal(err)
	}
	rows := submittedImages(t, h.do("POST", "/v1/images/jobs", "Bearer "+testSecret, `{"prompts":["fixture image"]}`))
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, _ := h.gw.runs.Store.Get(h.key.ID, rows[0].ID)
		if r.State == runstate.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("image did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	read := func() map[string]any {
		t.Helper()
		response := h.do("GET", "/me", "Bearer "+testSecret, "")
		if response.status != 200 {
			t.Fatalf("me: %d %s", response.status, response.body)
		}
		var m map[string]any
		if err := json.Unmarshal(response.body, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := read()
	if got, _ := json.Marshal(m["host_tools"]); string(got) != `["make_image"]` {
		t.Fatalf("offer: %s", got)
	}
	if path := os.Getenv("INFERCAT_146_ME_CAPTURE"); path != "" {
		m["key"].(map[string]any)["id"] = "fixture-key"
		raw, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m1"} })
	if _, ok := read()["host_tools"]; ok {
		t.Fatal("unshared image tool offered")
	}
	h.setKey(func(k *keys.Key) { k.Limits.Models = nil })
	h.gw.runs = nil
	if _, ok := read()["host_tools"]; ok {
		t.Fatal("tool offered without chat registry")
	}
}

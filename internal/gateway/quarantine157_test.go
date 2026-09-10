package gateway

import (
	"context"
	"encoding/json"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/usage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test157RuntimeQuarantineIsRetryable503(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, runError(runstate.ErrQuarantined))
	if w.Code != 503 || w.Header().Get("Retry-After") != "10" || !strings.Contains(w.Body.String(), `"code":"upstream_down"`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}

func Test157RuntimeStoppingIsDistinct(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, runError(runstate.ErrStopping))
	if w.Code != 503 || w.Header().Get("Retry-After") != "10" || !strings.Contains(w.Body.String(), "runtime stopping; retry shortly") || strings.Contains(w.Body.String(), "quarantined") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func Test157ModelIdentityRefusedBeforeDispatch(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	body, _ := json.Marshal(map[string]any{"model": strings.Repeat("m", usage.MaxModelBytes+1), "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	response := h.post("/v1/chat/completions", string(body))
	h.expectErr(response, CodeInvalidRequest)
	event := h.rec.waitFor(t, 1)[0]
	if event.Model != "" || event.PromptTokens != 0 || event.CompletionTokens != 0 {
		t.Fatal("overlong identity was charged or recorded", event)
	}
	if _, err := h.gw.router.Resolve(h.key, string(chatEndpoint), strings.Repeat("m", usage.MaxModelBytes)); err != nil {
		t.Fatal("boundary model refused", err)
	}
}

func Test157CommittedTerminalHTTPResponse(t *testing.T) {
	dir := t.TempDir()
	store, err := runstate.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, Config{}, nil)
	manager, err := runstate.New(store, nil, map[string]runstate.Kind{"test": func(context.Context, runstate.Run) (runstate.Decision, error) {
		t.Error("refused submission dispatched")
		return runstate.Decision{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	filler, err := store.Create(h.key.ID, "test", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Create(h.key.ID, "test", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runs", h.key.ID, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]json.RawMessage
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	var rows map[string]runstate.Run
	json.Unmarshal(snapshot["runs"], &rows)
	f := rows[filler.ID]
	f.State = runstate.Done
	f.Input = json.RawMessage(`""`)
	rows[f.ID] = f
	snapshot["exception_bytes"], _ = json.Marshal(map[string]int{target.ID: 4096})
	snapshot["runs"], _ = json.Marshal(rows)
	raw, _ = json.Marshal(snapshot)
	f.Input = json.RawMessage(`"` + strings.Repeat("x", runstate.MaxStored-runstate.MaxLiveKey*4096-len(raw)) + `"`)
	rows[f.ID] = f
	snapshot["runs"], _ = json.Marshal(rows)
	raw, _ = json.Marshal(snapshot)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Reopen the crafted legacy snapshot without re-running startup recovery.
	manager.Store, err = runstate.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.gw.SetRuns(manager); err != nil {
		t.Fatal(err)
	}
	response := h.do(http.MethodDelete, "/v1/runs/"+target.ID, "bearer", "")
	var got runstate.Run
	if response.status != 200 || json.Unmarshal(response.body, &got) != nil || got.ID != target.ID || got.State != runstate.Failed || got.Reason != "storage exhausted" {
		t.Fatal(response.status, string(response.body))
	}
	body, _ := json.Marshal(map[string]any{"kind": "test", "input": strings.Repeat("y", 8192)})
	refused := h.post("/v1/runs", string(body))
	h.expectErr(refused, CodeConcurrencyLimited)
	if refused.status != 429 {
		t.Fatal(refused.status)
	}
	after, err := manager.Store.List(h.key.ID)
	if err != nil || len(after) != 2 {
		t.Fatal("refusal mutated runs", len(after), err)
	}
}

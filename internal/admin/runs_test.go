package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runstate "github.com/infercat/infercat/internal/run"
)

func TestRunsListingRemainsAuthenticatedAndMetadataOnly(t *testing.T) {
	dir := shortDir(t)
	store, e := runstate.NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	made, e := store.Create("k_a", "test", "interactive", json.RawMessage(`{"input":"PRIVATE PAYLOAD"}`))
	if e != nil {
		t.Fatal(e)
	}
	server, e := Serve(dir, sample, func() error { return nil }, nil, WithRuns(http.NotFoundHandler(), store.List))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	request := httptest.NewRequest("GET", "/runs?key_id=k_a", nil)
	denied := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(denied, request)
	if denied.Code != 401 {
		t.Fatal(denied.Code)
	}
	token, e := os.ReadFile(filepath.Join(dir, TokenName))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	out := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(out, request)
	if out.Code != 200 || !strings.Contains(out.Body.String(), made.ID) || strings.Contains(out.Body.String(), "PRIVATE") {
		t.Fatal(out.Code, out.Body.String())
	}
	if out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(out.Header())
	}
}
func TestRunsListingBoundsAllKeys(t *testing.T) {
	handler := WithRuns(http.NotFoundHandler(), func(string) ([]runstate.Summary, error) { return make([]runstate.Summary, 101), nil })
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest("GET", "/runs", nil))
	var result struct {
		Runs      []runstate.Summary
		Truncated bool
	}
	if e := json.Unmarshal(out.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Runs) != 100 || !result.Truncated {
		t.Fatal(len(result.Runs), result.Truncated)
	}
}

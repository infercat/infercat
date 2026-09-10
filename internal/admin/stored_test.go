package admin

import (
	runstate "github.com/infercat/infercat/internal/run"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoredRequiresOneKeyAndNeverReplays(t *testing.T) {
	writes := 0
	reads := 0
	handler := WithStored(http.NotFoundHandler(), func(key string) (runstate.StoredData, error) { reads++; return runstate.StoredData{KeyID: key}, nil }, func(key string, expected runstate.ClearExpectation) (runstate.ClearResult, error) {
		writes++
		return runstate.ClearResult{}, nil
	})
	for _, path := range []string{"/stored", "/stored?key_id=../bad", "/stored?key_id="} {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest("DELETE", path, nil))
		if out.Code != 400 {
			t.Fatal(out.Code)
		}
	}
	if writes != 0 || reads != 0 {
		t.Fatal("invalid scope reached store")
	}
	for _, method := range []string{"GET", "DELETE"} {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(method, "/stored?key_id=k_a", strings.NewReader(`{"cursor":"e_test:0","terminal":0}`)))
		if out.Code != 200 || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(out.Code)
		}
	}
	if writes != 1 || reads != 2 {
		t.Fatal(writes, reads)
	}
}

func TestStoredRoutesRemainBehindAdminToken(t *testing.T) {
	dir := shortDir(t)
	calls := 0
	handler := WithStored(http.NotFoundHandler(), func(key string) (runstate.StoredData, error) { calls++; return runstate.StoredData{KeyID: key}, nil }, func(key string, expected runstate.ClearExpectation) (runstate.ClearResult, error) {
		calls++
		return runstate.ClearResult{}, nil
	})
	server, err := Serve(dir, sample, func() error { return nil }, nil, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	token, err := os.ReadFile(filepath.Join(dir, TokenName))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "DELETE"} {
		req := httptest.NewRequest(method, "/stored?key_id=k_a", strings.NewReader(`{"cursor":"e_test:0","terminal":0}`))
		out := httptest.NewRecorder()
		server.srv.Handler.ServeHTTP(out, req)
		if out.Code != 401 || calls != 0 {
			t.Fatal(out.Code, calls)
		}
	}
	req := httptest.NewRequest("DELETE", "/stored?key_id=k_a", strings.NewReader(`{"cursor":"e_test:0","terminal":0}`))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	out := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(out, req)
	if out.Code != 200 || calls != 2 {
		t.Fatal(out.Code, calls)
	}
}

func TestStoredClearConflictAndMissingConfirmation(t *testing.T) {
	calls := 0
	h := WithStored(http.NotFoundHandler(), func(string) (runstate.StoredData, error) {
		t.Fatal("read after rejected clear")
		return runstate.StoredData{}, nil
	}, func(_ string, e runstate.ClearExpectation) (runstate.ClearResult, error) {
		calls++
		if e.Cursor != "e_old:1" || e.Terminal != 2 || e.Images != 1 || e.Cleanup != 3 {
			t.Fatal(e)
		}
		return runstate.ClearResult{}, runstate.ErrConflict
	})
	for _, body := range []string{"", `{}`, `{"cursor":"e_old:1","terminal":2,"images":1,"cleanup":3}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("DELETE", "/stored?key_id=k_a", strings.NewReader(body)))
		want := 400
		if strings.Contains(body, "e_old") {
			want = 409
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestStoredClearSuccessSurvivesPostClearReadFailure(t *testing.T) {
	h := WithStored(http.NotFoundHandler(), func(string) (runstate.StoredData, error) { return runstate.StoredData{}, os.ErrPermission }, func(string, runstate.ClearExpectation) (runstate.ClearResult, error) {
		return runstate.ClearResult{Cleared: 2, Warning: "Stored data cleared; cleanup step failed: permission denied"}, nil
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/stored?key_id=k_a", strings.NewReader(`{"cursor":"e_test:1","terminal":2}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cleared":2`) || !strings.Contains(w.Body.String(), "cleanup step failed") {
		t.Fatal(w.Code, w.Body.String())
	}
}

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/usage"
)

func TestMachineRefusalsDoNotMutateOrPrompt(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	before, err := os.ReadFile(filepath.Join(f.dir, keys.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"keys", "revoke", f.id, "--json"}, {"keys", "add", "--json", "--", "-bad"}, {"keys", "add", "bad", "--json"}, {"keys", "limits", f.id, "--agent=maybe", "--json"}} {
		code, _, raw := machineCall(t, context.Background(), append([]string{"--data-dir", f.dir}, args...)...)
		if code != 2 {
			t.Fatal(code, raw)
		}
		after, err := os.ReadFile(filepath.Join(f.dir, keys.FileName))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("refusal wrote keys", err)
		}
	}
}

func TestMachineMissingAdminCredentialNeverFallsBackToMint(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	before, err := os.ReadFile(filepath.Join(f.dir, keys.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, admin.TokenName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	code, _, raw := machineCall(t, context.Background(), "--data-dir", f.dir, "keys", "add", "--json", "--", "bob")
	after, err := os.ReadFile(filepath.Join(f.dir, keys.FileName))
	if code != 1 || err != nil || !bytes.Equal(before, after) {
		t.Fatal(code, raw, err)
	}
}

func TestMachineAndHumanNeverRepeatAnAmbiguousMint(t *testing.T) {
	t.Parallel()
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "machine"}[jsonMode], func(t *testing.T) {
			dir, err := os.MkdirTemp("", "ic195-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			store, err := keys.NewFileStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server, err := admin.Serve(dir, func() admin.Status { return admin.Status{} }, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/keys" {
					t.Error("unexpected route", r.Method, r.URL.Path)
				}
				calls.Add(1)
				if _, _, err := store.Add(r.Context(), "once", keys.Limits{}); err != nil {
					t.Error(err)
				}
				io.WriteString(w, "{")
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			args := []string{"--data-dir", dir, "keys", "add", "once"}
			if jsonMode {
				args = []string{"--data-dir", dir, "keys", "add", "--json", "--", "once"}
			}
			plat := newPlatform()
			plat.savedAddr = func(string) (string, error) { t.Error("fell back to saved identity after dispatch"); return "", nil }
			var out, errw bytes.Buffer
			code := run(context.Background(), args, &out, &errw, forbiddenInput{}, false, plat)
			list, err := store.List(context.Background())
			if code != 1 || calls.Load() != 1 || err != nil || len(list) != 1 {
				t.Fatal(code, calls.Load(), len(list), err)
			}
		})
	}
}

func TestHumanUsageKeepsRollingFilterThroughHost(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	rec, err := usage.NewFileRecorder(f.dir, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, event := range []usage.Event{
		{TS: now.Add(-time.Hour), KeyID: f.id, Endpoint: "/v1/chat/completions", Status: 200, PromptTokens: 3},
		{TS: now.Add(-48 * time.Hour), KeyID: f.id, Endpoint: "/v1/chat/completions", Status: 200, PromptTokens: 7},
		{TS: now.Add(-time.Hour), KeyID: "k_other", Endpoint: "/v1/chat/completions", Status: 200, PromptTokens: 11},
	} {
		rec.Record(context.Background(), event)
	}
	rec.Close()
	for _, tc := range []struct{ since, want string }{{"24h", "requests  1 model call"}, {"all", "requests  2 model calls"}} {
		result := exec(t, newPlatform(), "--data-dir", f.dir, "usage", "--key", "alice", "--since", tc.since)
		if result.code != 0 || !strings.Contains(result.out, tc.want) {
			t.Fatal(result)
		}
	}
	// The remote route still refuses the newly added since input.
	r := httptest.NewRequest("GET", "/usage?since=all", nil)
	r.Header.Set("X-Infercat-Remote", "true")
	out := httptest.NewRecorder()
	f.api.ServeHTTP(out, r)
	if out.Code != 400 {
		t.Fatal(out.Code, out.Body.String())
	}
}

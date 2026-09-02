package keys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s, dir
}

func TestAddLookupAndDefaults(t *testing.T) {
	ctx := context.Background()
	s, dir := newStore(t)

	k, secret, err := s.Add(ctx, "alice", Limits{RPM: 30})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !strings.HasPrefix(k.ID, "k_") || len(k.ID) != 8 {
		t.Errorf("id %q: want k_ + 6 hex", k.ID)
	}
	if len(secret) != 43 || strings.Contains(secret, ".") {
		t.Errorf("secret %q: want 43 base64url chars and no dot", secret)
	}
	if k.SecretHash != HashSecret(secret) {
		t.Errorf("stored hash does not match the returned secret")
	}
	if strings.Contains(string(mustRead(t, filepath.Join(dir, FileName))), secret) {
		t.Fatal("the plaintext secret reached keys.json")
	}
	want := DefaultLimits()
	want.RPM = 30
	if !reflect.DeepEqual(k.Limits, want) {
		t.Errorf("limits = %+v, want %+v (explicit rpm kept, the rest defaulted)", k.Limits, want)
	}

	got, ok, err := s.Lookup(ctx, secret)
	if err != nil || !ok {
		t.Fatalf("Lookup(correct) = ok %v err %v", ok, err)
	}
	if got.ID != k.ID {
		t.Errorf("Lookup returned %s, want %s", got.ID, k.ID)
	}
	if _, ok, _ := s.Lookup(ctx, secret+"x"); ok {
		t.Error("Lookup accepted a wrong secret")
	}
	if _, ok, _ := s.Lookup(ctx, ""); ok {
		t.Error("Lookup accepted an empty secret")
	}
}

// Two keys must both resolve: the constant-time loop visits every key rather than stopping early.
func TestLookupAcrossManyKeys(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	secrets := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d"} {
		k, sec, err := s.Add(ctx, n, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		secrets[k.ID] = sec
	}
	for id, sec := range secrets {
		k, ok, err := s.Lookup(ctx, sec)
		if err != nil || !ok || k.ID != id {
			t.Fatalf("Lookup(%s) = %v %v %v", id, k, ok, err)
		}
	}
}

func TestRotateKeepsIdentityAndRetiresTheOldSecret(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	k, old, err := s.Add(ctx, "alice", Limits{RPM: 99, Models: []string{"m1"}})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.Rotate(ctx, k.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if fresh == old {
		t.Fatal("Rotate returned the same secret")
	}
	if _, ok, _ := s.Lookup(ctx, old); ok {
		t.Error("the retired secret still works")
	}
	got, ok, err := s.Lookup(ctx, fresh)
	if err != nil || !ok {
		t.Fatalf("Lookup(new) = %v %v", ok, err)
	}
	if got.ID != k.ID || got.Name != k.Name || got.Limits.RPM != 99 || len(got.Limits.Models) != 1 {
		t.Errorf("rotate changed identity or limits: %+v", got)
	}
	if !got.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("rotate changed created_at")
	}
}

func TestStatusAndLimits(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	k, _, _ := s.Add(ctx, "alice", Limits{})
	if err := s.SetStatus(ctx, k.ID, Paused); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Find(ctx, k.ID); got.Status != Paused {
		t.Errorf("status = %v, want paused", got.Status)
	}
	if err := s.SetStatus(ctx, k.ID, Status("bogus")); err == nil {
		t.Error("SetStatus accepted an unknown status")
	}
	// SetLimits is verbatim: a zero here is the host saying "no limit", not "use the default".
	if err := s.SetLimits(ctx, k.ID, Limits{RPM: 0, TPM: 5}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Find(ctx, k.ID)
	if got.Limits.RPM != 0 || got.Limits.TPM != 5 {
		t.Errorf("limits = %+v, want rpm 0 kept", got.Limits)
	}
}

func TestFindByIDAndUniqueName(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	k, _, _ := s.Add(ctx, "alice", Limits{})
	if got, err := s.Find(ctx, "alice"); err != nil || got.ID != k.ID {
		t.Errorf("Find by name = %v %v", got, err)
	}
	if _, err := s.Find(ctx, "nobody"); err == nil {
		t.Error("Find accepted an unknown ref")
	}
	if _, _, err := s.Add(ctx, "alice", Limits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Find(ctx, "alice"); err == nil || !strings.Contains(err.Error(), "use the key id") {
		t.Errorf("ambiguous name error = %v", err)
	}
}

// A gateway in another process must see `keys add` without a restart, but must not stat the file
// more than once a second.
func TestHotReload(t *testing.T) {
	ctx := context.Background()
	s, dir := newStore(t)
	writer, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	s.now = func() time.Time { return clock }

	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatalf("fresh store has %d keys", len(list))
	}
	if _, _, err := writer.Add(ctx, "alice", Limits{}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("re-read within one second; want the throttle to hold the old view")
	}
	clock = clock.Add(2 * time.Second)
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "alice" {
		t.Fatalf("after the throttle expired: %v", list)
	}

	// An edit made by hand (not through the store) is picked up too.
	clock = clock.Add(2 * time.Second)
	raw := mustRead(t, filepath.Join(dir, FileName))
	var doc fileDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Keys[0].Status = Revoked
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx); len(list) != 1 || list[0].Status != Revoked {
		t.Errorf("hand edit not seen: %v", list)
	}
}

func TestWritesAreAtomicAndLeaveNoTemps(t *testing.T) {
	ctx := context.Background()
	s, dir := newStore(t)
	for i := 0; i < 5; i++ {
		if _, _, err := s.Add(ctx, "k", Limits{}); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != FileName {
			t.Errorf("leftover file %q in the data dir", e.Name())
		}
	}
	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("keys.json mode = %v, want 0600 (it holds secret hashes)", fi.Mode().Perm())
	}
	var doc fileDoc
	if err := json.Unmarshal(mustRead(t, filepath.Join(dir, FileName)), &doc); err != nil {
		t.Fatalf("keys.json is not valid JSON: %v", err)
	}
	if doc.Version != FileVersion || len(doc.Keys) != 5 {
		t.Errorf("doc = version %d, %d keys", doc.Version, len(doc.Keys))
	}
}

// A write that cannot land must leave the in-memory store exactly as it was: refusal never mutates.
func TestRefusedWriteDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	s, dir := newStore(t)
	k, _, err := s.Add(ctx, "alice", Limits{RPM: 7})
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, _, err := s.Add(ctx, "bob", Limits{}); err == nil {
		t.Fatal("Add succeeded with an unwritable data dir")
	}
	if err := s.SetLimits(ctx, k.ID, Limits{RPM: 999}); err == nil {
		t.Fatal("SetLimits succeeded with an unwritable data dir")
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Limits.RPM != 7 {
		t.Errorf("refused writes changed the store: %+v", list)
	}
}

func TestUnknownFileVersionIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version":99,"keys":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(dir); err == nil || !strings.Contains(err.Error(), "not understood") {
		t.Errorf("err = %v, want a version complaint", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	_, secret, err := s.Add(ctx, "alice", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, ok, err := s.Lookup(ctx, secret); err != nil || !ok {
					t.Errorf("Lookup: %v %v", ok, err)
					return
				}
				if i%2 == 0 {
					if _, _, err := s.Add(ctx, "x", Limits{}); err != nil {
						t.Errorf("Add: %v", err)
						return
					}
				}
				if _, err := s.List(ctx); err != nil {
					t.Errorf("List: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestAddRejectsAnEmptyName(t *testing.T) {
	if _, _, err := mustStore(t).Add(context.Background(), "  ", Limits{}); err == nil {
		t.Error("Add accepted an empty name")
	}
}

func mustStore(t *testing.T) *FileStore { s, _ := newStore(t); return s }

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

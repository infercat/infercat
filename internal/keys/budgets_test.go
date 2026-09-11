package keys

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLegacyKeyBudgetsDoNotRewriteFile(t *testing.T) {
	// 0.1.1's v1 schema, before the audio limits; keep all non-resource policy too.
	raw := []byte(`{"version":1,"keys":[{"id":"k_legacy","name":"friend","secret_hash":"sha256:fixture","status":"active","created_at":"2026-09-01T00:00:00Z","limits":{"rpm":19,"tpm":1234,"daily_tokens":5678,"max_concurrent":2,"max_output_tokens":999,"max_context":8192,"models":["m"]}}]}`)
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("keys: %v %v", list, err)
	}
	l := list[0].Limits
	want := Budgets{"search": {{"requests", "day", 50}}, "images": {{"images", "day", 20}}, "tokens": {{"tokens", "minute", 1234}, {"tokens", "day", 5678}}, "audio": {{"seconds", "day", 3600}}, "speech": {{"characters", "day", 200000}}}
	if !reflect.DeepEqual(l.Budgets(), want) {
		t.Fatalf("budgets: %+v", l.Budgets())
	}
	if l.RPM != 19 || l.MaxConcurrent != 2 || l.MaxOutputTokens != 999 || l.MaxContext != 8192 || !reflect.DeepEqual(l.Models, []string{"m"}) {
		t.Fatalf("policy: %+v", l)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("read rewrote the legacy key file")
	}
	l.TPM = 0
	if l.Budgets().Amount("tokens", "minute") != 0 || l.Budgets().Amount("tokens", "day") != 5678 {
		t.Fatal("unlimited minute or day window changed")
	}
}

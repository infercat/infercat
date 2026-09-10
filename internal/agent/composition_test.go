package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedCompositionDisablesBothOwnersWithoutLiveInstall(t *testing.T) {
	if !Supported() {
		t.Skip("Unix runtime only")
	}
	hash := sha256.Sum256(baseComposition)
	if hex.EncodeToString(hash[:]) != "885d9766775a2585f8c3a608cd2d2c97391e158b3ac1c53365cb2d1ca82040db" {
		t.Fatal("vendored manifest changed")
	}
	dir := t.TempDir()
	root := runtimeDir(dir)
	for name, raw := range map[string][]byte{"installed": []byte(LockSHA256), "node/bin/node": {}, "node_modules/@deepseek-ai/dsh/lib/bin.js": {}, "node_modules/@deepseek-ai/dsh-base/cordis.patch.yml": baseComposition} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	options, err := HarnessOptions(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(options.Dir, "adapter.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID       string
		Disabled bool
	}
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("bad composed config")
	}
	for _, id := range []string{"session-persistence-jsonl", "session-checkpoint-policy"} {
		if !bytes.Contains(baseComposition, []byte("id: "+id+"\n")) {
			t.Fatal("disabled id absent from pinned manifest", id)
		}
		count := 0
		for _, r := range rows {
			if r.ID == id && r.Disabled {
				count++
			}
		}
		if count != 1 {
			t.Fatal("owner not disabled exactly once", id, count)
		}
	}
	if err = os.Remove(filepath.Join(root, "node_modules/@deepseek-ai/dsh-base/cordis.patch.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err = HarnessOptions(dir); err == nil {
		t.Fatal("missing manifest accepted")
	}
	if err = os.WriteFile(filepath.Join(root, "node_modules/@deepseek-ai/dsh-base/cordis.patch.yml"), []byte("renamed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = HarnessOptions(dir); err == nil {
		t.Fatal("changed manifest accepted")
	}
}

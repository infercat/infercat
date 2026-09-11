package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	for _, id := range []string{"session-persistence-jsonl", "session-checkpoint-policy", "session-projection-cache"} {
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
	if bytes.Contains(raw, []byte("infercat-outer-sandbox")) || strings.Contains(strings.Join(options.Env, "\n"), "INFERCAT_CONFINED_WORKSPACE") {
		t.Fatal("unconfined fixture got inherited provider")
	}
	workspace := t.TempDir()
	confined, err := harnessOptions(dir, filepath.Join(workspace, ".runtime"), workspace)
	if err != nil {
		t.Fatal(err)
	}
	composed, err := os.ReadFile(filepath.Join(confined.Dir, "adapter.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(composed, []byte(`"id":"sandbox"`)) || !bytes.Contains(composed, []byte(`"id":"infercat-outer-sandbox"`)) || !strings.Contains(strings.Join(confined.Env, "\n"), "INFERCAT_CONFINED_WORKSPACE="+workspace) {
		t.Fatal("missing confined provider composition")
	}
	got, err := os.ReadFile(filepath.Join(confined.Dir, "inherited-sandbox.mjs"))
	if err != nil || !bytes.Equal(got, inheritedSandbox) {
		t.Fatal("provider not materialized", err)
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

// Explicit pinned suite: authenticate the registry archive, not the installed copy.
func TestPinnedCompositionMatchesLockedTarball(t *testing.T) {
	if os.Getenv("INFERCAT_AGENT_TEST_INSTALL") == "" {
		t.Skip("explicit pinned archive fixture")
	}
	raw, _ := packages.ReadFile("assets/package-lock.json")
	var lock struct {
		Packages map[string]struct{ Resolved, Integrity string }
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	entry := lock.Packages["node_modules/@deepseek-ai/dsh-base"]
	if !strings.HasPrefix(entry.Resolved, "https://registry.npmjs.org/") || !strings.HasPrefix(entry.Integrity, "sha512-") {
		t.Fatal("unexpected lock entry")
	}
	client := http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(entry.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	archive, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil || response.StatusCode != 200 {
		t.Fatal("archive fetch", err, response.StatusCode)
	}
	digest := sha512.Sum512(archive)
	if "sha512-"+base64.StdEncoding.EncodeToString(digest[:]) != entry.Integrity {
		t.Fatal("registry archive integrity mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Name != "package/cordis.patch.yml" {
			continue
		}
		got, e := io.ReadAll(io.LimitReader(tr, 1<<20))
		if e != nil || !bytes.Equal(got, baseComposition) {
			t.Fatal("vendored manifest differs from locked archive", e)
		}
		t.Logf("locked tarball manifest SHA-256 %x", sha256.Sum256(got))
		return
	}
	t.Fatal("manifest absent from locked tarball")
}

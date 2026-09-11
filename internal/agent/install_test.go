package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedLock(t *testing.T) {
	b, err := packages.ReadFile("assets/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != LockSHA256 {
		t.Fatal("pin changed")
	}
	if err := checkLock(b); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"packages":{"node_modules/x":{"resolved":"https://other.test/x","integrity":"sha512-a"}}}`, `{"packages":{"node_modules/x":{"resolved":"https://registry.npmjs.org/x"}}}`} {
		if checkLock([]byte(raw)) == nil {
			t.Fatal("accepted unlocked package")
		}
	}
}
func TestDownloadIntegrity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "fixture archive") }))
	defer srv.Close()
	sum := sha256.Sum256([]byte("fixture archive"))
	want := hex.EncodeToString(sum[:])
	if err := download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "ok"), want); err != nil {
		t.Fatal(err)
	}
	if download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "bad"), "incorrect") == nil {
		t.Fatal("accepted wrong bytes")
	}
}
func TestIncompleteInstallIsNotReady(t *testing.T) {
	if !Supported() {
		t.Skip("Unix runtime only")
	}
	dir := t.TempDir()
	root := runtimeDir(dir)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "installed"), []byte(LockSHA256), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Installed(dir); err == nil {
		t.Fatal("incomplete install reported ready")
	}
}

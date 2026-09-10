package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
func fixtureArchive(t *testing.T, headers []*tar.Header) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "node.tgz")
	if err := os.WriteFile(p, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestExtractNode(t *testing.T) {
	archive := fixtureArchive(t, []*tar.Header{{Name: "node/bin/", Typeflag: tar.TypeDir, Mode: 0755}, {Name: "node/bin/node", Typeflag: tar.TypeReg, Mode: 0755, Size: 4}, {Name: "node/bin/alias", Typeflag: tar.TypeSymlink, Linkname: "node"}})
	root := filepath.Join(t.TempDir(), "node")
	if err := extractNode(archive, root); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "bin/alias")); err != nil || string(b) != "xxxx" {
		t.Fatalf("internal link: %q %v", b, err)
	}
	for _, header := range []*tar.Header{{Name: "node/../escape", Typeflag: tar.TypeReg, Size: 1}, {Name: "node/bin/alias", Typeflag: tar.TypeSymlink, Linkname: "../../../escape"}} {
		if extractNode(fixtureArchive(t, []*tar.Header{header}), filepath.Join(t.TempDir(), "node")) == nil {
			t.Fatal("accepted escaping archive entry")
		}
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

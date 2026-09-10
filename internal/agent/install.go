// Package agent adapts the pinned harness to host-owned runs.
package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const HarnessVersion = "0.1.5-alpha.1"
const HarnessRevision = "5dda764ed3aa172535a7967b06ff95d9cbfe536a"
const NodeVersion = "22.23.2"
const LockSHA256 = "9b053e130bd71c2950eb106f6ca7ad9043368d969532ae233df1c0579b75b90d"

//go:embed assets/package.json assets/package-lock.json
var packages embed.FS

// Official nodejs.org/dist/v22.23.2/SHASUMS256.txt, pinned at integration time.
var nodeHashes = map[string]string{
	"darwin-arm64": "61130f394c1630d211dd50aecc4353d379480f36d3ac913cd85dbba1aed585c6",
	"darwin-x64":   "58e99022c2ff89395576cc7fd4d98cea24bb68081475d5f88b801ee8729fb026",
	"linux-arm64":  "013b59cfd2819703a6f4a14ab891fc46fc2a4e3f5bcd92de3fb4929b43e35b30",
	"linux-x64":    "b294a556e639d64338823920e5866c21c02741742d2e1529ee1a225c1ec9252a",
}

func Supported() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" }
func runtimeDir(dataDir string) string {
	return filepath.Join(dataDir, "agent", "runtime-"+HarnessVersion+"-node"+NodeVersion)
}
func Installed(dataDir string) (string, error) {
	if !Supported() {
		return "", errors.New("agent runtime is supported on Darwin and Linux only")
	}
	dir := runtimeDir(dataDir)
	b, err := os.ReadFile(filepath.Join(dir, "installed"))
	if err != nil || string(b) != LockSHA256 {
		return "", errors.New("agent runtime is not installed; run infercat agent install")
	}
	for _, name := range []string{"node/bin/node", "node_modules/@deepseek-ai/dsh/lib/bin.js"} {
		st, e := os.Stat(filepath.Join(dir, name))
		if e != nil || !st.Mode().IsRegular() {
			return "", errors.New("agent runtime is incomplete; run infercat agent install")
		}
	}
	return dir, nil
}

// Install publishes a verified complete tree atomically. No service or system Node is changed.
func Install(ctx context.Context, dataDir string, out io.Writer) error {
	if !Supported() {
		return errors.New("agent install is supported on Darwin and Linux only")
	}
	if _, err := Installed(dataDir); err == nil {
		return nil
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	platform := runtime.GOOS + "-" + arch
	want, ok := nodeHashes[platform]
	if !ok {
		return fmt.Errorf("agent runtime unavailable for %s", platform)
	}
	lock, _ := packages.ReadFile("assets/package-lock.json")
	digest := sha256.Sum256(lock)
	if hex.EncodeToString(digest[:]) != LockSHA256 {
		return errors.New("embedded harness lock integrity mismatch")
	}
	if err := checkLock(lock); err != nil {
		return err
	}
	parent := filepath.Join(dataDir, "agent")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("agent directory must be a real directory")
	}
	stage, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "node.tgz")
	url := "https://nodejs.org/dist/v" + NodeVersion + "/node-v" + NodeVersion + "-" + platform + ".tar.gz"
	fmt.Fprintln(out, "Downloading pinned Node "+NodeVersion)
	if err = download(ctx, url, archive, want); err != nil {
		return err
	}
	if err = extractNode(archive, filepath.Join(stage, "node")); err != nil {
		return err
	}
	if err = os.Remove(archive); err != nil {
		return err
	}
	for _, name := range []string{"package.json", "package-lock.json"} {
		b, _ := packages.ReadFile("assets/" + name)
		if err = os.WriteFile(filepath.Join(stage, name), b, 0600); err != nil {
			return err
		}
	}
	node := filepath.Join(stage, "node/bin/node")
	cmd := exec.CommandContext(ctx, node, filepath.Join(stage, "node/lib/node_modules/npm/bin/npm-cli.js"), "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = stage
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = []string{"PATH=" + filepath.Join(stage, "node/bin") + ":/usr/bin:/bin", "HOME=" + stage, "npm_config_cache=" + filepath.Join(parent, "npm-cache"), "npm_config_registry=https://registry.npmjs.org/", "npm_config_update_notifier=false"}
	fmt.Fprintln(out, "Installing integrity-locked harness "+HarnessVersion)
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("pinned harness installation: %w", err)
	}
	// The pinned harness's own postinstall only restores its prebuilt helper's executable bit.
	cmd = exec.CommandContext(ctx, node, filepath.Join(stage, "node_modules/@deepseek-ai/dsh-subprocess-local/scripts/ensure-spawn-helper.mjs"))
	cmd.Dir = stage
	cmd.Env = []string{"PATH=" + filepath.Join(stage, "node/bin") + ":/usr/bin:/bin", "HOME=" + stage}
	cmd.Stdout, cmd.Stderr = out, out
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("pinned spawn helper setup: %w", err)
	}
	if err = os.WriteFile(filepath.Join(stage, "installed"), []byte(LockSHA256), 0600); err != nil {
		return err
	}
	if err = os.Rename(stage, runtimeDir(dataDir)); err != nil {
		if _, ready := Installed(dataDir); ready == nil {
			return nil
		}
		return err
	}
	return nil
}
func checkLock(raw []byte) error {
	var lock struct {
		Packages map[string]struct{ Resolved, Integrity string }
	}
	if json.Unmarshal(raw, &lock) != nil || len(lock.Packages) == 0 {
		return errors.New("invalid harness lock")
	}
	for name, p := range lock.Packages {
		if name == "" {
			continue
		}
		if !strings.HasPrefix(p.Resolved, "https://registry.npmjs.org/") || !strings.HasPrefix(p.Integrity, "sha512-") {
			return fmt.Errorf("unverified package in harness lock: %s", name)
		}
	}
	return nil
}
func download(ctx context.Context, url, path, want string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("runtime download: HTTP %d", res.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, 128<<20+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > 128<<20 || hex.EncodeToString(h.Sum(nil)) != want {
		return errors.New("runtime download integrity mismatch")
	}
	return nil
}
func extractNode(archive, root string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	var links [][2]string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok || rel == "" {
			continue
		}
		rel = filepath.Clean(rel)
		if !filepath.IsLocal(rel) {
			return errors.New("runtime archive path escapes destination")
		}
		path := filepath.Join(root, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path, 0700)
		case tar.TypeReg:
			if h.Size < 0 || h.Size > (512<<20)-total {
				return errors.New("runtime archive exceeds size bound")
			}
			total += h.Size
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			var out *os.File
			out, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0755)
			if err == nil {
				_, err = io.CopyN(out, tr, h.Size)
				e := out.Close()
				if err == nil {
					err = e
				}
			}
		case tar.TypeSymlink:
			target := filepath.Clean(filepath.Join(filepath.Dir(rel), h.Linkname))
			if filepath.IsAbs(h.Linkname) || !filepath.IsLocal(target) {
				return errors.New("runtime archive symlink escapes destination")
			}
			links = append(links, [2]string{path, h.Linkname})
		default:
			return errors.New("unsupported runtime archive entry")
		}
		if err != nil {
			return err
		}
	}
	// Publish links last, so archive entries cannot traverse an earlier symlink.
	for _, link := range links {
		if err = os.Symlink(link[1], link[0]); err != nil {
			return err
		}
	}
	return nil
}

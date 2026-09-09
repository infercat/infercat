//go:build !windows

package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxSockPath is the shortest sun_path limit across the unixes we ship to (darwin 104,
// linux 108), minus the NUL terminator.
const maxSockPath = 103

// listen binds admin.sock at mode 0600. Only the host's own user can read the status; there is
// a per-run token also protects browser access through the console listener.
func listen(dataDir string) (net.Listener, string, func(), error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, "", nil, err
	}
	path := filepath.Join(dataDir, SockName)
	// sockaddr_un.sun_path is 104 bytes on darwin and 108 on linux; over that the kernel
	// answers EINVAL, which reads as nonsense. Say what is actually wrong.
	if len(path) > maxSockPath {
		return nil, "", nil, fmt.Errorf("data dir path is too long for a unix socket (%d bytes; the limit is %d for %s) — pass a shorter --data-dir", len(path), maxSockPath, path)
	}
	if _, err := os.Stat(path); err == nil {
		// A socket left by a killed host: if nothing answers, it is stale and safe to replace.
		if c, derr := net.DialTimeout("unix", path, 300*time.Millisecond); derr == nil {
			c.Close()
			return nil, "", nil, errors.New("another host is already serving this data dir (" + path + ")")
		}
		os.Remove(path)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, "", nil, err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		l.Close()
		os.Remove(path)
		return nil, "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	tokenPath := filepath.Join(dataDir, TokenName)
	// Replace stale tokens instead of following a pre-existing symlink or retaining its mode.
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		l.Close()
		os.Remove(path)
		return nil, "", nil, err
	}
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		l.Close()
		os.Remove(path)
		return nil, "", nil, err
	}
	return l, token, func() { os.Remove(path); os.Remove(tokenPath) }, nil
}

func dial(dataDir string) (*http.Client, string, string, error) {
	path := filepath.Join(dataDir, SockName)
	if _, err := os.Stat(path); err != nil {
		return nil, "", "", ErrNoDaemon
	}
	// One connection per call, closed with it: a long-lived caller such as `status --watch` makes
	// a client per poll, and a kept-alive socket would sit on the host as one goroutine per poll.
	hc := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}},
	}
	token, err := os.ReadFile(filepath.Join(dataDir, TokenName))
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return nil, "", "", ErrNoDaemon
	}
	return hc, "http://admin", strings.TrimSpace(string(token)), nil
}

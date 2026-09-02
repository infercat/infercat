//go:build !windows

package admin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// maxSockPath is the shortest sun_path limit across the unixes we ship to (darwin 104,
// linux 108), minus the NUL terminator.
const maxSockPath = 103

// listen binds admin.sock at mode 0600. Only the host's own user can read the status; there is
// no token because the filesystem is the permission check.
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
	return l, "", func() { os.Remove(path) }, nil
}

func dial(dataDir string) (*http.Client, string, string, error) {
	path := filepath.Join(dataDir, SockName)
	if _, err := os.Stat(path); err != nil {
		return nil, "", "", ErrNoDaemon
	}
	hc := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}},
	}
	return hc, "http://admin", "", nil
}

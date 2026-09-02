//go:build windows

package admin

import (
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Windows has no usable unix-socket file permissions, so the admin API falls back to a loopback
// listener on an ephemeral port. The port goes in admin.port and a 32-byte random bearer token in
// admin.token, both 0600; without the token the endpoint answers 401. Loopback only, never the
// tunnel (pm/BELIEFS.md Protection 1).
func listen(dataDir string) (net.Listener, string, func(), error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, "", nil, err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}
	portPath := filepath.Join(dataDir, PortName)
	tokenPath := filepath.Join(dataDir, TokenName)
	_, port, _ := net.SplitHostPort(l.Addr().String())
	if err := os.WriteFile(portPath, []byte(port+"\n"), 0o600); err != nil {
		l.Close()
		return nil, "", nil, err
	}
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		l.Close()
		os.Remove(portPath)
		return nil, "", nil, err
	}
	return l, token, func() { os.Remove(portPath); os.Remove(tokenPath) }, nil
}

func dial(dataDir string) (*http.Client, string, string, error) {
	port, err := os.ReadFile(filepath.Join(dataDir, PortName))
	if err != nil {
		return nil, "", "", ErrNoDaemon
	}
	token, err := os.ReadFile(filepath.Join(dataDir, TokenName))
	if err != nil {
		return nil, "", "", ErrNoDaemon
	}
	hc := &http.Client{Timeout: 5 * time.Second}
	return hc, "http://127.0.0.1:" + strings.TrimSpace(string(port)), strings.TrimSpace(string(token)), nil
}

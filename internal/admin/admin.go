// Package admin is the host's own read-only window on a running daemon: a tiny HTTP server on a
// unix socket in the data dir (a loopback port on Windows), and the client the `status`
// subcommand uses. It is never exposed through the tunnel (pm/BELIEFS.md Protection 1).
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/2185Lab/bunny-network/internal/product"
)

// File names inside the data dir.
const (
	SockName  = "admin.sock"  // unix
	PortName  = "admin.port"  // windows fallback
	TokenName = "admin.token" // windows fallback
)

// ErrNoDaemon means nothing is listening: `serve` is not running for this data dir.
var ErrNoDaemon = errors.New("no running host found for this data dir")

// Status is the admin API's single response (docs/ARCHITECTURE.md §Admin API).
type Status struct {
	Product  string   `json:"product"`
	Version  string   `json:"version"`
	UptimeS  int64    `json:"uptime_s"`
	Tunnel   Tunnel   `json:"tunnel"`
	Upstream Upstream `json:"upstream"`
	Queue    Queue    `json:"queue"`
	Keys     []Key    `json:"keys"`
}

type Tunnel struct {
	Addr    string `json:"addr"`
	Region  string `json:"region"`
	Clients int    `json:"clients"`
}

type Upstream struct {
	Kind         string    `json:"kind"`
	URL          string    `json:"url"`
	Healthy      bool      `json:"healthy"`
	Since        time.Time `json:"since"` // when healthy last changed (ticket 011: "NOT ANSWERING for Ns")
	ModelContext int       `json:"model_context"`
	Slots        int       `json:"slots"`
}

type Queue struct {
	InFlight int `json:"in_flight"`
	Waiting  int `json:"waiting"`
}

type Key struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	InFlight    int       `json:"in_flight"`
	RPMUsed     int       `json:"rpm_used"`
	TodayTokens int       `json:"today_tokens"`
	LastSeen    time.Time `json:"last_seen"`
}

// Server is the running admin endpoint.
type Server struct {
	l     net.Listener
	srv   *http.Server
	clean func()
}

// Serve starts the admin endpoint for dataDir. status is called per GET /status; reload is called
// per POST /reload and must make an external edit to keys.json visible at once (ticket 009
// promise 9). Both are the host's own, over the unix socket only — never the tunnel (Protection 1).
func Serve(dataDir string, status func() Status, reload func() error) (*Server, error) {
	l, token, clean, err := listen(dataDir)
	if err != nil {
		return nil, err
	}
	authed := func(r *http.Request) bool { return token == "" || r.Header.Get("Authorization") == "Bearer "+token }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if reload != nil {
			if err := reload(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s := status()
		s.Product, s.Version = product.Name, product.Version
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(s)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s := &Server{l: l, srv: srv, clean: clean}
	go srv.Serve(l)
	return s, nil
}

// Addr is where the admin endpoint is listening, for the startup banner.
func (s *Server) Addr() string { return s.l.Addr().String() }

// Close stops serving and removes the socket / port files.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	if s.clean != nil {
		s.clean()
	}
	return err
}

// Reload tells the running host to re-read keys.json now, so a `keys` command's change is in
// force before the command returns instead of within the store's once-per-second poll. No running
// host is not an error to the caller: ErrNoDaemon means there was nothing to tell.
func Reload(ctx context.Context, dataDir string) error {
	hc, base, token, err := dial(dataDir)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/reload", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ErrNoDaemon
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return errors.New("admin API: " + resp.Status)
	}
	return nil
}

// Fetch asks the running host for its status. ErrNoDaemon means nothing is listening.
func Fetch(ctx context.Context, dataDir string) (Status, error) {
	var st Status
	hc, base, token, err := dial(dataDir)
	if err != nil {
		return st, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/status", nil)
	if err != nil {
		return st, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return st, ErrNoDaemon
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return st, errors.New("admin API: " + resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}, err
	}
	return st, nil
}

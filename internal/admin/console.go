package admin

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
)

// ListenConsole accepts literal loopback addresses only; validate before touching host state.
func ListenConsole(address string) (net.Listener, error) {
	if address == "off" {
		return nil, nil
	}
	a, err := netip.ParseAddrPort(address)
	if err != nil || !a.Addr().IsLoopback() {
		return nil, fmt.Errorf("console address must be a loopback IP:port or off")
	}
	return net.Listen("tcp", address)
}

// ServeConsole shares the authenticated admin handler. Opt-in remote requests arrive through
// the gateway's restricted proxy with a host-injected per-run token.
func (s *Server) ServeConsole(l net.Listener, bundle fs.FS) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", s.srv.Handler))
	mux.Handle("/", http.FileServer(http.FS(bundle)))
	srv := &http.Server{ReadHeaderTimeout: s.srv.ReadHeaderTimeout, IdleTimeout: s.srv.IdleTimeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
			// Reject alternate hostnames, including DNS-rebinding names. No CORS grants.
			if r.Host != l.Addr().String() {
				http.Error(w, "invalid host", http.StatusForbidden)
				return
			}
			mux.ServeHTTP(w, r)
		})}
	go srv.Serve(l)
	return srv
}

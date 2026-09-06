// Command tunneldemo starts the tunnel and serves a tiny HTTP handler on it. Ticket 004 develops
// against it; web/wasm/demo.html is the manual check for the wasm bridge.
//
//	go run ./hack/tunneldemo [-data-dir DIR | -ephemeral] [-region sfo] [-demo-listen 127.0.0.1:19080]
//
// Stdout is exactly one line, the tunnel address. Everything else goes to stderr.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/2185Lab/infercat/internal/tunnel"
)

func main() {
	dataDir := flag.String("data-dir", filepath.Join(os.TempDir(), "infercat-tunneldemo"), "directory holding host.key.json; the address is stable across restarts")
	ephemeral := flag.Bool("ephemeral", false, "new identity and address every run; nothing written to disk")
	derpMapURL := flag.String("derpmap-url", "", "alternate DERP map URL (the address then embeds the relay)")
	region := flag.String("region", "", "relay for a new key: region ID, code (sfo), name substring, or DERP hostname(s)")
	verbose := flag.Bool("verbose", false, "log tailcat internals to stderr")
	demoListen := flag.String("demo-listen", "127.0.0.1:19080", "loopback address serving -web-dir over plain HTTP (web/wasm/demo.html); empty disables")
	webDir := flag.String("web-dir", "web", "directory served on -demo-listen")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if *demoListen != "" {
		host, _, err := net.SplitHostPort(*demoListen)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			log.Fatalf("-demo-listen must be a loopback address, got %q", *demoListen)
		}
	}

	o := tunnel.Options{DataDir: *dataDir, Ephemeral: *ephemeral, DERPMapURL: *derpMapURL, Region: *region}
	if *verbose {
		o.Logf = log.Printf
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s, err := tunnel.Start(ctx, o)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.Addr())
	st := s.Status()
	log.Printf("tunnel up via relay %q; cross-check from another terminal: tailcat ping %s", st.RegionName, s.Addr())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)
		for i := range 20 {
			fmt.Fprintf(w, "data: {\"i\":%d,\"t\":%q}\n\n", i, time.Now().Format(time.RFC3339Nano))
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Status())
	})

	if *demoListen != "" {
		go func() { log.Fatal(http.ListenAndServe(*demoListen, http.FileServer(http.Dir(*webDir)))) }()
		log.Printf("demo page: http://%s/wasm/demo.html?addr=%s", *demoListen, s.Addr())
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Printf("closing")
		s.Close()
	}()
	if err := http.Serve(s.Listener(), mux); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
}

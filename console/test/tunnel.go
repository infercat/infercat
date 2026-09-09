// An opt-in proof adapter: curl's HTTP runs through a real tunnel session to the supplied test host.
package main

import (
	"context"
	"fmt"
	"github.com/infercat/infercat/internal/tunnel"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("supply the isolated proof host's tunnel address")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	dialctx, stop := context.WithTimeout(ctx, 30*time.Second)
	s, err := tunnel.Dial(dialctx, os.Args[1], tunnel.ClientOptions{})
	stop()
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	target, _ := url.Parse("http://infercat")
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return s.Open(ctx) }}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/console/") && r.URL.Path != "/v1/models" && r.URL.Path != "/me" {
			http.NotFound(w, r)
			return
		}
		p.ServeHTTP(w, r)
	})}
	fmt.Println("http://" + l.Addr().String())
	go server.Serve(l)
	<-ctx.Done()
	server.Close()
}

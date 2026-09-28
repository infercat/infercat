package main

import (
	"net"
	"testing"
)

// deadUpstreamURL owns the address until cleanup; no parallel fixture can answer it.
func deadUpstreamURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	if other, err := net.Listen("tcp", listener.Addr().String()); err == nil {
		other.Close()
		t.Fatal("dead upstream released its port")
	}
	return "http://" + listener.Addr().String()
}

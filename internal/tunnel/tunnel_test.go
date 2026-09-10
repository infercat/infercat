package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stun"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
)

func mkLogf(t testing.TB, name string) logger.Logf {
	return func(format string, args ...any) {
		if !t.Failed() {
			t.Logf("["+name+"] "+format, args...)
		}
	}
}

// localDERP runs an in-process DERP relay plus STUN on loopback, the way cmd/tailcat's
// TS_DEBUG_TAILCAT_LOCAL_DERP mode (runDevDERP) does, and serves a one-region DERP map for it over
// HTTP so Start can be pointed at it with Options.DERPMapURL. No network is needed.
func localDERP(t testing.TB) (mapURL string) {
	t.Helper()
	d := derpserver.New(key.NewNode(), mkLogf(t, "derp"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsrv := httptest.NewUnstartedServer(derpserver.Handler(d))
	httpsrv.Listener.Close()
	httpsrv.Listener = ln
	httpsrv.Config.ErrorLog = logger.StdLogger(mkLogf(t, "derp-http"))
	httpsrv.Config.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	httpsrv.StartTLS()

	uln, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		var buf [1500]byte
		for {
			n, src, err := uln.ReadFromUDPAddrPort(buf[:])
			if err != nil {
				return
			}
			if txid, err := stun.ParseBindingRequest(buf[:n]); err == nil {
				uln.WriteToUDPAddrPort(stun.Response(txid, src), src)
			}
		}
	}()
	dm := &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{1: {
		RegionID: 1, RegionCode: "test", RegionName: "Local Test Relay",
		Nodes: []*tailcfg.DERPNode{{
			Name: "t1", RegionID: 1, HostName: "127.0.0.1", IPv4: "127.0.0.1", IPv6: "none",
			STUNPort:         uln.LocalAddr().(*net.UDPAddr).Port,
			DERPPort:         ln.Addr().(*net.TCPAddr).Port,
			InsecureForTests: true,
		}},
	}}}
	mapJSON, _ := json.Marshal(dm)
	mapsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(mapJSON) }))
	t.Cleanup(func() {
		mapsrv.Close()
		httpsrv.CloseClientConnections()
		httpsrv.Close()
		d.Close()
		uln.Close()
	})
	return mapsrv.URL
}

// pingUntil is the client-side retry the browser bridge also does: the first meows can be lost
// while either side's DERP connection is still coming up.
func pingUntil(t testing.TB, cl *tailcat.Client) tailcat.PingResult {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		res, err := cl.Ping(ctx)
		cancel()
		if err == nil {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatalf("ping never succeeded: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func startOK(t testing.TB, o Options) *Server {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Start(ctx, o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestListenerServesPort80Only(t *testing.T) {
	mapURL := localDERP(t)
	s := startOK(t, Options{DataDir: t.TempDir(), DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "server")})

	var clientsSeen atomic.Int32
	var handlerHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		handlerHits.Add(1)
		clientsSeen.Store(int32(s.Status().Clients))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	go http.Serve(s.Listener(), mux)

	cl := &tailcat.Client{Server: tailcat.Addr(s.Addr()), Logf: mkLogf(t, "client")}
	t.Cleanup(func() { cl.Close() })
	res := pingUntil(t, cl)
	t.Logf("meow handshake rtt via local DERP: %v", res.Latency)

	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return cl.DialTCPPort(ctx, Port)
	}}, Timeout: 15 * time.Second}
	resp, err := hc.Get("http://tunnel/healthz")
	if err != nil {
		t.Fatalf("GET /healthz through the tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("healthz = %d %q; want 200 {\"ok\":true}", resp.StatusCode, body)
	}
	t.Logf("healthz through the tunnel: %d %s", resp.StatusCode, body)
	if got := clientsSeen.Load(); got != 1 {
		t.Errorf("Status().Clients during the request = %d; want 1", got)
	}
	hc.CloseIdleConnections()
	for deadline := time.Now().Add(10 * time.Second); s.Status().Clients != 0; {
		if time.Now().After(deadline) {
			t.Fatalf("Status().Clients = %d after the connection closed; want 0", s.Status().Clients)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Port 81: the filter drops the SYN silently, so the dial rides out its deadline and the
	// handler never runs. Port 80 keeps working meanwhile.
	hits := handlerHits.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if c, err := cl.DialTCPPort(ctx, 81); err == nil {
		c.Close()
		t.Fatal("dial to port 81 succeeded; want a silent drop")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial to port 81 = %v; want a silent drop until the deadline", err)
	}
	if handlerHits.Load() != hits {
		t.Fatal("port 81 reached the handler")
	}
	if resp, err := hc.Get("http://tunnel/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("second GET /healthz = %v, %v; want 200", resp, err)
	} else {
		resp.Body.Close()
	}

	st := s.Status()
	if st.Addr != s.Addr() || st.RegionName != "Local Test Relay" || st.Started.IsZero() {
		t.Errorf("Status = %+v; want addr %q, region name from the map, a start time", st, s.Addr())
	}

	// After Listener().Close(), port 80 gets a RST (fails fast) and Accept reports closed.
	s.Listener().Close()
	if _, err := s.Listener().Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v; want net.ErrClosed", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if c, err := cl.DialTCPPort(ctx2, Port); err == nil {
		c.Close()
		t.Fatal("dial to port 80 after Listener().Close() succeeded")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial to port 80 after Listener().Close() timed out; want a fast RST")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestHostKeyPersistsAddr(t *testing.T) {
	mapURL := localDERP(t)
	dir := t.TempDir()
	o := Options{DataDir: dir, DERPMapURL: mapURL, Region: "test", Logf: mkLogf(t, "server")}

	if _, err := SavedAddr(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SavedAddr before any Start = %v; want os.ErrNotExist", err)
	}
	s1 := startOK(t, o)
	addr := s1.Addr()
	s1.Close()

	fi, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatalf("host key not written: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("host key mode = %v; want 0600", fi.Mode().Perm())
	}
	if saved, err := SavedAddr(dir); err != nil || saved != addr {
		t.Fatalf("SavedAddr = %q, %v; want the started address %q", saved, err, addr)
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(addr))
	if err != nil {
		t.Fatal(err)
	}
	if len(ci.Region) != 1 || len(ci.Region[0].Nodes) != 1 || ci.Region[0].Nodes[0].HostName != "127.0.0.1" {
		t.Fatalf("address with DERPMapURL set should embed the relay (full form); got %+v", ci)
	}

	s2 := startOK(t, o)
	if s2.Addr() != addr {
		t.Fatalf("address changed across restarts: %q then %q", addr, s2.Addr())
	}
	s2.Close()

	// Options.Region only applies to a new key: the saved region wins.
	s3 := startOK(t, Options{DataDir: dir, DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "server")})
	if s3.Addr() != addr {
		t.Fatalf("address changed when Region differed on a saved key: %q then %q", addr, s3.Addr())
	}
	s3.Close()

	// Ephemeral: new address every time, and never a file.
	edir := t.TempDir()
	e1 := startOK(t, Options{DataDir: edir, Ephemeral: true, DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "eph")})
	e1addr := e1.Addr()
	e1.Close()
	e2 := startOK(t, Options{DataDir: edir, Ephemeral: true, DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "eph")})
	if e2.Addr() == e1addr || e2.Addr() == addr {
		t.Fatalf("ephemeral address reused: %q", e2.Addr())
	}
	e2.Close()
	if ents, _ := os.ReadDir(edir); len(ents) != 0 {
		t.Fatalf("ephemeral Start wrote to disk: %v", ents)
	}
	if _, err := Start(context.Background(), Options{Ephemeral: true, DERPMapURL: mapURL, Region: "nowhere"}); err == nil || !strings.Contains(err.Error(), "test") {
		t.Fatalf("Start with an unknown region code = %v; want an error listing codes", err)
	}
}

// --- no relay needed below ---

func TestAddrShortForm(t *testing.T) {
	pk := NewIdentity()
	pk.Public.RegionID = 302
	addr := addrFor(pk)
	want := (&tailcat.ConnInfo{ServerPublic: pk.Public.ServerPublic, ServerDiscoPublic: pk.Public.ServerDiscoPublic, RegionID: 302, PresharedKey: pk.Public.PresharedKey}).Addr()
	if addr != string(want) {
		t.Fatalf("addrFor = %s; want %s", addr, want)
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(addr))
	if err != nil {
		t.Fatal(err)
	}
	if ci.RegionID != 302 || len(ci.Region) != 0 || ci.ServerDiscoPublic.IsZero() || ci.PresharedKey.IsZero() {
		t.Fatalf("short form parsed to %+v; want RegionID 302, no embedded region, a disco key", ci)
	}
	// A tampered Public.ServerPublic does not change the address: that key derives from Private.
	pk.Public.ServerPublic = tailcat.NodePublic{NodePublic: key.NewNode().Public()}
	if addrFor(pk) != addr {
		t.Fatal("addrFor followed a tampered Public.ServerPublic")
	}

	dir := t.TempDir()
	if _, err := saveKey(filepath.Join(dir, KeyFile), pk); err != nil {
		t.Fatal(err)
	}
	if got, err := SavedAddr(dir); err != nil || got != addr {
		t.Fatalf("SavedAddr = %q, %v; want %q", got, err, addr)
	}
}

// Ticket 005 fix 10f: the temp file is a fresh O_EXCL name, so a planted host.key.json.tmp is
// neither followed nor overwritten, the key lands at 0600, and nothing is left behind. Ticket 009
// promise 1: the identity is created once — a second saveKey never replaces it and hands back the
// identity that is actually on disk, so the caller can adopt it.
func TestSaveKeyIsCreateOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFile)
	decoy := path + ".tmp"
	if err := os.WriteFile(decoy, []byte("decoy"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := (&Identity{PrivateKey: *tailcat.NewPrivateKey()})
	first.Public.RegionID = 302
	for round := 0; round < 2; round++ {
		// Round 1 creates the identity; round 2 is a second host arriving with a different key.
		pk := first
		if round == 1 {
			pk = (&Identity{PrivateKey: *tailcat.NewPrivateKey()})
			pk.Public.RegionID = 303
		}
		saved, err := saveKey(path, pk)
		if err != nil {
			t.Fatal(err)
		}
		if addrFor(saved) != addrFor(first) {
			t.Fatalf("round %d: saveKey returned %s; want the first identity %s", round, addrFor(saved), addrFor(first))
		}
		if b, _ := os.ReadFile(decoy); string(b) != "decoy" {
			t.Fatalf("saveKey wrote through the fixed .tmp name: %q", b)
		}
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("host key: %v, mode %v; want 0600", err, fi.Mode())
		}
		if got, err := SavedAddr(dir); err != nil || got != addrFor(first) {
			t.Fatalf("SavedAddr = %q, %v; want %q", got, err, addrFor(first))
		}
		if ents, _ := os.ReadDir(dir); len(ents) != 2 {
			t.Fatalf("round %d: data dir has %v; want only the key and the decoy", round, ents)
		}
	}

	// Concurrent claims on one fresh dir: every caller comes away with the same identity, which
	// is the one on disk. This is the shape that broke the stranger's first session.
	race := t.TempDir()
	rpath := filepath.Join(race, KeyFile)
	const n = 8
	got := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pk := (&Identity{PrivateKey: *tailcat.NewPrivateKey()})
			pk.Public.RegionID = 301
			saved, err := saveKey(rpath, pk)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = addrFor(saved)
		}()
	}
	wg.Wait()
	want, err := SavedAddr(race)
	if err != nil {
		t.Fatal(err)
	}
	for i, g := range got {
		if g != want {
			t.Fatalf("racer %d came away with %s; the saved identity is %s", i, g, want)
		}
	}
	if ents, _ := os.ReadDir(race); len(ents) != 1 {
		t.Fatalf("racing saveKey left %v behind; want only the host key", ents)
	}
}

// TestStartAgreesWithSavedAddr is ticket 009 promise 1: on a fresh data dir the address the first
// Start prints is the address SavedAddr derives and the address every later Start prints — even
// when a second host is starting on the same dir at the same moment, which is how the first-run
// address used to diverge.
func TestStartAgreesWithSavedAddr(t *testing.T) {
	mapURL := localDERP(t)
	o := func(dir string) Options {
		return Options{DataDir: dir, DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "server")}
	}

	dir := t.TempDir()
	s1 := startOK(t, o(dir))
	addr := s1.Addr()
	saved, err := SavedAddr(dir)
	if err != nil {
		t.Fatal(err)
	}
	if addr != saved {
		t.Fatalf("the first Start printed %s but saved %s", addr, saved)
	}
	s1.Close()
	s2 := startOK(t, o(dir))
	if s2.Addr() != saved {
		t.Fatalf("restart printed %s; the saved identity is %s", s2.Addr(), saved)
	}
	s2.Close()

	// Two hosts starting together on a fresh dir: both serve the identity that is on disk, so
	// invites minted against either one connect.
	race := t.TempDir()
	var wg sync.WaitGroup
	addrs := make([]string, 2)
	for i := range addrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s, err := Start(ctx, o(race))
			if err != nil {
				t.Error(err)
				return
			}
			addrs[i] = s.Addr()
			s.Close()
		}()
	}
	wg.Wait()
	want, err := SavedAddr(race)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range addrs {
		if a != want {
			t.Fatalf("host %d advertised %s but the saved identity is %s", i, a, want)
		}
	}
}

// Ticket 005 fix 10b (001's half): the engine's NetworkMap dump never reaches the host's Logf;
// every other line does, arguments intact.
func TestQuietDropsOnlyTheNetworkMapDump(t *testing.T) {
	var got []string
	logf := quiet(func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) })
	logf("NetworkMap: %v", `{"SelfNode":{"ID":1}}`)
	logf("magicsock: disco key = %s", "d:abc")
	logf("[v1] netstack: registered IP %s", "::/0")
	want := []string{"magicsock: disco key = d:abc", "[v1] netstack: registered IP ::/0"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("quiet passed %q; want %q", got, want)
	}
}

func TestSavedAddrErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFile)
	for name, body := range map[string]string{
		"corrupt":  `{"Private": "nope"`,
		"no_key":   `{"Public": {"RegionID": 302}}`,
		"empty":    ``,
		"unpinned": mustJSON(t, (&Identity{PrivateKey: *tailcat.NewPrivateKey()})),
		"auto_-1": mustJSON(t, func() *Identity {
			pk := (&Identity{PrivateKey: *tailcat.NewPrivateKey()})
			pk.Public.RegionID = -1
			return pk
		}()),
		"array":     `[]`,
		"huge_null": strings.Repeat(" ", 1<<16) + "null",
	} {
		t.Run(name, func(t *testing.T) {
			os.WriteFile(path, []byte(body), 0o600)
			if got, err := SavedAddr(dir); err == nil {
				t.Fatalf("SavedAddr = %q; want an error", got)
			} else if strings.HasPrefix(name, "unpinned") || strings.HasPrefix(name, "auto") {
				if !errors.Is(err, ErrUnpinned) {
					t.Fatalf("SavedAddr = %v; want ErrUnpinned", err)
				}
			}
		})
	}
	if _, err := Start(context.Background(), Options{}); err == nil {
		t.Fatal("Start without DataDir or Ephemeral succeeded")
	}
	os.WriteFile(path, []byte(`{"Private": "nope"}`), 0o600)
	if _, err := Start(context.Background(), Options{DataDir: dir}); err == nil {
		t.Fatal("Start with a corrupt host key succeeded")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestOnTCPGate pins Promise 2 at the callback level: nil for every port but 80, nil once closed.
func TestOnTCPGate(t *testing.T) {
	s := &Server{}
	s.ln = newListener(s)
	for _, port := range []uint16{0, 1, 22, 79, 81, 443, 8080, 65535} {
		if s.onTCP(port) != nil {
			t.Errorf("onTCP(%d) returned a handler", port)
		}
	}
	if s.onTCP(Port) == nil {
		t.Fatal("onTCP(80) returned nil while open")
	}
	s.ln.Close()
	if s.onTCP(Port) != nil {
		t.Fatal("onTCP(80) returned a handler after Close")
	}
}

// TestProtection1Config pins what the tailcat server is never configured with.
func TestProtection1Config(t *testing.T) {
	s := &Server{}
	s.ln = newListener(s)
	tc := newTailcatServer((&Identity{PrivateKey: *tailcat.NewPrivateKey()}), &tailcfg.DERPRegion{RegionID: 1}, logger.Discard, s.onTCP)
	if tc.OnTCPForward != nil || tc.AllowProxy != nil || len(tc.AllowedClients) != 0 {
		t.Fatalf("tailcat server has forwarding/proxy/allowlist set: %+v", tc)
	}
	if len(tc.ServedTCPPorts) != 1 || tc.ServedTCPPorts[0].First != Port || tc.ServedTCPPorts[0].Last != Port {
		t.Fatalf("ServedTCPPorts = %v; want exactly {80,80}", tc.ServedTCPPorts)
	}
	if tc.OnTCP == nil || tc.OnTCP(81) != nil || tc.OnTCP(80) == nil {
		t.Fatal("OnTCP gate not wired")
	}
}

func TestListenerCountsClientsAndClosesParked(t *testing.T) {
	s := &Server{}
	l := newListener(s)
	s.ln = l
	a1, b1 := net.Pipe()
	a2, b2 := net.Pipe()
	defer b1.Close()
	defer b2.Close()
	go l.deliver(a1)
	go l.deliver(a2)
	c1, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	c2, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	// net.Pipe has no TCP addresses, so both count under the zero key: one "client".
	if got := l.clients(); got != 1 {
		t.Fatalf("clients = %d; want 1", got)
	}
	c1.Close()
	c1.Close() // idempotent
	if got := l.clients(); got != 1 {
		t.Fatalf("clients after one close = %d; want 1", got)
	}
	c2.Close()
	if got := l.clients(); got != 0 {
		t.Fatalf("clients after both closed = %d; want 0", got)
	}
	// A connection parked in the listener when it closes is closed, not leaked.
	a3, b3 := net.Pipe()
	l.deliver(a3)
	l.Close()
	b3.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := b3.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("parked connection not closed on listener Close: read = %v", err)
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v; want net.ErrClosed", err)
	}
	// deliver after Close closes the connection instead of parking it.
	a4, b4 := net.Pipe()
	l.deliver(a4)
	b4.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := b4.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("delivered-after-close connection not closed: read = %v", err)
	}
}

func TestChooseRegion(t *testing.T) {
	ctx := context.Background()
	var ci tailcat.ConnInfo
	if err := chooseRegion(ctx, Options{Region: "302"}, &ci); err != nil || ci.RegionID != 302 {
		t.Fatalf("numeric region: %+v, %v", ci, err)
	}
	ci = tailcat.ConnInfo{}
	if err := chooseRegion(ctx, Options{Region: "derp1.example.com, derp2.example.com"}, &ci); err != nil ||
		len(ci.Region) != 1 || len(ci.Region[0].Nodes) != 2 || ci.Region[0].Nodes[1].HostName != "derp2.example.com" {
		t.Fatalf("hostname region: %+v, %v", ci, err)
	}
	ci = tailcat.ConnInfo{}
	if err := chooseRegion(ctx, Options{}, &ci); err != nil || ci.RegionID != -1 {
		t.Fatalf("auto region: %+v, %v", ci, err)
	}
	for _, bad := range []string{"0", "-5"} {
		if err := chooseRegion(ctx, Options{Region: bad}, &ci); err == nil {
			t.Fatalf("region %q accepted", bad)
		}
	}
	if err := chooseRegion(ctx, Options{Region: "sfo", DERPMapURL: "http://127.0.0.1:1/nope"}, &ci); err == nil {
		t.Fatal("region code lookup without a reachable map succeeded")
	}
	if fmt.Sprint(regionName(&tailcfg.DERPRegion{RegionID: 302, RegionCode: "sfo", RegionName: "San Francisco"})) != "San Francisco" ||
		regionName(&tailcfg.DERPRegion{RegionID: 1, RegionCode: "1", Nodes: []*tailcfg.DERPNode{{HostName: "derp.example.com"}}}) != "derp.example.com" ||
		regionName(&tailcfg.DERPRegion{RegionID: 7}) != "7" {
		t.Fatal("regionName fallbacks wrong")
	}
}

// TestSavedAddrLive is an opt-in check against a real data dir (e.g. one hack/tunneldemo is
// running from): INFERCAT_TUNNEL_DATA_DIR=/path go test ./internal/tunnel -run SavedAddrLive -v
func TestSavedAddrLive(t *testing.T) {
	dir := os.Getenv("INFERCAT_TUNNEL_DATA_DIR")
	if dir == "" {
		t.Skip("INFERCAT_TUNNEL_DATA_DIR not set")
	}
	addr, err := SavedAddr(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SavedAddr(%s) = %s", dir, addr)
}

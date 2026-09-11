package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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

	qrcode "github.com/skip2/go-qrcode"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/product"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

// testPlatform stands in for tickets 001 and 002 with a fixed address, so the CLI's own
// behaviour can be checked without them.
func testPlatform(addr string, addrErr error) platform {
	return platform{
		startTunnel: func(ctx context.Context, o tunnelOptions) (tunnelServer, error) {
			return nil, errors.New("not used in these tests")
		},
		savedAddr: func(string) (string, error) {
			if addrErr != nil {
				return "", addrErr
			}
			return addr, nil
		},
		encodeInvite: func(a, s string) string { return product.InvitePrefix + "." + a + "." + s },
		newGateway: func(gatewayOptions, upstream.Upstream, keys.Store, usage.Recorder, func(string, ...any)) (gatewayServer, error) {
			return nil, errors.New("not used in these tests")
		},
	}
}

type result struct {
	code int
	out  string
	err  string
}

func exec(t *testing.T, plat platform, args ...string) result {
	t.Helper()
	return execIn(t, plat, "", args...)
}

// execIn runs the command with stdin, and with tty on, so the tests see the QR and can answer a
// confirmation prompt the way a person at a terminal would.
func execIn(t *testing.T, plat platform, stdin string, args ...string) result {
	t.Helper()
	var out, errw lockedBuffer
	code := run(context.Background(), args, &out, &errw, strings.NewReader(stdin), true, plat)
	return result{code, out.String(), errw.String()}
}

const fakeAddr = "tcFAKEADDRESSFORTESTS"

func TestKeysAddPrintsAnInviteExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	r := exec(t, testPlatform(fakeAddr, nil), "keys", "add", "alice", "--data-dir", dir, "--rpm", "60")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s%s", r.code, r.out, r.err)
	}
	prefix := product.InvitePrefix + "." + fakeAddr + "."
	i := strings.Index(r.out, prefix)
	if i < 0 {
		t.Fatalf("no invite in the output:\n%s", r.out)
	}
	secret := strings.Fields(r.out[i:])[0][len(prefix):]
	if len(secret) != 43 {
		t.Errorf("secret %q is %d chars, want 43", secret, len(secret))
	}
	if !strings.Contains(r.out, "shown once") {
		t.Error("the output does not say the secret is shown once")
	}

	// The secret is usable, is stored only as a hash, and is not repeated by a later command.
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	k, ok, err := store.Lookup(context.Background(), secret)
	if err != nil || !ok {
		t.Fatalf("the printed secret does not resolve: %v %v", ok, err)
	}
	if k.Limits.RPM != 60 {
		t.Errorf("rpm = %d, want the 60 that followed the name on the command line", k.Limits.RPM)
	}
	raw := string(readFile(t, filepath.Join(dir, keys.FileName)))
	if strings.Contains(raw, secret) {
		t.Error("keys.json contains the plaintext secret")
	}
	list := exec(t, testPlatform(fakeAddr, nil), "keys", "list", "--data-dir", dir)
	if strings.Contains(list.out, secret) {
		t.Error("keys list reprinted the secret")
	}
}

// Minting a key whose invite cannot be printed would burn a secret nobody ever sees.
func TestKeysAddRefusesWithoutAHostAddress(t *testing.T) {
	dir := t.TempDir()
	r := exec(t, testPlatform("", errors.New("no host key")), "keys", "add", "alice", "--data-dir", dir)
	if r.code == 0 {
		t.Fatalf("exit 0; want a refusal\n%s", r.out)
	}
	if !strings.Contains(r.err, "serve") {
		t.Errorf("stderr = %q, want it to point at `serve`", r.err)
	}
	if _, err := os.Stat(filepath.Join(dir, keys.FileName)); err == nil {
		t.Error("a key was written even though the invite could not be printed")
	}
}

func TestKeysLifecycle(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	if r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	for _, tc := range []struct{ args, want string }{
		{"keys pause alice", "paused"},
		{"keys resume alice", "active"},
		{"keys revoke alice --yes", "revoked"},
	} {
		r := exec(t, plat, append(strings.Fields(tc.args), "--data-dir", dir)...)
		if r.code != 0 || !strings.Contains(r.out, tc.want) {
			t.Errorf("%s = %d %q", tc.args, r.code, r.out+r.err)
		}
	}
	r := exec(t, plat, "keys", "limits", "alice", "--rpm", "5", "--models", "a,b", "--data-dir", dir)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	store, _ := keys.NewFileStore(dir)
	k, err := store.Find(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if k.Limits.RPM != 5 || len(k.Limits.Models) != 2 {
		t.Errorf("limits = %+v", k.Limits)
	}
	if k.Limits.TPM != keys.DefaultLimits().TPM {
		t.Errorf("tpm = %d; flags not passed must not change", k.Limits.TPM)
	}
	if r := exec(t, plat, "keys", "pause", "nobody", "--data-dir", dir); r.code == 0 {
		t.Error("pausing an unknown key exited 0")
	}
}

func TestRotatePrintsANewInviteAndRetiresTheOld(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	add := exec(t, plat, "keys", "add", "alice", "--data-dir", dir)
	rot := exec(t, plat, "keys", "rotate", "alice", "--data-dir", dir)
	if rot.code != 0 {
		t.Fatalf("%s", rot.err)
	}
	prefix := product.InvitePrefix + "." + fakeAddr + "."
	first := strings.Fields(add.out[strings.Index(add.out, prefix):])[0]
	second := strings.Fields(rot.out[strings.Index(rot.out, prefix):])[0]
	if first == second {
		t.Fatal("rotate printed the same invite")
	}
	store, _ := keys.NewFileStore(dir)
	if _, ok, _ := store.Lookup(context.Background(), strings.TrimPrefix(first, prefix)); ok {
		t.Error("the old secret still works")
	}
	if _, ok, _ := store.Lookup(context.Background(), strings.TrimPrefix(second, prefix)); !ok {
		t.Error("the new secret does not work")
	}
}

// The QR must carry the invite verbatim; a mistyped one is a friend who cannot connect.
func TestQRMatchesTheInvite(t *testing.T) {
	invite := product.InvitePrefix + "." + fakeAddr + ".GEcLTxHfEEc1nkOJcHYzDbJmMBLbEwXAJfDBrjE8CQA"
	var buf lockedBuffer
	if err := writeQR(&buf, invite); err != nil {
		t.Fatal(err)
	}
	q, err := qrcode.New(invite, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	bm := q.Bitmap()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != (len(bm)+1)/2 {
		t.Fatalf("%d rendered rows for %d module rows", len(lines), len(bm))
	}
	for y := 0; y < len(bm); y += 2 {
		row := []rune(strings.TrimSuffix(strings.TrimPrefix(lines[y/2], "  \x1b[30;47m"), "\x1b[0m"))
		if len(row) != len(bm[y]) {
			t.Fatalf("row %d has %d cells, want %d", y/2, len(row), len(bm[y]))
		}
		for x := range bm[y] {
			bottom := y+1 < len(bm) && bm[y+1][x]
			if want := []rune(halfBlock(bm[y][x], bottom))[0]; row[x] != want {
				t.Fatalf("module (%d,%d) rendered %q, want %q", x, y, row[x], want)
			}
		}
	}
}

// The documented `keys add alice --rpm 60` puts a flag after a positional, which the standard
// flag package would otherwise ignore.
func TestFlagsAfterPositionals(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	n := fs.Int("n", 0, "")
	b := fs.Bool("b", false, "")
	s := fs.String("s", "", "")
	ordered, err := reorder(fs, []string{"name", "-n", "5", "-b", "-s=x", "extra"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Parse(ordered); err != nil {
		t.Fatal(err)
	}
	if *n != 5 || !*b || *s != "x" {
		t.Errorf("n=%d b=%v s=%q", *n, *b, *s)
	}
	if got := fs.Args(); len(got) != 2 || got[0] != "name" || got[1] != "extra" {
		t.Errorf("positionals = %v", got)
	}
	// Everything after -- stays positional.
	fs2 := flag.NewFlagSet("t2", flag.ContinueOnError)
	fs2.Int("n", 0, "")
	ordered2, err := reorder(fs2, []string{"--", "-n", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fs2.Parse(ordered2); err != nil {
		t.Fatal(err)
	}
	if got := fs2.Args(); len(got) != 2 {
		t.Errorf("after --: %v", got)
	}
}

// 005 fix 10h: a value flag as the last word must not swallow the "--" terminator as its value.
func TestDanglingValueFlagIsAnError(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("models", "", "")
	fs.Bool("b", false, "")
	if _, err := reorder(fs, []string{"zed", "--models"}); err == nil || !strings.Contains(err.Error(), "needs an argument") {
		t.Fatalf("dangling --models: err = %v", err)
	}
	if _, err := reorder(fs, []string{"zed", "--b"}); err != nil { // a bool flag may end the line
		t.Fatalf("trailing bool flag: %v", err)
	}
	if got, err := reorder(fs, []string{"zed", "--models=a"}); err != nil || strings.Join(got, " ") != "--models=a -- zed" {
		t.Fatalf("--flag=value: %v %v", got, err)
	}
	// Through the real command: exit 2, nothing minted.
	dir := t.TempDir()
	r := exec(t, testPlatform(fakeAddr, nil), "keys", "add", "zed", "--data-dir", dir, "--models")
	if r.code != 2 || !strings.Contains(r.err, "flag needs an argument: --models") {
		t.Fatalf("exit %d, stderr %q; want 2 and the flag error", r.code, r.err)
	}
	if list := exec(t, testPlatform(fakeAddr, nil), "keys", "list", "--data-dir", dir); strings.Contains(list.out, "zed") {
		t.Fatalf("a key was minted by a malformed command:\n%s", list.out)
	}
}

func TestGlobalDataDirBeforeTheCommand(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	if r := exec(t, plat, "--data-dir", dir, "keys", "add", "alice"); r.code != 0 {
		t.Fatalf("%s", r.err)
	}
	r := exec(t, plat, "--data-dir="+dir, "keys", "list")
	if r.code != 0 || !strings.Contains(r.out, "alice") {
		t.Errorf("list = %d %q %q", r.code, r.out, r.err)
	}
	if r := exec(t, plat, "--nonsense", "keys", "list"); r.code != 2 {
		t.Errorf("unknown global flag exited %d, want 2", r.code)
	}
}

func TestHelpAndExitCodes(t *testing.T) {
	plat := testPlatform(fakeAddr, nil)
	for _, tc := range []struct {
		args []string
		code int
		out  string
	}{
		{nil, 0, "share your local inference"},
		{[]string{"help"}, 0, "Start here:"},
		{[]string{"version"}, 0, product.Version},
		{[]string{"serve", "-h"}, 0, "Runs the host"},
		{[]string{"keys", "-h"}, 0, "One key is one person"},
		{[]string{"keys", "add", "-h"}, 0, "shown here and never again"},
		{[]string{"status", "-h"}, 0, "admin socket"},
		{[]string{"usage", "-h"}, 0, "median and p95"},
	} {
		r := exec(t, plat, tc.args...)
		if r.code != tc.code {
			t.Errorf("%v exited %d, want %d (%s)", tc.args, r.code, tc.code, r.err)
		}
		if !strings.Contains(r.out, tc.out) {
			t.Errorf("%v stdout = %q, want it to mention %q", tc.args, r.out, tc.out)
		}
	}
	for _, args := range [][]string{{"frobnicate"}, {"keys"}, {"keys", "frobnicate"}, {"keys", "add"}, {"serve", "--nope"}} {
		if r := exec(t, plat, args...); r.code != 2 {
			t.Errorf("%v exited %d, want 2", args, r.code)
		}
	}
}

// `status` without a running host must fail loudly rather than print an empty block.
func TestStatusWithoutADaemon(t *testing.T) {
	r := exec(t, testPlatform(fakeAddr, nil), "status", "--data-dir", t.TempDir())
	if r.code == 0 {
		t.Errorf("exit 0 with no host running")
	}
	if !strings.Contains(r.err, "no host is running") {
		t.Errorf("stderr = %q", r.err)
	}
}

func TestUsageAggregatesFromTheLog(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	if r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	store, _ := keys.NewFileStore(dir)
	list, _ := store.List(context.Background())
	id := list[0].ID

	rec, err := usage.NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	chat := "/v1/chat/completions"
	rec.Record(context.Background(), usage.Event{TS: now, KeyID: id, Endpoint: chat, Status: 200, PromptTokens: 100, CompletionTokens: 20, TTFTMS: 120, TotalMS: 3100})
	rec.Record(context.Background(), usage.Event{TS: now, KeyID: id, Via: "bridge", Endpoint: chat, Status: 429, Code: "rate_limited"})
	rec.Record(context.Background(), usage.Event{TS: now, KeyID: id, Endpoint: "/me", Status: 200, TTFTMS: 2, TotalMS: 2})
	rec.Record(context.Background(), usage.Event{TS: now.Add(-72 * time.Hour), KeyID: id, Endpoint: chat, Status: 200, PromptTokens: 999})
	rec.Close()

	r := exec(t, plat, "usage", "--data-dir", dir)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	for _, want := range []string{"requests  2 model calls (+1 app polls)", "rate_limited 1", "100 prompt", "120 ms", "3.1 s", "alice"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("usage output missing %q:\n%s", want, r.out)
		}
	}
	for _, want := range []string{"ID NAME VIA CALLS", "alice bridge 1 0 1", "alice direct 1 1 0", "via bridge 1 model call — 1 error: rate_limited 1", "via direct 1 model call (+1 app polls)"} {
		if !strings.Contains(strings.Join(strings.Fields(r.out), " "), want) {
			t.Errorf("usage missing via column or total %q:\n%s", want, r.out)
		}
	}
	// The 72-hour-old event is outside the default window but inside --since all.
	if all := exec(t, plat, "usage", "--since", "all", "--data-dir", dir); !strings.Contains(all.out, "requests  3 model calls") {
		t.Errorf("--since all:\n%s", all.out)
	}
	// keys list picks up "last seen" from the same log.
	if l := exec(t, plat, "keys", "list", "--data-dir", dir); strings.Contains(l.out, "never") {
		t.Errorf("keys list still says never:\n%s", l.out)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":      0,
		"all":   0,
		"30m":   30 * time.Minute,
		"24h":   24 * time.Hour,
		"7d":    7 * 24 * time.Hour,
		"1d":    24 * time.Hour,
		"1h30m": 90 * time.Minute,
	} {
		got, err := parseSince(in)
		if err != nil || got != want {
			t.Errorf("parseSince(%q) = %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"bogus", "-5h", "7days"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func TestConfigRoundTripAndDefaults(t *testing.T) {
	dir := t.TempDir()
	if c, err := loadConfig(dir); err != nil || c.Upstream != "" {
		t.Fatalf("missing config = %+v %v, want the zero value", c, err)
	}
	want := config{Upstream: "http://127.0.0.1:8000", Slots: 4, Name: "max"}
	if err := saveConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(dir)
	if err != nil || got != want {
		t.Errorf("round trip = %+v %v", got, err)
	}
	fi, err := os.Stat(filepath.Join(dir, configName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config.json mode = %v, want 0600 (it may hold an upstream API key)", fi.Mode().Perm())
	}
	// --log-prompts and --ephemeral are deliberately absent from the persisted shape.
	var raw map[string]any
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, configName)), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"log_prompts", "ephemeral"} {
		if _, ok := raw[k]; ok {
			t.Errorf("config.json persists %q; it must be typed each run", k)
		}
	}
	// A config.json from before ticket 010 still loads: its retired keys are ignored.
	old := `{"upstream":"http://127.0.0.1:8000","slots":4,"queue_timeout":"45s","request_timeout":"5m0s","max_body":1024,"name":"max"}`
	if err := os.WriteFile(filepath.Join(dir, configName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := loadConfig(dir); err != nil || got != want {
		t.Errorf("old config with retired keys = %+v %v, want %+v", got, err, want)
	}
}

func TestComma(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 12004: "12,004", 1234567: "1,234,567", -4321: "-4,321"} {
		if got := comma(in); got != want {
			t.Errorf("comma(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDataDirDefaultsUnderTheUserConfigDir(t *testing.T) {
	got, err := resolveDataDir("")
	if err != nil {
		t.Skipf("no user config dir here: %v", err)
	}
	base, _ := os.UserConfigDir()
	if got != filepath.Join(base, product.CLIName) {
		t.Errorf("default data dir = %q, want %q", got, filepath.Join(base, product.CLIName))
	}
}

// A gateway server must satisfy the interface this command wires ticket 002 through.
var _ gatewayServer = (*fakeGateway)(nil)

type fakeGateway struct {
	sessions map[string]int
	serving  chan struct{} // closed when Serve is first called
	done     chan struct{} // closed by Shutdown; Serve blocks until then
	once     sync.Once
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{serving: make(chan struct{}), done: make(chan struct{})}
}

func (g *fakeGateway) Handler() http.Handler { return http.NotFoundHandler() }

func (g *fakeGateway) Serve(net.Listener) error {
	g.once.Do(func() { close(g.serving) })
	<-g.done
	return nil
}
func (g *fakeGateway) ServeDev(string) error                     { return nil }
func (g *fakeGateway) Shutdown(context.Context) error            { close(g.done); return nil }
func (g *fakeGateway) Counters(string) usage.KeyCounters         { return usage.KeyCounters{} }
func (g *fakeGateway) AllCounters() map[string]usage.KeyCounters { return nil }
func (g *fakeGateway) Destinations() []gateway.DestinationStatus { return nil }
func (g *fakeGateway) EngineRoutes() []string {
	return gateway.New(gateway.Config{}, nil, nil, nil, nil).EngineRoutes()
}
func (g *fakeGateway) Sessions() map[string]int { return g.sessions }
func (g *fakeGateway) Queue() (int, int)        { return 0, 0 }

type fakeTunnel struct{}

func (fakeTunnel) Listener() net.Listener { return nil }
func (fakeTunnel) Addr() string           { return fakeAddr }
func (fakeTunnel) Status() tunnelStatus   { return tunnelStatus{Addr: fakeAddr, Region: "Testville"} }
func (fakeTunnel) Peers() []admin.Session { return nil }
func (fakeTunnel) Close() error           { return nil }

// Ticket 005 fixes 10b and 10d through the real serve command: the tunnel engine's log lands
// in <data-dir>/tunnel.log and never on the terminal (--verbose flips that), and the engine's
// slot count is re-read by the refresh loop and reported — the gateway's queue reads it live
// (ticket 010, DESIGN §1.5), nothing is pushed.
func TestServeRoutesTunnelLogAndFollowsSlots(t *testing.T) {
	dir, err := os.MkdirTemp("", "bn005-") // short: the admin socket path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var engineSlots atomic.Int32
	engineSlots.Store(1)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			fmt.Fprintf(w, `{"total_slots":%d,"default_generation_settings":{"n_ctx":4096}}`, engineSlots.Load())
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"m"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	prev := refreshEvery
	refreshEvery = 20 * time.Millisecond
	defer func() { refreshEvery = prev }()

	serve := func(verbose bool) (*fakeGateway, result) {
		engineSlots.Store(1) // the engine "changes" to 3 only once serve is up
		gw := newFakeGateway()
		var engineSeen atomic.Pointer[upstream.Upstream]
		plat := testPlatform(fakeAddr, nil)
		plat.startTunnel = func(ctx context.Context, o tunnelOptions) (tunnelServer, error) {
			o.Logf("magicsock: disco key = d:test")
			o.Logf("NetworkMap: {\"SelfNode\":1}")
			return fakeTunnel{}, nil
		}
		plat.newGateway = func(_ gatewayOptions, up upstream.Upstream, _ keys.Store, _ usage.Recorder, _ func(string, ...any)) (gatewayServer, error) {
			engineSeen.Store(&up)
			return gw, nil
		}
		args := []string{"serve", "--console", "127.0.0.1:0", "--data-dir", dir, "--upstream", engine.URL}
		if verbose {
			args = append(args, "--verbose")
		}
		ctx, cancel := context.WithCancel(context.Background())
		var out, errw lockedBuffer
		code := make(chan int, 1)
		go func() { code <- run(ctx, args, &out, &errw, nil, false, plat) }()
		select {
		case <-gw.serving:
		case <-time.After(10 * time.Second):
			t.Fatalf("serve never reached the gateway:\n%s%s", out.String(), errw.String())
		}
		engineSlots.Store(3)
		for deadline := time.Now().Add(5 * time.Second); (*engineSeen.Load()).Info().Slots != 3; {
			if time.Now().After(deadline) {
				t.Fatalf("the engine state never followed the engine to 3 slots (got %d)", (*engineSeen.Load()).Info().Slots)
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		select {
		case c := <-code:
			return gw, result{c, out.String(), errw.String()}
		case <-time.After(15 * time.Second):
			t.Fatal("serve did not stop after Ctrl-C")
		}
		return nil, result{}
	}

	_, r := serve(false)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s%s", r.code, r.out, r.err)
	}
	for _, want := range []string{product.Name, "upstream  llama.cpp", "slots 1", "tunnel    [invalid tunnel address]", "relay     Testville"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("startup output lacks %q:\n%s", want, r.out)
		}
	}
	if strings.Contains(r.err, "magicsock") || strings.Contains(r.err, "NetworkMap") {
		t.Errorf("engine log reached the terminal:\n%s", r.err)
	}
	if !strings.Contains(r.err, "engine slots: 3 (was 1)") {
		t.Errorf("slot change not reported:\n%s", r.err)
	}
	logPath := filepath.Join(dir, tunnelLogName)
	if fi, err := os.Stat(logPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("tunnel.log: %v, mode %v; want 0600", err, fi.Mode())
	}
	if tl := string(readFile(t, logPath)); !strings.Contains(tl, "magicsock: disco key") {
		t.Errorf("tunnel.log lacks the engine line:\n%s", tl)
	}

	_, r = serve(true)
	if r.code != 0 || !strings.Contains(r.err, "magicsock: disco key") {
		t.Errorf("--verbose must put the engine log on the terminal (exit %d):\n%s", r.code, r.err)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- ticket 009 ----

// serveOnce runs the real serve command against a fake engine, tunnel, and gateway, waits until
// the gateway is serving, then stops it — so the startup banner can be read as a host reads it.
func serveOnce(t *testing.T, dir string, args ...string) result {
	t.Helper()
	gw := newFakeGateway()
	plat := testPlatform(fakeAddr, nil)
	plat.startTunnel = func(ctx context.Context, o tunnelOptions) (tunnelServer, error) { return fakeTunnel{}, nil }
	plat.newGateway = func(gatewayOptions, upstream.Upstream, keys.Store, usage.Recorder, func(string, ...any)) (gatewayServer, error) {
		return gw, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw lockedBuffer
	code := make(chan int, 1)
	go func() {
		code <- run(ctx, append([]string{"serve", "--console", "127.0.0.1:0", "--data-dir", dir}, args...), &out, &errw, nil, false, plat)
	}()
	select {
	case <-gw.serving:
	case c := <-code:
		return result{c, out.String(), errw.String()}
	case <-time.After(10 * time.Second):
		t.Fatalf("serve never reached the gateway:\n%s%s", out.String(), errw.String())
	}
	cancel()
	select {
	case c := <-code:
		return result{c, out.String(), errw.String()}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
	return result{}
}

// fakeEngine is a llama.cpp-shaped upstream: enough for detection, health, and a model list.
func fakeEngine(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			io.WriteString(w, `{"total_slots":2,"default_generation_settings":{"n_ctx":4096}}`)
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"m"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The banner must answer what the strangers had to go looking for: the name friends see, exactly
// what they can reach, where the host's own files are, and — once — what host.key.json is
// (ticket 009 promises 4, 7, 8, 12).
func TestServeBannerTellsTheTruth(t *testing.T) {
	dir, err := os.MkdirTemp("", "bn009-") // short: the admin socket path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	engine := fakeEngine(t)

	first := serveOnce(t, dir, "--upstream", engine, "--name", "Max's laptop", "--web-url", "https://app.example")
	for _, want := range []string{
		"name      Max's laptop  (shown to your friends)",
		"access    friends reach only /v1/chat/completions, /v1/embeddings, /v1/models and /v1/responses on " + engine,
		"nothing else on this machine",
		"data      " + dir,
		"web       https://app.example",
		"wrote " + "host.key.json" + " — this is your host identity",
		"Mint a friend:",
	} {
		if !strings.Contains(first.out, want) {
			t.Errorf("first banner missing %q:\n%s", want, first.out)
		}
	}

	// The identity note is for the run that creates the file, and never again. (The fake tunnel
	// writes no key, so plant one the way a real first run would have.)
	if err := os.WriteFile(filepath.Join(dir, "host.key.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := serveOnce(t, dir)
	if strings.Contains(second.out, "this is your host identity") {
		t.Errorf("the identity note was printed again:\n%s", second.out)
	}
	if !strings.Contains(second.out, "web       https://app.example") || !strings.Contains(second.out, "data      "+dir) {
		t.Errorf("remembered web url or data dir missing on the second run:\n%s", second.out)
	}

	// One key is "1 key", not "1 keys"; when none of them is active the line says what to do.
	plat := testPlatform(fakeAddr, nil)
	if r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := serveOnce(t, dir); !strings.Contains(r.out, "\n1 key active\n") {
		t.Errorf("want \"1 key active\":\n%s", r.out)
	}
	if r := exec(t, plat, "keys", "revoke", "alice", "--yes", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := serveOnce(t, dir); !strings.Contains(r.out, "1 key, none active — all paused or revoked") {
		t.Errorf("want the actionable zero-active line:\n%s", r.out)
	}
}

// A remembered upstream that never answers must name the file it is remembered in and the way
// out; `--upstream auto` is that way out (ticket 009 promise 10).
func TestDownUpstreamNamesTheWayOut(t *testing.T) {
	dir, err := os.MkdirTemp("", "bn009-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dead, err := net.Listen("tcp", "127.0.0.1:0") // a port nothing is behind
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + dead.Addr().String()
	dead.Close()

	r := serveOnce(t, dir, "--upstream", url)
	for _, want := range []string{"is not answering", configPath(dir), "--upstream auto"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("warning missing %q:\n%s", want, r.err)
		}
	}
	// The banner does not name an engine nobody has met (ticket 011, DESIGN §3.2).
	if want := "upstream  (not identified yet)  " + url + "  NOT ANSWERING for "; !strings.Contains(r.out, want) {
		t.Errorf("banner missing %q:\n%s", want, r.out)
	}
	if strings.Contains(r.out, "openai-compatible") {
		t.Errorf("banner guessed a kind for an engine that never answered:\n%s", r.out)
	}
	if got := forgetIfAuto("auto"); got != "" {
		t.Errorf("forgetIfAuto(auto) = %q; want the remembered URL forgotten", got)
	}
	if got := forgetIfAuto("  AUTO "); got != "" {
		t.Errorf("forgetIfAuto is case- and space-sensitive: %q", got)
	}
	if got := forgetIfAuto("http://x:1"); got != "http://x:1" {
		t.Errorf("forgetIfAuto ate a real URL: %q", got)
	}
}

func TestHostDisplayName(t *testing.T) {
	if got := hostDisplayName("  Max's laptop "); got != "Max's laptop" {
		t.Errorf("hostDisplayName trimmed wrong: %q", got)
	}
	h, err := os.Hostname()
	if err != nil {
		t.Skip("no hostname on this system")
	}
	want := strings.TrimSuffix(strings.TrimSpace(h), ".local")
	if got := hostDisplayName(""); got != want {
		t.Errorf("hostDisplayName(\"\") = %q; want this machine's hostname %q", got, want)
	}
}

// `keys add alice` twice is what a host does after losing the invite in scrollback. It used to
// mint a second alice and break every later `keys pause alice` as ambiguous (promise 3).
func TestDuplicateNameIsRefused(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	if r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir)
	if r.code == 0 {
		t.Fatalf("a second alice was minted:\n%s", r.out)
	}
	for _, want := range []string{"alice already has an active key", "keys rotate alice", "keys add alice-laptop", "--force"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("refusal missing %q:\n%s", want, r.err)
		}
	}
	if r := exec(t, plat, "keys", "add", "alice", "--force", "--data-dir", dir); r.code != 0 {
		t.Fatalf("--force did not mint: %s", r.err)
	}
	// A revoked namesake is not in the way: the name is free again.
	dir2 := t.TempDir()
	if r := exec(t, plat, "keys", "add", "bob", "--data-dir", dir2); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := exec(t, plat, "keys", "revoke", "bob", "--yes", "--data-dir", dir2); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := exec(t, plat, "keys", "add", "bob", "--data-dir", dir2); r.code != 0 {
		t.Fatalf("a revoked namesake blocked the name:\n%s", r.err)
	}
	// A paused one is: pausing is temporary, so the person still holds a live invite.
	if r := exec(t, plat, "keys", "pause", "bob", "--data-dir", dir2); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := exec(t, plat, "keys", "add", "bob", "--data-dir", dir2); r.code == 0 {
		t.Errorf("a paused namesake did not block the name:\n%s", r.out)
	}
}

// The invite must say where it goes: a link when the host has a web app, the honest alternative
// when nobody has hosted one (promise 2), and a machine-readable form for scripts (promise 11).
func TestInviteNamesItsDestination(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)

	// The product ships with a hosted web app (product.WebURL), so an unconfigured host names it.
	r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir)
	if !strings.Contains(r.out, "Send alice this link:") || !strings.Contains(r.out, product.WebURL+"#"+product.InvitePrefix+".") {
		t.Errorf("an unconfigured host did not name the hosted web app as the destination:\n%s", r.out)
	}

	if err := saveConfig(dir, config{WebURL: "https://app.example"}); err != nil {
		t.Fatal(err)
	}
	r = exec(t, plat, "keys", "add", "bob", "--data-dir", dir)
	link := "https://app.example#" + product.InvitePrefix + "." + fakeAddr + "."
	if !strings.Contains(r.out, "Send bob this link:") || !strings.Contains(r.out, link) {
		t.Errorf("no link when a web app is known:\n%s", r.out)
	}

	// --json is exactly the four fields, and nothing human.
	r = exec(t, plat, "keys", "add", "carol", "--json", "--data-dir", dir)
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("--json did not print an object: %v\n%s", err, r.out)
	}
	if len(got) != 4 || got["name"] != "carol" || got["key_id"] == "" {
		t.Errorf("--json fields = %v", got)
	}
	inv, _ := got["invite"].(string)
	if !strings.HasPrefix(inv, product.InvitePrefix+".") || got["link"] != "https://app.example#"+inv {
		t.Errorf("--json invite/link = %v", got)
	}
	if strings.Contains(r.out, "shown once") || strings.Contains(r.out, "\x1b[") {
		t.Errorf("--json printed human output too:\n%s", r.out)
	}

	// rotate speaks the same way.
	if r := exec(t, plat, "keys", "rotate", "bob", "--data-dir", dir); !strings.Contains(r.out, "Send bob this link:") {
		t.Errorf("rotate did not name the destination:\n%s", r.out)
	}
}

// The QR is decoration for a person: not in a pipe, and not when the host says no (promise 11).
func TestQRIsForTerminalsOnly(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	if r := execIn(t, plat, "", "keys", "add", "alice", "--data-dir", dir); !strings.Contains(r.out, "\x1b[30;47m") {
		t.Errorf("no QR on a terminal:\n%s", r.out)
	}
	var out, errw lockedBuffer
	if code := run(context.Background(), []string{"keys", "add", "bob", "--data-dir", dir}, &out, &errw, nil, false, plat); code != 0 {
		t.Fatal(errw.String())
	}
	if strings.Contains(out.String(), "\x1b[30;47m") {
		t.Errorf("QR drawn into a pipe:\n%s", out.String())
	}
	if r := execIn(t, plat, "", "keys", "add", "carol", "--no-qr", "--data-dir", dir); strings.Contains(r.out, "\x1b[30;47m") {
		t.Errorf("--no-qr still drew one:\n%s", r.out)
	}
}

// A QR is ~30 rows tall, so on a short terminal it scrolls the invite out of sight — a host
// persona lost one that way. The line to copy is printed again underneath the QR, and only there:
// --no-qr, a pipe and --json are untouched (ticket 016 promise 2).
func TestInviteIsRepeatedUnderTheQR(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	qrEnd := func(out string) int { return strings.LastIndex(out, "\x1b[0m") }

	r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir)
	// The line to copy is the link (the QR carries it too); the bare invite is only ever part of it.
	inv := strings.Fields(r.out[strings.Index(r.out, product.WebURL+"#"):])[0]
	if n := strings.Count(r.out, inv); n != 2 {
		t.Fatalf("the link appears %d time(s); want once above the QR and once under it:\n%s", n, r.out)
	}
	if strings.LastIndex(r.out, inv) < qrEnd(r.out) {
		t.Errorf("the repeat is not under the QR:\n%s", r.out)
	}
	t.Logf("keys add alice — everything after the QR:\n%s", r.out[qrEnd(r.out)+len("\x1b[0m"):])

	// With a web app the QR carries the link, so the link is what comes back under it.
	if err := saveConfig(dir, config{WebURL: "https://app.example"}); err != nil {
		t.Fatal(err)
	}
	r = exec(t, plat, "keys", "add", "bob", "--data-dir", dir)
	link := strings.Fields(r.out[strings.Index(r.out, "https://app.example#"):])[0]
	if n := strings.Count(r.out, link); n != 2 || strings.LastIndex(r.out, link) < qrEnd(r.out) {
		t.Errorf("the link appears %d time(s), last one under the QR: %v\n%s", n, strings.LastIndex(r.out, link) > qrEnd(r.out), r.out)
	}
	// rotate speaks the same way, and there the repeat is the last line of the command.
	rot := exec(t, plat, "keys", "rotate", "bob", "--data-dir", dir)
	rotLink := strings.Fields(rot.out[strings.Index(rot.out, "https://app.example#"):])[0]
	if lines := strings.Fields(strings.TrimSpace(rot.out)); lines[len(lines)-1] != rotLink {
		t.Errorf("rotate does not end with the link to copy:\n%s", rot.out)
	}

	// No QR, nothing repeated: --no-qr, and a pipe.
	r = exec(t, plat, "keys", "add", "carol", "--no-qr", "--data-dir", dir)
	carol := strings.Fields(r.out[strings.Index(r.out, "https://app.example#"):])[0]
	if n := strings.Count(r.out, carol); n != 1 {
		t.Errorf("--no-qr repeated the link %d time(s):\n%s", n, r.out)
	}
	var out, errw lockedBuffer
	if code := run(context.Background(), []string{"keys", "add", "dave", "--data-dir", dir}, &out, &errw, nil, false, plat); code != 0 {
		t.Fatal(errw.String())
	}
	dave := strings.Fields(out.String()[strings.Index(out.String(), "https://app.example#"):])[0]
	if n := strings.Count(out.String(), dave); n != 1 {
		t.Errorf("a pipe got the link %d time(s):\n%s", n, out.String())
	}
}

// Revoke is permanent, so it asks, names the reversible alternative, and does nothing on anything
// but yes (promise 9).
func TestRevokeAsksFirst(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	if r := exec(t, plat, "keys", "add", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	no := execIn(t, plat, "n\n", "keys", "revoke", "alice", "--data-dir", dir)
	for _, want := range []string{"Revoking is permanent", "keys pause alice", "[y/N]", "nothing changed"} {
		if !strings.Contains(no.out, want) {
			t.Errorf("prompt missing %q:\n%s", want, no.out)
		}
	}
	store, _ := keys.NewFileStore(dir)
	if k, _ := store.Find(context.Background(), "alice"); k.Status != keys.Active {
		t.Fatalf("answering no revoked anyway: %s", k.Status)
	}
	if r := execIn(t, plat, "y\n", "keys", "revoke", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	store2, _ := keys.NewFileStore(dir)
	if k, _ := store2.Find(context.Background(), "alice"); k.Status != keys.Revoked {
		t.Fatalf("answering yes did not revoke: %s", k.Status)
	}
}

// A key change must be in force before the command returns, not within the store's once-per-second
// re-read: a host who tests revoke the obvious way concluded revocation was broken (promise 9).
func TestKeyWritesPokeTheRunningHost(t *testing.T) {
	dir, err := os.MkdirTemp("", "bn009-") // short: the admin socket path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	plat := testPlatform(fakeAddr, nil)

	r := exec(t, plat, "keys", "add", "alice", "--json", "--data-dir", dir)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	var minted struct{ Invite string }
	if err := json.Unmarshal([]byte(r.out), &minted); err != nil {
		t.Fatal(err)
	}
	secret := minted.Invite[strings.LastIndex(minted.Invite, ".")+1:]

	// The host the gateway would be reading through, with its admin socket up.
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Serve(dir, func() admin.Status { return admin.Status{} }, store.Reload, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer adm.Close()

	ctx := context.Background()
	if k, ok, err := store.Lookup(ctx, secret); err != nil || !ok || k.Status != keys.Active {
		t.Fatalf("alice should be active: %v %v %v", k, ok, err)
	} // this Lookup arms the once-per-second throttle, which is the window the host complained about

	if r := exec(t, plat, "keys", "revoke", "alice", "--yes", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	k, ok, err := store.Lookup(ctx, secret)
	if err != nil || !ok {
		t.Fatalf("lookup after revoke: %v %v", ok, err)
	}
	if k.Status != keys.Revoked {
		t.Fatalf("the running host still says %s straight after revoke; the reload poke did not land", k.Status)
	}
	// pause and resume travel the same way.
	if r := exec(t, plat, "keys", "resume", "alice", "--data-dir", dir); r.code != 0 {
		t.Fatal(r.err)
	}
	if k, _, _ := store.Lookup(ctx, secret); k.Status != keys.Active {
		t.Fatalf("resume did not reach the running host: %s", k.Status)
	}
}

// `status` reads the same words off the admin API: an engine nobody has met and one that stopped
// answering, with since when (ticket 011).
func TestStatusWordsForTheEngineState(t *testing.T) {
	var out lockedBuffer
	writeStatus(&out, admin.Status{Upstream: admin.Upstream{Kind: "unknown", URL: "http://127.0.0.1:1", Since: time.Now().Add(-12 * time.Second)}})
	if s := out.String(); !strings.Contains(s, "upstream  (not identified yet)  http://127.0.0.1:1  NOT ANSWERING for 12s") {
		t.Errorf("status for an unknown engine:\n%s", s)
	}
	out.Reset()
	writeStatus(&out, admin.Status{Upstream: admin.Upstream{Kind: "llama.cpp", URL: "http://127.0.0.1:8080", Healthy: true, Slots: 2}})
	if s := out.String(); !strings.Contains(s, "upstream  llama.cpp  http://127.0.0.1:8080  healthy") {
		t.Errorf("status for a healthy engine:\n%s", s)
	}
}

func TestNoEnginePointsToEngineQuickstart(t *testing.T) {
	// Cancellation makes every signature probe fail even on a developer's machine with engines.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output lockedBuffer
	e := env{errw: &output}
	_, err := e.openUpstream(ctx, t.TempDir(), "", "", 0)
	if !errors.Is(err, upstream.ErrNoUpstream) {
		t.Fatalf("error = %v, want no engine", err)
	}
	want := "Start llama.cpp, llama-swap, Ollama, LM Studio or vLLM: https://github.com/infercat/infercat#no-engine-yet\n"
	if output.String() != want {
		t.Fatalf("hint = %q, want %q", output.String(), want)
	}
}

func TestServeRemembersModelPinAndAllClearsIt(t *testing.T) {
	dir, err := os.MkdirTemp("", "ic066-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := store.Add(context.Background(), "friend", keys.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			io.WriteString(w, `{"total_slots":1,"default_generation_settings":{"n_ctx":4096}}`)
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"private"},{"id":"public"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--models", " public, offline "}, "public,offline"},
		{nil, "public,offline"},
		{[]string{"--models", "all"}, ""},
		{nil, ""},
	} {
		gw := newFakeGateway()
		plat := testPlatform(fakeAddr, nil)
		plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { return fakeTunnel{}, nil }
		var handler http.Handler
		plat.newGateway = func(o gatewayOptions, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) (gatewayServer, error) {
			wired, err := newPlatform().newGateway(o, up, store, rec, logf)
			if err != nil {
				return nil, err
			}
			handler = wired.(interface{ Handler() http.Handler }).Handler()
			return gw, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		var out, errw lockedBuffer
		done := make(chan int, 1)
		args := append([]string{"serve", "--console", "off", "--data-dir", dir, "--upstream", engine.URL}, tc.args...)
		go func() { done <- run(ctx, args, &out, &errw, nil, false, plat) }()
		select {
		case <-gw.serving:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatalf("serve not ready: %s", errw.String())
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 200 || strings.Contains(response.Body.String(), `"private"`) != (tc.want == "") {
			t.Errorf("serve pin did not reach the gateway: %d %s", response.Code, response.Body.String())
		}
		cfg, cfgErr := loadConfig(dir)
		st, statusErr := admin.Fetch(ctx, dir)
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Fatalf("serve: %d %s", code, errw.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serve did not stop")
		}
		t.Logf("remembered=%q; status.models_pinned=%q; GET /v1/models=%s", cfg.Models, strings.Join(st.ModelsPinned, ","), response.Body.String())
		if cfgErr != nil || cfg.Models != tc.want {
			t.Fatalf("config: %+v %v, want %q", cfg, cfgErr, tc.want)
		}
		if statusErr != nil || strings.Join(st.ModelsPinned, ",") != tc.want {
			t.Fatalf("status: %+v %v", st.ModelsPinned, statusErr)
		}
		var display lockedBuffer
		writeStatus(&display, st)
		for name, text := range map[string]string{"banner": out.String(), "status": display.String()} {
			if strings.Contains(text, "(pinned)") != (tc.want != "") {
				t.Fatalf("%s pin indication: %s", name, text)
			}
		}
		if tc.want != "" && !strings.Contains(out.String(), "not currently reported by the engine: offline") {
			t.Fatalf("unreported id: %s", out.String())
		}
		if tc.want == "" && strings.Contains(string(readFile(t, filepath.Join(dir, configName))), `"models"`) {
			t.Fatal("all left a remembered pin")
		}
	}
}

func (g *fakeGateway) SetRuns(*runstate.Manager) error { return nil }
func (g *fakeGateway) ExecuteStep(context.Context, string, runstate.Step, func() error) (runstate.StepResult, error) {
	return runstate.StepResult{}, nil
}

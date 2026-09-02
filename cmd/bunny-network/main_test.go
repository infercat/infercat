package main

import (
	"bytes"
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

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/product"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
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
	var out, errw bytes.Buffer
	code := run(context.Background(), args, &out, &errw, plat)
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
		{"keys revoke alice", "revoked"},
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
	var buf bytes.Buffer
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
	rec.Record(context.Background(), usage.Event{TS: now, KeyID: id, Status: 200, PromptTokens: 100, CompletionTokens: 20, TTFTMS: 120, TotalMS: 3100})
	rec.Record(context.Background(), usage.Event{TS: now, KeyID: id, Status: 429, Code: "rate_limited"})
	rec.Record(context.Background(), usage.Event{TS: now.Add(-72 * time.Hour), KeyID: id, Status: 200, PromptTokens: 999})
	rec.Close()

	r := exec(t, plat, "usage", "--data-dir", dir)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	for _, want := range []string{"requests  2", "rate_limited 1", "100 prompt", "120 ms", "3.1 s", "alice"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("usage output missing %q:\n%s", want, r.out)
		}
	}
	// The 72-hour-old event is outside the default window but inside --since all.
	if all := exec(t, plat, "usage", "--since", "all", "--data-dir", dir); !strings.Contains(all.out, "requests  3") {
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
	want := config{Upstream: "http://127.0.0.1:8000", Slots: 4, QueueTimeout: "45s", MaxBody: 1024, Name: "max"}
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
	if durOr("", time.Second) != time.Second || durOr("nonsense", time.Second) != time.Second || durOr("2m", time.Second) != 2*time.Minute {
		t.Error("durOr")
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
	serving chan struct{} // closed when Serve is first called
	done    chan struct{} // closed by Shutdown; Serve blocks until then
	once    sync.Once
	slots   atomic.Int32 // last SetSlots value
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{serving: make(chan struct{}), done: make(chan struct{})}
}

func (g *fakeGateway) Serve(net.Listener) error {
	g.once.Do(func() { close(g.serving) })
	<-g.done
	return nil
}
func (g *fakeGateway) ServeDev(string) error                     { return nil }
func (g *fakeGateway) Shutdown(context.Context) error            { close(g.done); return nil }
func (g *fakeGateway) Counters(string) usage.KeyCounters         { return usage.KeyCounters{} }
func (g *fakeGateway) AllCounters() map[string]usage.KeyCounters { return nil }
func (g *fakeGateway) Queue() (int, int)                         { return 0, 0 }
func (g *fakeGateway) SetSlots(n int)                            { g.slots.Store(int32(n)) }

type fakeTunnel struct{}

func (fakeTunnel) Listener() net.Listener { return nil }
func (fakeTunnel) Addr() string           { return fakeAddr }
func (fakeTunnel) Status() tunnelStatus   { return tunnelStatus{Addr: fakeAddr, Region: "Testville"} }
func (fakeTunnel) Close() error           { return nil }

// Ticket 005 fixes 10b and 10d through the real serve command: the tunnel engine's log lands
// in <data-dir>/tunnel.log and never on the terminal (--verbose flips that), and the gateway
// follows the engine's slot count once a refresh sees the real number.
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
		plat := testPlatform(fakeAddr, nil)
		plat.startTunnel = func(ctx context.Context, o tunnelOptions) (tunnelServer, error) {
			o.Logf("magicsock: disco key = d:test")
			o.Logf("NetworkMap: {\"SelfNode\":1}")
			return fakeTunnel{}, nil
		}
		plat.newGateway = func(gatewayOptions, upstream.Upstream, keys.Store, usage.Recorder, func(string, ...any)) (gatewayServer, error) {
			return gw, nil
		}
		args := []string{"serve", "--data-dir", dir, "--upstream", engine.URL}
		if verbose {
			args = append(args, "--verbose")
		}
		ctx, cancel := context.WithCancel(context.Background())
		var out, errw bytes.Buffer
		code := make(chan int, 1)
		go func() { code <- run(ctx, args, &out, &errw, plat) }()
		select {
		case <-gw.serving:
		case <-time.After(10 * time.Second):
			t.Fatalf("serve never reached the gateway:\n%s%s", out.String(), errw.String())
		}
		engineSlots.Store(3)
		for deadline := time.Now().Add(5 * time.Second); gw.slots.Load() != 3; {
			if time.Now().After(deadline) {
				t.Fatalf("gateway never followed the engine to 3 slots (got %d):\n%s%s", gw.slots.Load(), out.String(), errw.String())
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
	for _, want := range []string{product.Name, "upstream  llama.cpp", "slots 1", "tunnel    " + fakeAddr, "relay     Testville"} {
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

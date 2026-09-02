package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/product"
	// only for tunnel.KeyFile: the host identity's file name is the tunnel's to define, and a
	// second copy of it here would be a lie waiting to happen.
	"github.com/2185Lab/bunny-network/internal/tunnel"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// refreshEvery is how often a running host re-probes the upstream. An engine that was down at
// startup becomes healthy on its own within this interval. A variable so a test can hurry it.
var refreshEvery = 10 * time.Second

// tunnelLogName is where the tunnel engine's own log goes, under the data dir; truncated at
// every start so it never grows without bound.
const tunnelLogName = "tunnel.log"

// drainTimeout is how long Ctrl-C waits for in-flight completions before hanging up.
const drainTimeout = 10 * time.Second

func (e *env) cmdServe(ctx context.Context, pre string, args []string) error {
	dataDir, err := resolveDataDir(peekDataDir(args, pre))
	if err != nil {
		return err
	}
	cfg, err := loadConfig(dataDir)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.String("data-dir", dataDir, dataDirUsage)
	upURL := fs.String("upstream", cfg.Upstream, "inference server URL; detected when empty")
	upKey := fs.String("upstream-key", cfg.UpstreamKey, "bearer token for the inference server")
	slots := fs.Int("slots", cfg.Slots, "parallel requests the engine can serve; 0 asks the engine")
	logPrompts := fs.Bool("log-prompts", false, "record prompts and completions in usage.jsonl (off by default; not remembered)")
	devListen := fs.String("dev-listen", cfg.DevListen, "also serve the gateway on this loopback address, with permissive CORS")
	ephemeral := fs.Bool("ephemeral", false, "do not touch disk for the host key; a new address every run (not remembered)")
	derpMapURL := fs.String("derpmap-url", cfg.DERPMapURL, "relay map URL")
	region := fs.String("region", cfg.Region, "preferred relay region")
	name := fs.String("name", cfg.Name, "host display name your friends see; defaults to this machine's hostname")
	webURLFlag := fs.String("web-url", cfg.WebURL, "where your friends open the web app; invites print as <url>#<invite>")
	verbose := fs.Bool("verbose", false, "print the tunnel engine's log on the terminal instead of tunnel.log (not remembered)")
	if err := e.parse(fs, serveHelp, args); err != nil {
		return err
	}

	*upURL = forgetIfAuto(*upURL)
	if err := saveConfig(dataDir, config{
		Upstream: *upURL, UpstreamKey: *upKey, Slots: *slots,
		DevListen: *devListen, DERPMapURL: *derpMapURL,
		Region: *region, Name: *name, WebURL: *webURLFlag,
	}); err != nil {
		return err
	}
	hostName := hostDisplayName(*name)
	// Whether this run is the one that creates the host identity, checked before the tunnel does
	// it, so the note that explains the file is printed exactly once (ticket 009 promise 8).
	_, keyErr := os.Stat(filepath.Join(dataDir, tunnel.KeyFile))
	newIdentity := errors.Is(keyErr, os.ErrNotExist) && !*ephemeral

	if e.plat.warn != "" {
		e.logf("%s", e.plat.warn)
	}
	if *logPrompts {
		e.logf("--log-prompts is ON: your friends' prompts and completions are being written to %s/%s.", dataDir, usage.FileName)
	}

	up, err := e.openUpstream(ctx, dataDir, *upURL, *upKey, *slots)
	if err != nil {
		return err
	}
	store, err := keys.NewFileStore(dataDir)
	if err != nil {
		return err
	}
	rec, err := usage.NewFileRecorder(dataDir, e.logf)
	if err != nil {
		return err
	}
	defer rec.Close()

	tunLogf, closeTunLog, err := tunnelLogf(dataDir, *verbose, e.logf)
	if err != nil {
		return err
	}
	defer closeTunLog()
	tun, err := e.plat.startTunnel(ctx, tunnelOptions{
		DataDir: dataDir, Ephemeral: *ephemeral, DERPMapURL: *derpMapURL, Region: *region, Logf: tunLogf,
	})
	if err != nil {
		return fmt.Errorf("tunnel: %w", err)
	}
	defer tun.Close()

	gw, err := e.plat.newGateway(gatewayOptions{
		LogPrompts:  *logPrompts,
		HostName:    hostName,
		RelayRegion: func() string { return tun.Status().Region },
	}, up, store, rec, e.logf)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	started := time.Now()
	adm, err := admin.Serve(dataDir, func() admin.Status {
		return buildStatus(ctx, started, tun, up, gw, store)
	}, store.Reload)
	if err != nil {
		return fmt.Errorf("admin API: %w", err)
	}
	defer adm.Close()

	e.printStartup(ctx, startup{
		tun: tun, up: up, store: store, dataDir: dataDir,
		hostName: hostName, webURL: webURL(config{WebURL: *webURLFlag}), newIdentity: newIdentity,
	})

	go refreshLoop(ctx, up, e.logf)

	errc := make(chan error, 2)
	go func() { errc <- gw.Serve(tun.Listener()) }()
	if *devListen != "" {
		go func() { errc <- gw.ServeDev(*devListen) }()
	}

	select {
	case <-ctx.Done():
		fmt.Fprintf(e.out, "\nshutting down (up to %s for in-flight requests)\n", drainTimeout)
	case err := <-errc:
		if err != nil {
			return err
		}
	}

	sctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := gw.Shutdown(sctx); err != nil {
		e.logf("gateway shutdown: %v", err)
	}
	tun.Close()
	adm.Close()
	rec.Close()
	return nil
}

// openUpstream honours an explicit --upstream (unreachable is a warning) and otherwise runs
// detection (nothing found is fatal, because there is nothing to proxy to).
func (e *env) openUpstream(ctx context.Context, dataDir, url, key string, slots int) (upstream.Upstream, error) {
	var up upstream.Upstream
	var err error
	if url != "" {
		up, err = upstream.Open(ctx, url, key)
	} else {
		up, err = upstream.Detect(ctx, key)
	}
	if err != nil {
		// Detection found nothing: end with the command that fixes it, and say where the flag
		// goes, because `--upstream` reads like a subcommand to a first-time host (promise 5).
		return nil, fmt.Errorf("%w — like this, as a flag of `serve`:\n\n  %s serve --upstream http://127.0.0.1:<port>", err, product.CLIName)
	}
	if s, ok := up.(upstream.Slotted); ok && slots > 0 {
		s.SetSlots(slots)
	}
	if !up.Info().Health.OK {
		e.logf("WARNING: %s is not answering. Friends get 503 upstream_down until it does; retrying every %s.", up.Info().URL, refreshEvery)
		e.logf("         It is remembered in %s — `%s serve --upstream auto` detects again and forgets it.", configPath(dataDir), product.CLIName)
	}
	return up, nil
}

// forgetIfAuto turns `--upstream auto` into "no remembered upstream": detection runs again this
// run, and the empty value is what gets persisted, so the mistyped URL is gone for good. It is
// the documented way out of a remembered upstream that never answers (ticket 009 promise 10).
func forgetIfAuto(url string) string {
	if strings.EqualFold(strings.TrimSpace(url), "auto") {
		return ""
	}
	return url
}

// hostDisplayName is what /me shows a friend: the host's --name, else this machine's hostname
// without the mDNS suffix, else empty (and the client then omits the sentence).
func hostDisplayName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(h), ".local")
}

// tunnelLogf routes the tunnel engine's chatter (wgengine, magicsock, netstack, …) to
// <data-dir>/tunnel.log so the terminal shows only the product's own lines; --verbose sends it
// to the terminal instead (ticket 005 fix 10b).
func tunnelLogf(dataDir string, verbose bool, terminal func(string, ...any)) (func(string, ...any), func(), error) {
	if verbose {
		return terminal, func() {}, nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, tunnelLogName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("tunnel log: %w", err)
	}
	return log.New(f, "", log.LstdFlags|log.Lmicroseconds).Printf, func() { f.Close() }, nil
}

// refreshLoop re-probes the engine; when it comes or goes, or reports a different slot count,
// that is logged once. Nothing is pushed anywhere: the gateway's queue reads the engine's slot
// count at every decision (ticket 010, DESIGN §1.5), so an engine down at startup cannot pin it.
func refreshLoop(ctx context.Context, up upstream.Upstream, logf func(string, ...any)) {
	t := time.NewTicker(refreshEvery)
	defer t.Stop()
	was := up.Info().Health.OK
	slots := up.Info().Slots
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := up.Refresh(ctx)
			info := up.Info()
			if info.Health.OK != was {
				if info.Health.OK {
					logf("upstream is back: %s (%s)", info.URL, info.Kind)
				} else {
					logf("upstream went away: %v", err)
				}
				was = info.Health.OK
			}
			if err == nil && info.Slots > 0 && info.Slots != slots {
				logf("engine slots: %d (was %d)", info.Slots, slots)
				slots = info.Slots
			}
		}
	}
}

// startup is everything the banner names. It is a struct because the banner grew the four lines
// the strangers asked for (web, name, access, data) and a positional list of seven was worse.
type startup struct {
	tun         tunnelServer
	up          upstream.Upstream
	store       keys.Store
	dataDir     string
	hostName    string
	webURL      string
	newIdentity bool // this run created host.key.json: explain the file, once
}

// printStartup writes the block docs the ticket fixes the order of: product, upstream, tunnel,
// relay, then — added by ticket 009 — where friends open the app, the name they see, exactly what
// they can reach, where the host's own files live, and finally what to do next.
func (e *env) printStartup(ctx context.Context, s startup) {
	info := s.up.Info()
	health := ""
	if !info.Health.OK {
		health = "  " + healthWord(false, info.Health.Since)
	}
	fmt.Fprintf(e.out, "%s %s\n", product.Name, product.Version)
	fmt.Fprintf(e.out, "upstream  %s  %s%s\n", kindWord(string(info.Kind)), info.URL, health)
	fmt.Fprintf(e.out, "          %s  context %s  slots %d\n", modelList(info.Models), contextStr(info.ModelContext), info.Slots)
	fmt.Fprintf(e.out, "tunnel    %s\n", orDash(s.tun.Addr()))
	fmt.Fprintf(e.out, "relay     %s\n", orDash(s.tun.Status().Region))
	if s.webURL != "" {
		fmt.Fprintf(e.out, "web       %s  (your friends open this and paste their invite)\n", s.webURL)
	}
	if s.hostName != "" {
		fmt.Fprintf(e.out, "name      %s  (shown to your friends)\n", s.hostName)
	}
	fmt.Fprintf(e.out, "access    friends reach only %s on %s\n", friendRoutes, info.URL)
	fmt.Fprintf(e.out, "          nothing else on this machine — no other port, no files\n")
	fmt.Fprintf(e.out, "data      %s\n", s.dataDir)
	if s.newIdentity {
		fmt.Fprintf(e.out, "          wrote %s — this is your host identity. Back it up; don't sync it to\n", tunnel.KeyFile)
		fmt.Fprintf(e.out, "          Dropbox or a dotfiles repo; deleting it invalidates every invite you send.\n")
	}
	list, err := s.store.List(ctx)
	if err != nil {
		e.logf("keys: %v", err)
		return
	}
	active := 0
	for _, k := range list {
		if k.Status == keys.Active {
			active++
		}
	}
	switch {
	case len(list) == 0:
		fmt.Fprintf(e.out, "\nMint a friend: %s keys add <name>\n", product.CLIName)
	case active == 0:
		fmt.Fprintf(e.out, "\n%s, none active — all paused or revoked; `%s keys add <name>` invites someone\n",
			plural(len(list), "key"), product.CLIName)
	case active == len(list):
		fmt.Fprintf(e.out, "\n%s active\n", plural(active, "key"))
	default:
		fmt.Fprintf(e.out, "\n%s, %d active\n", plural(len(list), "key"), active)
	}
}

// friendRoutes is the whole of what the tunnel exposes, checked against internal/gateway's router
// (request.go serve): /me and /healthz are answered by the gateway itself and never touch the
// engine, so the sentence names the three routes that reach it.
const friendRoutes = "/v1/models, /v1/chat/completions and /v1/embeddings"

func buildStatus(ctx context.Context, started time.Time, tun tunnelServer, up upstream.Upstream, gw gatewayServer, store keys.Store) admin.Status {
	ts := tun.Status()
	info := up.Info()
	inFlight, waiting := gw.Queue()
	st := admin.Status{
		UptimeS:  int64(time.Since(started).Seconds()),
		Tunnel:   admin.Tunnel{Addr: ts.Addr, Region: ts.Region, Clients: ts.Clients},
		Upstream: admin.Upstream{Kind: string(info.Kind), URL: info.URL, Healthy: info.Health.OK, Since: info.Health.Since, ModelContext: info.ModelContext, Slots: info.Slots},
		Queue:    admin.Queue{InFlight: inFlight, Waiting: waiting},
		Keys:     []admin.Key{},
	}
	list, err := store.List(ctx)
	if err != nil {
		return st
	}
	counters := gw.AllCounters()
	for _, k := range list {
		c := counters[k.ID]
		st.Keys = append(st.Keys, admin.Key{
			ID: k.ID, Name: k.Name, Status: string(k.Status),
			InFlight: c.InFlight, RPMUsed: c.RPMUsed, TodayTokens: c.TodayTokens, LastSeen: c.LastSeen,
		})
	}
	return st
}

func modelList(m []string) string {
	switch len(m) {
	case 0:
		return "(no models reported)"
	case 1:
		return m[0]
	default:
		return fmt.Sprintf("%s (+%d more)", m[0], len(m)-1)
	}
}

func contextStr(n int) string {
	if n <= 0 {
		return "unknown"
	}
	return fmt.Sprint(n)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

const serveHelp = `Usage: bunny-network serve [flags]

Runs the host: finds your inference server, opens the tunnel, and serves the gateway inside it.
Flags you pass are remembered in config.json, so the next serve needs none of them.

  bunny-network serve
  bunny-network serve --upstream http://127.0.0.1:8000 --slots 4
  bunny-network serve --dev-listen 127.0.0.1:9090     # also on loopback, for web development

Ctrl-C drains in-flight requests for up to 10s, then stops.

Flags:
  --upstream URL          inference server; detected when absent, in the order
                          llama.cpp :8080, Ollama :11434, LM Studio :1234, vLLM :8000.
                          --upstream auto forgets a remembered URL and detects again
  --upstream-key TOKEN    bearer token for the inference server
  --slots N               parallel requests the engine can serve (0 = ask the engine)
  --dev-listen ADDR       also serve on this loopback address, permissive CORS
  --derpmap-url URL       relay map URL
  --region NAME           preferred relay region
  --name NAME             host display name your friends see   (default: this machine's hostname)
  --web-url URL           where your friends open the web app; invites then print as a link
  --log-prompts           write prompts and completions to usage.jsonl.
                          Off by default and never remembered: your friends' conversations
                          are theirs. Turn it on only to debug, one run at a time.
  --ephemeral             never write the host key; a new address every run (not remembered)
  --verbose               print the tunnel engine's log on the terminal instead of
                          <data-dir>/tunnel.log (not remembered)
  --data-dir DIR          where this host's files live:
                            host.key.json  your host identity — every invite points at it.
                                           Back it up; don't sync it; deleting it kills every invite.
                            keys.json      one entry per friend, secrets stored only as hashes
                            usage.jsonl    one line per request; no prompt text unless --log-prompts
                            config.json    the flags above, remembered
                            tunnel.log     the tunnel engine's own log, truncated at every start
                            admin.sock     how keys, status and usage talk to a running host

There is no daemon mode: run it under your supervisor of choice (launchd, systemd, tmux).
`

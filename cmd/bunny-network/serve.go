package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/product"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// refreshEvery is how often a running host re-probes the upstream. An engine that was down at
// startup becomes healthy on its own within this interval.
const refreshEvery = 10 * time.Second

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
	queueTimeout := fs.Duration("queue-timeout", durOr(cfg.QueueTimeout, 30*time.Second), "how long a request may wait for a slot")
	requestTimeout := fs.Duration("request-timeout", durOr(cfg.RequestTimeout, 300*time.Second), "how long one request may take")
	maxBody := fs.Int64("max-body", int64Or(cfg.MaxBody, 4<<20), "largest request body in bytes")
	logPrompts := fs.Bool("log-prompts", false, "record prompts and completions in usage.jsonl (off by default; not remembered)")
	devListen := fs.String("dev-listen", cfg.DevListen, "also serve the gateway on this loopback address, with permissive CORS")
	ephemeral := fs.Bool("ephemeral", false, "do not touch disk for the host key; a new address every run (not remembered)")
	derpMapURL := fs.String("derpmap-url", cfg.DERPMapURL, "relay map URL")
	region := fs.String("region", cfg.Region, "preferred relay region")
	name := fs.String("name", cfg.Name, "host display name your friends see")
	if err := e.parse(fs, serveHelp, args); err != nil {
		return err
	}

	if err := saveConfig(dataDir, config{
		Upstream: *upURL, UpstreamKey: *upKey, Slots: *slots,
		QueueTimeout: queueTimeout.String(), RequestTimeout: requestTimeout.String(),
		MaxBody: *maxBody, DevListen: *devListen, DERPMapURL: *derpMapURL,
		Region: *region, Name: *name,
	}); err != nil {
		return err
	}

	if e.plat.warn != "" {
		e.logf("%s", e.plat.warn)
	}
	if *logPrompts {
		e.logf("--log-prompts is ON: your friends' prompts and completions are being written to %s/%s.", dataDir, usage.FileName)
	}

	up, err := e.openUpstream(ctx, *upURL, *upKey, *slots)
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

	tun, err := e.plat.startTunnel(ctx, tunnelOptions{
		DataDir: dataDir, Ephemeral: *ephemeral, DERPMapURL: *derpMapURL, Region: *region, Logf: e.logf,
	})
	if err != nil {
		return fmt.Errorf("tunnel: %w", err)
	}
	defer tun.Close()

	gw, err := e.plat.newGateway(gatewayOptions{
		Slots:          up.Info().Slots,
		QueueTimeout:   *queueTimeout,
		RequestTimeout: *requestTimeout,
		MaxBody:        *maxBody,
		LogPrompts:     *logPrompts,
		HostName:       *name,
		RelayRegion:    func() string { return tun.Status().Region },
	}, up, store, rec, e.logf)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	started := time.Now()
	adm, err := admin.Serve(dataDir, func() admin.Status {
		return buildStatus(ctx, started, tun, up, gw, store)
	})
	if err != nil {
		return fmt.Errorf("admin API: %w", err)
	}
	defer adm.Close()

	e.printStartup(ctx, tun, up, store)

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
func (e *env) openUpstream(ctx context.Context, url, key string, slots int) (upstream.Upstream, error) {
	var up upstream.Upstream
	var err error
	if url != "" {
		up, err = upstream.Open(ctx, url, key)
	} else {
		up, err = upstream.Detect(ctx)
	}
	if err != nil {
		return nil, err
	}
	if s, ok := up.(upstream.Slotted); ok && slots > 0 {
		s.SetSlots(slots)
	}
	if !up.Info().Healthy {
		e.logf("WARNING: %s is not answering. Friends get 503 upstream_down until it does; retrying every %s.", up.Info().URL, refreshEvery)
	}
	return up, nil
}

func refreshLoop(ctx context.Context, up upstream.Upstream, logf func(string, ...any)) {
	t := time.NewTicker(refreshEvery)
	defer t.Stop()
	was := up.Info().Healthy
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := up.Refresh(ctx)
			now := up.Info().Healthy
			if now != was {
				if now {
					logf("upstream is back: %s", up.Info().URL)
				} else {
					logf("upstream went away: %v", err)
				}
				was = now
			}
		}
	}
}

// printStartup writes the block docs the ticket fixes the order of: product, upstream, tunnel,
// relay, then what to do next.
func (e *env) printStartup(ctx context.Context, tun tunnelServer, up upstream.Upstream, store keys.Store) {
	info := up.Info()
	health := ""
	if !info.Healthy {
		health = "  (not answering)"
	}
	fmt.Fprintf(e.out, "%s %s\n", product.Name, product.Version)
	fmt.Fprintf(e.out, "upstream  %s  %s%s\n", info.Kind, info.URL, health)
	fmt.Fprintf(e.out, "          %s  context %s  slots %d\n", modelList(info.Models), contextStr(info.ModelContext), info.Slots)
	fmt.Fprintf(e.out, "tunnel    %s\n", orDash(tun.Addr()))
	fmt.Fprintf(e.out, "relay     %s\n", orDash(tun.Status().Region))
	list, err := store.List(ctx)
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
	case active == len(list):
		fmt.Fprintf(e.out, "\n%d keys active\n", active)
	default:
		fmt.Fprintf(e.out, "\n%d keys, %d active\n", len(list), active)
	}
}

func buildStatus(ctx context.Context, started time.Time, tun tunnelServer, up upstream.Upstream, gw gatewayServer, store keys.Store) admin.Status {
	ts := tun.Status()
	info := up.Info()
	inFlight, waiting := gw.Queue()
	st := admin.Status{
		UptimeS:  int64(time.Since(started).Seconds()),
		Tunnel:   admin.Tunnel{Addr: ts.Addr, Region: ts.Region, Clients: ts.Clients},
		Upstream: admin.Upstream{Kind: string(info.Kind), URL: info.URL, Healthy: info.Healthy, ModelContext: info.ModelContext, Slots: info.Slots},
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
                          llama.cpp :8080, Ollama :11434, LM Studio :1234, vLLM :8000
  --upstream-key TOKEN    bearer token for the inference server
  --slots N               parallel requests the engine can serve (0 = ask the engine)
  --queue-timeout D       how long a request may wait for a slot   (default 30s)
  --request-timeout D     how long one request may take            (default 5m0s)
  --max-body BYTES        largest request body                     (default 4194304)
  --dev-listen ADDR       also serve on this loopback address, permissive CORS
  --derpmap-url URL       relay map URL
  --region NAME           preferred relay region
  --name NAME             host display name your friends see
  --log-prompts           write prompts and completions to usage.jsonl.
                          Off by default and never remembered: your friends' conversations
                          are theirs. Turn it on only to debug, one run at a time.
  --ephemeral             never write the host key; a new address every run (not remembered)
  --data-dir DIR          where keys, usage, config, and the host key live

There is no daemon mode: run it under your supervisor of choice (launchd, systemd, tmux).
`

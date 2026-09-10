package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	consolebundle "github.com/infercat/infercat/console"
	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/bridge"
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/product"
	runstate "github.com/infercat/infercat/internal/run"
	// only for tunnel.KeyFile: the host identity's file name is the tunnel's to define, and a
	// second copy of it here would be a lie waiting to happen.
	"github.com/infercat/infercat/internal/tunnel"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
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
	consoleDefault := cfg.Console
	if consoleDefault == "" {
		consoleDefault = "127.0.0.1:9101"
	}
	consoleAddr := fs.String("console", consoleDefault, "console loopback IP:port; off disables")
	fs.String("data-dir", dataDir, dataDirUsage)
	upURL := fs.String("upstream", cfg.Upstream, "inference server URL; detected when empty")
	upKey := fs.String("upstream-key", cfg.UpstreamKey, "bearer token for the inference server")
	transcribeModel := fs.String("upstream-transcribe-model", cfg.UpstreamTranscribeModel, "host's default transcription model")
	speechModel := fs.String("upstream-speech-model", cfg.UpstreamSpeechModel, "host's default speech model")
	transcribeURL := fs.String("upstream-transcribe", cfg.UpstreamTranscribe, "explicit OpenAI transcription engine URL")
	transcribeKey := fs.String("upstream-transcribe-key", cfg.UpstreamTranscribeKey, "transcription engine bearer")
	speechURL := fs.String("upstream-speech", cfg.UpstreamSpeech, "explicit OpenAI speech engine URL")
	speechKey := fs.String("upstream-speech-key", cfg.UpstreamSpeechKey, "speech engine bearer")
	maxAudio := fs.Int("max-transcription-seconds", intOr(cfg.MaxTranscriptionSeconds, 300), "maximum measured duration and unknown-duration reservation")
	models := fs.String("models", cfg.Models, "comma-separated host model pin; all forgets it")
	slots := fs.Int("slots", cfg.Slots, "parallel requests the engine can serve; 0 asks the engine")
	logPrompts := fs.Bool("log-prompts", false, "record prompts and completions in usage.jsonl (off by default; not remembered)")
	devListen := fs.String("dev-listen", cfg.DevListen, "also serve the gateway on this loopback address, with permissive CORS")
	ephemeral := fs.Bool("ephemeral", false, "do not touch disk for the host key; a new address every run (not remembered)")
	derpMapURL := fs.String("derpmap-url", cfg.DERPMapURL, "relay map URL")
	region := fs.String("region", cfg.Region, "preferred relay region")
	name := fs.String("name", cfg.Name, "host display name your friends see; defaults to this machine's hostname")
	webURLFlag := fs.String("web-url", cfg.WebURL, "where your friends open the web app; invites print as <url>#<invite>")
	verbose := fs.Bool("verbose", false, "print the tunnel engine's log on the terminal instead of tunnel.log (not remembered)")
	logRequests := fs.Bool("log-requests", false, "print one line per completed request on this terminal (not remembered; never prompt content)")
	if err := e.parse(fs, serveHelp, args); err != nil {
		return err
	}

	if *maxAudio <= 0 {
		return errors.New("--max-transcription-seconds must be positive")
	}
	transcribe, err := upstream.OpenAudio(ctx, *transcribeURL, *transcribeKey)
	if err != nil {
		return err
	}
	speech, err := upstream.OpenAudio(ctx, *speechURL, *speechKey)
	if err != nil {
		return err
	}
	consoleListener, err := admin.ListenConsole(*consoleAddr)
	if err != nil {
		return err
	}
	consoleAddress := ""
	if consoleListener != nil {
		defer consoleListener.Close()
		consoleAddress = consoleListener.Addr().String()
	}
	if strings.TrimSpace(*models) == "all" {
		*models = ""
	}
	pinned := splitModels(*models)
	*upURL = forgetIfAuto(*upURL)
	if err := saveConfig(dataDir, config{
		Upstream: *upURL, UpstreamKey: *upKey, Slots: *slots, Models: strings.Join(pinned, ","),
		UpstreamTranscribeModel: *transcribeModel, UpstreamSpeechModel: *speechModel,
		UpstreamTranscribe: *transcribeURL, UpstreamTranscribeKey: *transcribeKey, UpstreamSpeech: *speechURL, UpstreamSpeechKey: *speechKey, MaxTranscriptionSeconds: *maxAudio,
		DevListen: *devListen, DERPMapURL: *derpMapURL,
		Region: *region, Name: *name, WebURL: *webURLFlag, Console: *consoleAddr,
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
	events := admin.NewEvents(rec) // the live copy of every event (029); rec keeps the file

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

	remoteStore, err := adminkey.Open(dataDir)
	if err != nil {
		return err
	}
	state := &consoleState{remote: remoteStore, value: consoleSettings{
		WritesSupported: true, DefaultWebURL: product.WebURL, Name: hostName, WebURL: webURL(config{WebURL: *webURLFlag}), Slots: *slots,
		ConfiguredConsole: *consoleAddr, ConsoleAddress: consoleAddress,
		LogRequests: *logRequests, DataDir: dataDir, LogPrompts: *logPrompts,
		Upstream: *upURL, DERPMapURL: *derpMapURL, Region: *region, ConfiguredWebURL: *webURLFlag,
	}}
	state.logs.Store(*logRequests)
	// Subscribe before serving even when disabled; the live switch gates printing, never recording.
	requestLines, stopLines := events.Subscribe()
	defer stopLines()
	remoteHandler := gateway.Console(remoteStore, consoleAddress, func() string {
		b, _ := os.ReadFile(filepath.Join(dataDir, admin.TokenName))
		return strings.TrimSpace(string(b))
	}, events, e.logf)

	public := bridge.Manager{Keys: store, Slots: func() int { return up.Info().Slots }}
	defer public.Close()
	gw, err := e.plat.newGateway(gatewayOptions{
		RemoteConsole: remoteHandler, LiveHostName: state.name,
		ModelsPinned:    pinned,
		TranscribeModel: *transcribeModel, SpeechModel: *speechModel,
		Transcribe: transcribe, Speech: speech, MaxTranscriptionSeconds: float64(*maxAudio),
		LogPrompts:  *logPrompts,
		HostName:    hostName,
		RelayRegion: func() string { return tun.Status().Region },
		DataDir:     dataDir, // today's counters are seeded from usage.jsonl there
	}, up, store, bridge.Recorder{Next: events, Count: public.Count}, e.logf)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	runStore, err := runstate.NewStore(dataDir)
	if err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	runs, err := runstate.New(runStore, gw.ExecuteStep, nil)
	if err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	defer runs.Close()
	if err = runs.Sweep(); err != nil {
		return fmt.Errorf("run expiry: %w", err)
	}
	gw.SetRuns(runs)
	runs.Start()

	reload := func() error {
		if err := store.Reload(); err != nil {
			return err
		}
		return public.Reload(ctx, dataDir, gw.Handler(), e.logf)
	}
	if err := reload(); err != nil {
		return fmt.Errorf("bridge: %w", err)
	}
	tele := &telemetry{events: events, name: hostName}
	go tele.sample(ctx, gw, tun)
	if requestLines != nil {
		go printRequests(ctx, requestLines, e.out, keyNamer(ctx, store), state.logs.Load)
	}
	started := time.Now()
	adm, err := admin.Serve(dataDir, func() admin.Status {
		st := buildStatus(ctx, started, tun, up, gw, store, tele)
		st.Bridge = public.Status()
		st.Console = consoleAddress
		st.Name = state.name()
		remoteState := remoteStore.State()
		st.Remote = &remoteState
		st.ModelsPinned = pinned
		st.Audio = audioStatus(transcribe, speech)
		return st
	}, func() error {
		err := reload()
		for _, a := range []upstream.AudioEngine{transcribe, speech} {
			if a != nil {
				err = errors.Join(err, a.Refresh(ctx))
			}
		}
		return err
	}, events, admin.WithRuns(e.consoleAPI(store, tun.Addr(), up, state.value, state), runStore.List))
	if err != nil {
		return fmt.Errorf("admin API: %w", err)
	}
	defer adm.Close()
	if consoleListener != nil {
		server := adm.ServeConsole(consoleListener, consolebundle.Files())
		defer server.Close()
	}

	e.printStartup(ctx, startup{
		transcribe: transcribe, speech: speech, remoteWarning: remoteStore.Warning(),
		tun: tun, up: up, store: store, dataDir: dataDir, consoleAddr: consoleAddress, pinned: pinned,
		hostName: hostName, webURL: webURL(config{WebURL: *webURLFlag}), newIdentity: newIdentity,
	})

	go refreshLoop(ctx, up, e.logf, refreshEvery)

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

	runs.Close() // Cancel steps and let SSE readers leave before the gateway drain.
	sctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := gw.Shutdown(sctx); err != nil {
		e.logf("gateway shutdown: %v", err)
	}
	if err := tun.Close(); err != nil {
		e.logf("%v", err)
	}
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
		if errors.Is(err, upstream.ErrNoUpstream) {
			e.logf("Start llama.cpp, llama-swap, Ollama, LM Studio or vLLM: https://github.com/infercat/infercat#no-engine-yet")
		}
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
func refreshLoop(ctx context.Context, up upstream.Upstream, logf func(string, ...any), every time.Duration) {
	t := time.NewTicker(every)
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
	transcribe, speech upstream.AudioEngine
	pinned             []string
	consoleAddr        string
	remoteWarning      string
	tun                tunnelServer
	up                 upstream.Upstream
	store              keys.Store
	dataDir            string
	hostName           string
	webURL             string
	newIdentity        bool // this run created host.key.json: explain the file, once
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
	models := modelList(info.Models)
	if len(s.pinned) > 0 {
		models = modelList(s.pinned) + " (pinned)"
	}
	fmt.Fprintf(e.out, "          %s  context %s  slots %d\n", models, contextStr(info.ModelContext), info.Slots)
	var missing []string
	for _, id := range s.pinned {
		if !slices.Contains(info.Models, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(e.out, "          not currently reported by the engine: %s\n", modelList(missing))
	}
	fmt.Fprintf(e.out, "tunnel    %s\n", orDash(s.tun.Addr()))
	fmt.Fprintf(e.out, "relay     %s\n", orDash(s.tun.Status().Region))
	if s.webURL != "" {
		fmt.Fprintf(e.out, "web       %s  (your friends open this and paste their invite)\n", s.webURL)
	}
	if s.hostName != "" {
		fmt.Fprintf(e.out, "name      %s  (shown to your friends)\n", s.hostName)
	}
	routes := friendRoutes(s.transcribe != nil, s.speech != nil)
	for _, route := range []string{"transcriptions", "speech"} {
		if a, ok := audioStatus(s.transcribe, s.speech)[route]; ok {
			fmt.Fprintf(e.out, "audio     /v1/audio/%s  %s  %s\n", route, a.URL, healthWord(a.Healthy, a.Since))
		}
	}
	if s.transcribe == nil && s.speech == nil {
		fmt.Fprintf(e.out, "access    friends reach only %s on %s\n", routes, info.URL)
	} else {
		fmt.Fprintf(e.out, "access    friends reach only %s (engines above)\n", routes)
	}
	fmt.Fprintf(e.out, "          nothing else on this machine — no other port, no files\n")
	fmt.Fprintf(e.out, "data      %s\n", s.dataDir)
	if s.remoteWarning != "" {
		fmt.Fprintln(e.out, s.remoteWarning)
	}
	if s.consoleAddr != "" {
		fmt.Fprintf(e.out, "console   http://%s  (open it with: infercat console)\n", s.consoleAddr)
	}
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
func friendRoutes(transcribe, speech bool) string {
	routes := []string{"/v1/models", "/v1/chat/completions", "/v1/embeddings"}
	if transcribe {
		routes = append(routes, "/v1/audio/transcriptions")
	}
	if speech {
		routes = append(routes, "/v1/audio/speech")
	}
	return strings.Join(routes[:len(routes)-1], ", ") + " and " + routes[len(routes)-1]
}

// telemetry is what the admin status reads beyond the seams' own snapshots (029): the event hub
// for tokens per second, the sampled slot peak, and the host's display name.
type telemetry struct {
	events *admin.Events
	name   string
	peak   atomic.Int32
}

// sample runs once a second: the slot peak (the queue reports an exact now; a high-water mark
// inside it would be a gateway change beyond a read-only accessor, so the peak is sampled and
// says so in its name) and a Peers call, which is what stamps a new client's first-seen time.
func (t *telemetry) sample(ctx context.Context, gw gatewayServer, tun tunnelServer) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if n, _ := gw.Queue(); int32(n) > t.peak.Load() {
				t.peak.Store(int32(n))
			}
			tun.Peers()
		}
	}
}

// printRequests is `serve --log-requests`: the request line on the host's terminal, from the
// same stream `status --watch` reads.
func printRequests(ctx context.Context, ch <-chan usage.Event, w io.Writer, name func(string) string, enabled ...func() bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if len(enabled) > 0 && !enabled[0]() {
				continue
			}
			fmt.Fprintln(w, requestLine(ev, name(ev.KeyID)))
		}
	}
}

// keyNamer resolves key ids to names for the request line, re-reading the store only for an id
// it has not seen (a key minted while the host runs).
func keyNamer(ctx context.Context, store keys.Store) func(string) string {
	names := map[string]string{}
	return func(id string) string {
		if _, ok := names[id]; !ok {
			if list, err := store.List(ctx); err == nil {
				for _, k := range list {
					names[k.ID] = k.Name
				}
			}
		}
		if n, ok := names[id]; ok {
			return n
		}
		return id
	}
}

// engineMetrics reads the engine's own /metrics through the seam (a probe-bounded GET, cut at one
// second so a stalled engine cannot stall `status`): a map of every sample by name, labels
// dropped and labelled series summed, so `vllm:x{engine="0"}` and `llamacpp:x` read alike.
func engineMetrics(ctx context.Context, up upstream.Engine) (map[string]float64, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	resp, err := up.Do(ctx, http.MethodGet, "/metrics", nil, false)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	return parseMetrics(io.LimitReader(resp.Body, 1<<20)), true
}

func parseMetrics(r io.Reader) map[string]float64 {
	m := map[string]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		name, rest := line, ""
		if i := strings.LastIndexByte(line, '}'); i >= 0 {
			name, rest = line[:strings.IndexByte(line, '{')], line[i+1:]
		} else if i := strings.IndexByte(line, ' '); i >= 0 {
			name, rest = line[:i], line[i:]
		}
		if f := strings.Fields(rest); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				m[name] += v
			}
		}
	}
	return m
}

func buildStatus(ctx context.Context, started time.Time, tun tunnelServer, up upstream.Upstream, gw gatewayServer, store keys.Store, tele *telemetry) admin.Status {
	ts := tun.Status()
	info := up.Info()
	inFlight, waiting := gw.Queue()
	st := admin.Status{
		Destinations: gw.Destinations(),
		UptimeS:      int64(time.Since(started).Seconds()),
		Mode:         "host",
		Name:         tele.name,
		Tunnel:       admin.Tunnel{Addr: ts.Addr, Region: ts.Region, Clients: ts.Clients, Sessions: tun.Peers()},
		Upstream:     admin.Upstream{Kind: string(info.Kind), URL: info.URL, Healthy: info.Health.OK, Since: info.Health.Since, ModelContext: info.ModelContext, Slots: info.Slots},
		Queue:        admin.Queue{InFlight: inFlight, Waiting: waiting},
		Engine:       admin.Engine{SlotsPeak: int(tele.peak.Load()), TokensPerS: tele.events.TokensPerSecond()},
		Process:      admin.ProcessStats(),
		Keys:         []admin.Key{},
	}
	for _, p := range st.Tunnel.Sessions {
		st.Tunnel.RxBytes += p.RxBytes
		st.Tunnel.TxBytes += p.TxBytes
	}
	if m, ok := engineMetrics(ctx, up); ok && info.Health.OK {
		st.Engine.Metrics = true
		st.Engine.Busy = int(m["llamacpp:requests_processing"] + m["vllm:num_requests_running"])
		st.Engine.Waiting = int(m["llamacpp:requests_deferred"] + m["vllm:num_requests_waiting"])
		st.Engine.MemoryBytes = int64(m["process_resident_memory_bytes"])
		st.Engine.KVCachePct = 100 * (m["vllm:kv_cache_usage_perc"] + m["vllm:gpu_cache_usage_perc"] + m["llamacpp:kv_cache_usage_ratio"])
	}
	list, err := store.List(ctx)
	if err != nil {
		return st
	}
	counters := gw.AllCounters()
	sessions := gw.Sessions()
	for _, k := range list {
		c := counters[k.ID]
		st.Keys = append(st.Keys, admin.Key{
			ID: k.ID, Name: k.Name, Status: string(k.Status),
			Connected: sessions[k.ID] > 0, Sessions: sessions[k.ID],
			InFlight: c.InFlight, RPMUsed: c.RPMUsed, TPMUsed: c.TPMUsed, TodayTokens: c.TodayTokens, LastSeen: c.LastSeen,
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

const serveHelp = `Usage: infercat serve [flags]

Runs the host: finds your inference server, opens the tunnel, and serves the gateway inside it.
Flags you pass are remembered in config.json, so the next serve needs none of them.

  infercat serve
  infercat serve --upstream http://127.0.0.1:8000 --slots 4
  infercat serve --dev-listen 127.0.0.1:9090     # also on loopback, for web development

Ctrl-C drains in-flight requests for up to 10s, then stops.

Flags:
  --upstream-transcribe URL  explicit OpenAI transcription engine base URL
  --upstream-transcribe-model ID  default model (otherwise first probed id)
  --upstream-speech-model ID default model (otherwise first probed id)
  --upstream-transcribe-key TOKEN  transcription engine bearer
  --upstream-speech URL      explicit OpenAI speech engine base URL
  --upstream-speech-key TOKEN speech engine bearer
  --max-transcription-seconds N  measured upload ceiling / unknown reservation (default 300)
  --upstream URL          inference server; detected when absent, in the order
                          llama.cpp/llama-swap :8080, Ollama :11434, LM Studio :1234, vLLM :8000.
                          --upstream auto forgets a remembered URL and detects again
  --upstream-key TOKEN    bearer token for the inference server
  --models a,b            pin models for every key; all forgets the pin
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
  --console IP:PORT      loopback console (default 127.0.0.1:9101); off disables; remembered
  --log-requests          print one line per completed request: who, what, tokens, timings,
                          how it ended — never the prompt (not remembered)
  --data-dir DIR          where this host's files live:
                            host.key.json  your host identity — every invite points at it.
                                           Back it up; don't sync it; deleting it kills every invite.
                            keys.json      one entry per friend, secrets stored only as hashes
                            usage.jsonl    one line per request; no prompt text unless --log-prompts
                            config.json    the flags above, remembered
                            tunnel.log     the tunnel engine's own log, truncated at every start
                            admin.sock     how keys, status and usage talk to a running host
                                           (GET /status, GET /events, POST /reload)

There is no daemon mode: run it under your supervisor of choice (launchd, systemd, tmux).
`

func audioStatus(transcribe, speech upstream.AudioEngine) map[string]admin.Upstream {
	out := map[string]admin.Upstream{}
	for route, a := range map[string]upstream.AudioEngine{"transcriptions": transcribe, "speech": speech} {
		if a != nil {
			i := a.Info()
			out[route] = admin.Upstream{URL: i.URL, Healthy: i.Health.OK, Since: i.Health.Since, Slots: 1}
		}
	}
	return out
}

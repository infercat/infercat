package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/agentconfig"
	"github.com/infercat/infercat/internal/product"
	"github.com/infercat/infercat/internal/tunnel"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

// watchLines is how many request lines `status --watch` keeps under the block.
const watchLines = 20

func (e *env) cmdStatus(ctx context.Context, pre string, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	watch := fs.Bool("watch", false, "redraw every interval and stream one line per completed request; Ctrl-C stops")
	interval := fs.Duration("interval", time.Second, "how often --watch redraws")
	if err := e.parse(fs, statusHelp, args); err != nil {
		return err
	}
	dataDir, err := resolveDataDir(*dd)
	if err != nil {
		return err
	}
	var agentsOut io.Writer = io.Discard
	if !*watch {
		agentsOut = e.out
	}
	agentCount, err := writeAgents(agentsOut)
	if err != nil {
		return err
	}
	st, err := admin.Fetch(ctx, dataDir)
	if errors.Is(err, admin.ErrNoDaemon) {
		if agentCount > 0 && !*watch {
			fmt.Fprintln(e.out, "no running host or bridge for this data directory")
			return nil
		}
		return fmt.Errorf("no host is running for %s — start one with `%s serve` (a bridge: `%s connect --data-dir %s …`)", dataDir, product.CLIName, product.CLIName, dataDir)
	}
	if err != nil {
		return err
	}
	if !*watch {
		writeStatus(e.out, st)
		return nil
	}
	return e.watchStatus(ctx, dataDir, st, max(*interval, 100*time.Millisecond))
}

func writeAgents(w io.Writer) (int, error) {
	cfg, err := agentconfig.Default()
	if err != nil {
		fmt.Fprintln(w, "agents: unknown (no config dir)")
		return 0, nil
	}
	agents, err := cfg.List()
	if err != nil {
		return 0, err
	}
	if len(agents) > 0 {
		fmt.Fprintf(w, "agents    %s\n", strings.Join(agents, ", "))
	}
	return len(agents), nil
}

// watchStatus is `status --watch`: the block redrawn in place each interval — a plain ANSI clear,
// no library — with the last watchLines request lines from the event stream beneath it. Ctrl-C
// ends it cleanly. A host that stops answering mid-watch is shown as such and the watch keeps
// trying, since a restart is exactly when someone is watching.
func (e *env) watchStatus(ctx context.Context, dataDir string, st admin.Status, every time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan usage.Event, 64)
	go func() {
		for ctx.Err() == nil {
			_ = admin.Watch(ctx, dataDir, func(ev usage.Event) { events <- ev })
			select {
			case <-time.After(every):
			case <-ctx.Done():
			}
		}
	}()
	names := map[string]string{}
	var block string
	var lines []string
	refresh := func(st admin.Status, err error) {
		var b strings.Builder
		_, _ = writeAgents(&b)
		if err != nil {
			fmt.Fprintf(&b, "%s %s — no answer from the host for %s (%v); still watching\n", product.Name, product.Version, dataDir, err)
		} else {
			for _, k := range st.Keys {
				names[k.ID] = k.Name
			}
			writeStatus(&b, st)
		}
		block = b.String()
	}
	draw := func() {
		fmt.Fprintf(e.out, "\x1b[H\x1b[2J%s\n%s", block, strings.Join(lines, "\n"))
	}
	refresh(st, nil)
	draw()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(e.out)
			return nil
		case <-t.C:
			refresh(admin.Fetch(ctx, dataDir))
			draw()
		case ev := <-events:
			who := names[ev.KeyID]
			if who == "" {
				who = orDash(ev.KeyID)
			}
			lines = append(lines, requestLine(ev, who))
			if len(lines) > watchLines {
				lines = lines[len(lines)-watchLines:]
			}
			draw()
		}
	}
}

// requestLine is the one line per completed request — `status --watch`, `serve --log-requests`,
// `connect --log-requests` print the same one: when it ended, who, what, the tokens, the timings,
// how it ended. Never prompt content (Protection 3).
func requestLine(e usage.Event, who string) string {
	at := e.TS.Add(time.Duration(e.TotalMS) * time.Millisecond).Local().Format("15:04:05")
	parts := []string{at, who, endpointWord(e.Endpoint)}
	if e.Via == "bridge" {
		parts = append(parts, "via bridge")
	}
	if e.Model != "" {
		parts = append(parts, e.Model)
	}
	if e.PromptTokens > 0 || e.CompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d→%d tok", e.PromptTokens, e.CompletionTokens))
	}
	if e.QueuedMS > 0 {
		parts = append(parts, "queued "+msWord(e.QueuedMS))
	}
	if e.TTFTMS > 0 {
		parts = append(parts, "ttft "+msWord(e.TTFTMS))
	}
	parts = append(parts, msWord(e.TotalMS))
	switch {
	case e.Code == "":
		parts = append(parts, "ok")
	case e.Status >= 400 && e.Status != 499:
		parts = append(parts, fmt.Sprintf("%d %s", e.Status, e.Code))
	default: // client_closed, or an error event inside a stream that had already started
		parts = append(parts, e.Code)
	}
	return strings.Join(parts, "  ")
}

func endpointWord(path string) string {
	if word, _ := usage.ModelEndpoint(path); word != "" {
		return word
	}
	switch path {
	case "/v1/models":
		return "models"
	case "/me":
		return "me"
	}
	return path
}

func msWord(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%dms", n)
	}
	return fmt.Sprintf("%.1fs", float64(n)/1000)
}

func bytesWord(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}

func writeStatus(w io.Writer, st admin.Status) {
	if st.Console != "" {
		fmt.Fprintf(w, "console   http://%s/ (infercat console opens it)\n", st.Console)
	}
	if st.Mode == "bridge" {
		writeBridge(w, st)
		return
	}
	fmt.Fprintf(w, "%s %s — up %s\n", st.Product, st.Version, shortDur(time.Duration(st.UptimeS)*time.Second))
	fmt.Fprintf(w, "upstream  %s  %s  %s  context %s  slots %d\n",
		kindWord(st.Upstream.Kind), st.Upstream.URL, healthWord(st.Upstream.Healthy, st.Upstream.Since),
		contextStr(st.Upstream.ModelContext), st.Upstream.Slots)
	for _, route := range []string{"transcriptions", "speech"} {
		if a, ok := st.Audio[route]; ok {
			fmt.Fprintf(w, "audio     /v1/audio/%s  %s  %s\n", route, a.URL, healthWord(a.Healthy, a.Since))
		}
	}
	if len(st.ModelsPinned) > 0 {
		fmt.Fprintf(w, "models    %s (pinned)\n", modelList(st.ModelsPinned))
	}
	fmt.Fprintf(w, "tunnel    %s  relay %s  %s\n", orDash(tunnel.Display(st.Tunnel.Addr)), orDash(st.Tunnel.Region), plural(st.Tunnel.Clients, "client"))
	if b := st.Bridge; b != nil {
		state := "off"
		if b.Enabled {
			state = "on, connecting"
			if b.Connected {
				state = "on, connected"
			}
		}
		fmt.Fprintf(w, "bridge    %s  %s  since %s  %d requests today (UTC)", b.URL, state, b.Since.Local().Format(time.RFC3339), b.RequestsToday)
		if b.Connected && b.Slots > 0 {
			fmt.Fprintf(w, " · %s", plural(b.Slots, "slot"))
		}
		if b.LastError != "" {
			fmt.Fprintf(w, "  %s", b.LastError)
		}
		fmt.Fprintln(w)
	}
	for _, m := range st.Members {
		fmt.Fprintf(w, "member    %s: %s (restarts %d) %s\n", m.ID, m.State, m.Restarts, m.Error)
	}
	writeSessions(w, st.Tunnel)
	fmt.Fprintf(w, "queue     %d in flight, %d waiting  (peak %d in flight, sampled)\n", st.Queue.InFlight, st.Queue.Waiting, st.Engine.SlotsPeak)
	fmt.Fprintf(w, "engine    %s\n", engineWords(st.Engine))
	fmt.Fprintf(w, "process   %s\n", processWords(st.Process))
	for _, d := range st.Destinations {
		if d.ImageAbandons == 1 {
			fmt.Fprintln(w, "images    1 failed generation; the next counted failure makes the engine suspect")
		}
		if d.ImageAbandons >= 2 {
			retry := "retry wait elapsed"
			if at := time.Unix(0, d.ImageRetryAt); time.Until(at) > 0 {
				retry = "next retry " + at.Format(time.RFC3339)
			}
			fmt.Fprintf(w, "images    suspect: %d failed generations; %s (further failures do not count)\n", d.ImageAbandons, retry)
		}
	}
	if st.ImageCleanupPending > 0 || st.ImageOrphansForReview > 0 {
		fmt.Fprintf(w, "images    %d awaiting cleanup, %d for review\n", st.ImageCleanupPending, st.ImageOrphansForReview)
	}
	if len(st.Keys) == 0 {
		fmt.Fprintf(w, "\nno keys yet — `%s keys add <name>` mints one\n", product.CLIName)
		return
	}
	fmt.Fprintln(w)
	tw := newTable(w)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tIN FLIGHT\tRPM\tTODAY\tLAST SEEN")
	for _, k := range st.Keys {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n", k.ID, k.Name, k.Status, k.InFlight, k.RPMUsed, k.TodayTokens, ago(k.LastSeen))
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%s\n", countsLine)
}

// writeSessions is the tunnel's clients as the host has met them (029 promise 3): the count, the
// bytes both ways, one line per active session — and, once, the truth that a host on tailcat
// 0.4.0 cannot see a session's path or handshake (the client measures them; connect prints them).
func writeSessions(w io.Writer, t admin.Tunnel) {
	active := 0
	for _, s := range t.Sessions {
		if s.Active {
			active++
		}
	}
	fmt.Fprintf(w, "sessions  %d active of %d seen  ·  in %s  out %s  ·  paths: the client's to measure, not visible to a host\n", active, len(t.Sessions), bytesWord(t.RxBytes), bytesWord(t.TxBytes))
	tw := newTable(w)
	for _, s := range t.Sessions {
		if s.Active {
			fmt.Fprintf(tw, "  %s\t%s\tin %s\tout %s\tlast byte %s\tage %s\n", shortKey(s.Key), plural(s.Conns, "conn"), bytesWord(s.RxBytes), bytesWord(s.TxBytes), ago(s.LastByte), shortDur(time.Since(s.Since)))
		}
	}
	tw.Flush()
}

// shortKey is a client's tunnel address as the block shows it: its last two groups.
func shortKey(k string) string {
	if g := strings.Split(k, ":"); len(g) > 2 {
		return "…" + strings.Join(g[len(g)-2:], ":")
	}
	return k
}

func pathWord(s admin.Session) string {
	switch s.Path {
	case "direct":
		return "direct " + s.Via
	case "relayed":
		return "relayed via " + orDash(s.Via)
	}
	return "path unknown"
}

func engineWords(e admin.Engine) string {
	s := fmt.Sprintf("%.0f tok/s over the last minute", e.TokensPerS)
	if !e.Metrics {
		return s + "  ·  no /metrics from this engine"
	}
	s += fmt.Sprintf("  ·  engine says %d busy, %d waiting  ·  memory ", e.Busy, e.Waiting)
	if e.MemoryBytes > 0 {
		s += bytesWord(e.MemoryBytes)
	} else {
		s += "not reported"
	}
	if e.KVCachePct > 0 {
		s += fmt.Sprintf("  ·  kv cache %.0f%%", e.KVCachePct)
	}
	return s
}

func processWords(p admin.Process) string {
	rss := "—"
	if p.RSSBytes > 0 {
		rss = bytesWord(p.RSSBytes)
	}
	return fmt.Sprintf("%d goroutines  heap %s  sys %s  rss %s", p.Goroutines, bytesWord(int64(p.HeapBytes)), bytesWord(int64(p.SysBytes)), rss)
}

// writeBridge is `status` against a `connect --data-dir` (029 promise 5): the host it reaches,
// the path right now, the local endpoint and what is in flight through it.
func writeBridge(w io.Writer, st admin.Status) {
	fmt.Fprintf(w, "%s %s — up %s  ·  bridge\n", st.Product, st.Version, shortDur(time.Duration(st.UptimeS)*time.Second))
	fmt.Fprintf(w, "host      %s  relay %s  %s\n", orDash(st.Name), orDash(st.Tunnel.Region), orDash(tunnel.Display(st.Tunnel.Addr)))
	path := "lost — reconnecting"
	if len(st.Tunnel.Sessions) > 0 && st.Tunnel.Sessions[0].Active {
		s := st.Tunnel.Sessions[0]
		path = fmt.Sprintf("%s · %.1f ms  (handshake took %d ms; session %s old)", pathWord(s), s.RTTMS, s.HandshakeMS, shortDur(time.Since(s.Since)))
	}
	fmt.Fprintf(w, "path      %s\n", path)
	fmt.Fprintf(w, "local     %s  %d in flight\n", st.Upstream.URL, st.Queue.InFlight)
}

// healthWord and kindWord are the engine state as the host reads it, on the banner and in
// `status` (DESIGN §3.2): an engine nobody has met is not called by a guessed name, and one that
// stopped answering says since when.
func healthWord(ok bool, since time.Time) string {
	if ok {
		return "healthy"
	}
	if since.IsZero() {
		return "NOT ANSWERING"
	}
	return "NOT ANSWERING for " + shortDur(time.Since(since))
}

func kindWord(kind string) string {
	if kind == string(upstream.Unknown) {
		return "(not identified yet)"
	}
	return kind
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// shortDur renders an uptime the way a person reads it.
func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

const statusHelp = `Usage: infercat status [--data-dir DIR] [--watch [--interval 1s]]

What the running host is doing right now: the upstream, the tunnel address and relay, every
session (path, handshake, bytes, age), the queue and the engine's own counters, the process,
and every key with its live counters. Reads the admin socket in the data dir; exits non-zero
when no host is running. Against a bridge (connect --data-dir DIR) it shows that bridge.

  infercat status
  infercat status --watch            # redrawn every second, one line per request beneath:
                                          # 12:01:05  alice  chat  gemma  38→412 tok  ttft 61ms  4.1s  ok

Flags:
  --watch          redraw in place every --interval and stream requests; Ctrl-C stops
  --interval D     how often --watch redraws (default 1s)
`

func newTable(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }

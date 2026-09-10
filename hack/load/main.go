// Command load is ticket 028's instrument: N simulated friends, each with its own key and its own
// tunnel session, running the growing-conversation message mix against a running host for M
// minutes while the host, the engine and the relay are sampled once a second. One directory per
// run: requests.jsonl (what each friend saw), samples.jsonl (one line a second), host.jsonl (the
// host's own usage events for the run's keys), tailcat.log, and summary.md — the tables that
// docs/MEASURE.md quotes.
//
//	go run ./hack/load --host-dir DIR --bin bin/infercat --host-pid PID \
//	  --engine http://127.0.0.1:18080 --engine-pid PID --relay-ssh root@<relay-ip> \
//	  --n 12 --minutes 3 --relay-only --out /tmp/bn028-out --name llama-n12
//
// It sends requests only and never starts, stops or reconfigures the host, the engine or the
// relay by itself; --at "60s=<cmd>" runs a command the operator chose (the restart cases).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/invite"
	"github.com/infercat/infercat/internal/usage"
	"tailscale.com/envknob"
)

type run struct {
	name, model, hostDir string
	trickle              time.Duration
	bodyBytes            int
	noReadOne            bool
	mu                   sync.Mutex
	recs                 []reqRecord
	samples              []sample
	events               []string
	reqW, sampW          *os.File
	counters             loadCounters
}

func (r *run) record(rec reqRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
	r.counters.Done++
	if rec.Err != "" || rec.Status >= 400 {
		r.counters.Errors++
	}
	r.counters.BytesUp += int64(rec.BytesUp)
	r.counters.BytesDown += int64(rec.BytesDown)
	json.NewEncoder(r.reqW).Encode(rec)
}

// noRead: with --noread, friend 0's first turn is the friend who never reads its stream.
func (r *run) noRead(s *session) bool {
	return r.noReadOne && s.friend == 0 && s.idx == 0 && s.turn == 1
}

type hook struct {
	at  time.Duration
	cmd string
}

type hooks []hook

func (h *hooks) String() string { return fmt.Sprint(*h) }
func (h *hooks) Set(v string) error {
	d, cmd, ok := strings.Cut(v, "=")
	dur, err := time.ParseDuration(d)
	if !ok || err != nil {
		return fmt.Errorf("--at wants DURATION=COMMAND, got %q", v)
	}
	*h = append(*h, hook{dur, cmd})
	return nil
}

func main() {
	var (
		hostDir    = flag.String("host-dir", "", "the host's data dir (admin.sock, keys.json, usage.jsonl)")
		bin        = flag.String("bin", "bin/infercat", "host binary, for `keys add --json`")
		hostPid    = flag.Int("host-pid", 0, "host process id, for RSS")
		engine     = flag.String("engine", "", "engine base URL, for /metrics (and /slots on llama.cpp)")
		enginePid  = flag.Int("engine-pid", 0, "engine process id when local, for RSS")
		relaySSH   = flag.String("relay-ssh", "", "ssh target of the relay box (varz + top once a second); empty = no relay samples")
		n          = flag.Int("n", 2, "friends")
		perFriend  = flag.Int("sessions-per-friend", 1, "tunnel sessions each friend opens on its one key (abuse shape)")
		minutes    = flag.Float64("minutes", 2, "run length")
		mode       = flag.String("mode", "chat", "chat (the message mix) or sessions (idle-connected + a trickle)")
		relayOnly  = flag.Bool("relay-only", false, "browser-like: UDP off, every byte through the relay (TS_DEBUG_ALWAYS_USE_DERP)")
		stagger    = flag.Duration("stagger", 100*time.Millisecond, "delay between session connects; 0 = all in the same instant")
		keepalive  = flag.Bool("keepalive", false, "reuse tunnel connections across requests (default: dial per request, like the browser)")
		seedSpread = flag.Int("seed-spread", 8, "friend i starts i%%spread turns into a chat; 0 = every chat starts fresh")
		maxConc    = flag.Int("max-concurrent", 1, "the keys' max_concurrent")
		settle     = flag.Duration("settle", 10*time.Second, "wait after the last session closes before the after-sample (wireguard retries a gone peer for ~90 s)")
		bodyBytes  = flag.Int("body-bytes", 0, "first turn carries a message of about this many bytes (the 4 MiB case)")
		noRead     = flag.Bool("noread", false, "friend 0 never reads its first stream")
		trickle    = flag.Duration("trickle", 15*time.Second, "sessions mode: interval between a session's tiny requests")
		model      = flag.String("model", "", "model id; default: the host's first")
		out        = flag.String("out", "hack/load/out", "output root")
		name       = flag.String("name", "", "run name (default: mode-nN-time)")
		at         hooks
		preview    = flag.Bool("bridge-preview", false, "bounded authenticated benchmark of this host's registered preview bridge")
		earlyClose = flag.Bool("early-close", false, "preview only: close one stream after its first token, then complete a request")
	)
	flag.Var(&at, "at", "DURATION=COMMAND to run mid-run via sh -c (repeatable)")
	flag.Parse()
	if *preview {
		if *mode != "chat" || *bodyBytes != 0 || *noRead || len(at) != 0 || *perFriend != 1 || *relaySSH != "" || *relayOnly {
			log.Fatal("bridge-preview supports authenticated normal chat only")
		}
		if err := previewRun(previewOptions{hostDir: *hostDir, bin: *bin, pid: *hostPid, n: *n, duration: time.Duration(*minutes * float64(time.Minute)), model: *model, out: *out, name: *name, earlyClose: *earlyClose}); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *earlyClose {
		log.Fatal("early-close requires bridge-preview")
	}
	if *hostDir == "" {
		log.Fatal("--host-dir is required")
	}
	if *name == "" {
		*name = fmt.Sprintf("%s-n%d-%s", *mode, *n, time.Now().Format("150405"))
	}
	if *relayOnly {
		envknob.Setenv("TS_DEBUG_ALWAYS_USE_DERP", "true")
	}
	dir := filepath.Join(*out, *name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	r := &run{name: *name, hostDir: *hostDir, trickle: *trickle, bodyBytes: *bodyBytes, noReadOne: *noRead, model: *model}
	r.reqW = mustCreate(filepath.Join(dir, "requests.jsonl"))
	r.sampW = mustCreate(filepath.Join(dir, "samples.jsonl"))
	tlog := log.New(mustCreate(filepath.Join(dir, "tailcat.log")), "", log.Ltime|log.Lmicroseconds)
	ctx := context.Background()
	log.SetFlags(log.Ltime)
	log.Printf("run %s: N=%d×%d mode=%s relay-only=%v minutes=%.1f → %s", *name, *n, *perFriend, *mode, *relayOnly, *minutes, dir)

	before := sampleHost(ctx, *hostDir, *hostPid)
	log.Printf("host before: %s", before)

	// Keys: one per friend, minted the way a host does it.
	var sessions []*session
	for i := 0; i < *n; i++ {
		id, secret, addr, err := mintKey(*bin, *hostDir, fmt.Sprintf("load-%s-%02d", *name, i), *maxConc)
		if err != nil {
			log.Fatalf("mint key %d: %v", i, err)
		}
		for j := 0; j < *perFriend; j++ {
			sessions = append(sessions, newSession(i, j, id, secret, addr, tlog.Printf, *keepalive))
		}
	}
	log.Printf("%d keys minted, %d sessions", *n, len(sessions))

	var relay *relaySampler
	if *relaySSH != "" {
		var err error
		if relay, err = startRelaySampler(*relaySSH); err != nil {
			log.Fatalf("relay sampler: %v", err)
		}
		for i := 0; i < 100; i++ { // the baseline must be in hand before the first friend connects
			if m := relay.read(); m != nil {
				r.samples = append(r.samples, sample{TS: time.Now(), Relay: m})
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	stopSampling := make(chan struct{})
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-t.C:
			}
			r.mu.Lock()
			c := r.counters
			r.mu.Unlock()
			s := sample{TS: time.Now(), Load: c, Host: sampleHost(ctx, *hostDir, *hostPid), Engine: sampleEngine(ctx, *engine, *enginePid), Relay: relay.read()}
			r.mu.Lock()
			r.samples = append(r.samples, s)
			json.NewEncoder(r.sampW).Encode(s)
			r.mu.Unlock()
		}
	}()

	// Connect: staggered, or all at once (the launch-day spike).
	cctx, ccancel := context.WithTimeout(ctx, 90*time.Second)
	var wg sync.WaitGroup
	for i, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * *stagger)
			if err := s.connect(cctx); err != nil {
				log.Printf("friend %d/%d: %v", s.friend, s.idx, err)
			}
		}()
	}
	wg.Wait()
	ccancel()
	if r.model == "" {
		if _, b := sessions[0].get(ctx, "/v1/models"); b != nil {
			var v struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(b, &v) == nil && len(v.Data) > 0 {
				r.model = v.Data[0].ID
			}
		}
	}
	if *mode == "chat" && *seedSpread > 0 {
		for _, s := range sessions {
			s.history = seedHistory(s.friend, s.friend%*seedSpread)
		}
	}
	log.Printf("connected; model=%q; running %.1f min", r.model, *minutes)

	start := time.Now()
	loop, cancelLoop := context.WithTimeout(ctx, time.Duration(*minutes*float64(time.Minute)))
	grace, cancelGrace := context.WithTimeout(ctx, time.Duration(*minutes*float64(time.Minute))+45*time.Second)
	defer cancelLoop()
	defer cancelGrace()
	for _, h := range at {
		go func() {
			sleepCtx(grace, h.at)
			t0 := time.Now()
			out, err := exec.Command("sh", "-c", h.cmd).CombinedOutput()
			ev := fmt.Sprintf("t+%s: `%s` → %s (%v, %s)", h.at, h.cmd, strings.TrimSpace(firstLine(string(out))), err, time.Since(t0).Round(time.Millisecond))
			log.Print(ev)
			r.mu.Lock()
			r.events = append(r.events, ev)
			r.mu.Unlock()
		}()
	}
	for _, s := range sessions {
		if s.connectErr != "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if *mode == "sessions" {
				s.runSessions(loop, grace, r)
			} else {
				s.runChat(loop, grace, r)
			}
		}()
	}
	<-loop.Done()
	log.Printf("loop over; waiting for requests in flight (≤45 s)")
	wg.Wait()
	dur := time.Since(start)
	// Close the tunnel clients, but never let the instrument's own shutdown block the run. Under
	// relay-only (UDP off) tailcat.Client.Close can park for minutes in wireguard's BindClose
	// (defect BN-i1, ticket 035); the host's leak check reads the HOST's counters, which a
	// stuck client does not touch, so the after-sample and summary go ahead on a bounded wait.
	closed := make(chan struct{})
	go func() {
		var cwg sync.WaitGroup
		for _, s := range sessions {
			cwg.Add(1)
			go func() { defer cwg.Done(); s.client.Close() }()
		}
		cwg.Wait()
		close(closed)
	}()
	select {
	case <-closed:
		log.Printf("sessions closed")
	case <-time.After(15 * time.Second):
		log.Printf("client close did not return in 15 s (tailcat relay-only Close hang, ticket 035); proceeding")
	}
	log.Printf("settling %s before the after-sample", *settle)
	time.Sleep(*settle)
	after := sampleHost(ctx, *hostDir, *hostPid)
	close(stopSampling)
	<-samplingDone
	relay.stop()
	log.Printf("host after: %s", after)

	keyIDs := map[string]bool{}
	for _, s := range sessions {
		keyIDs[s.keyID] = true
	}
	events := hostEvents(*hostDir, start.Add(-2*time.Second), keyIDs)
	hw := mustCreate(filepath.Join(dir, "host.jsonl"))
	for _, e := range events {
		json.NewEncoder(hw).Encode(e)
	}
	hw.Close()
	md := summary(r, sessions, events, before, after, dur, *n, *engine, *settle)
	os.WriteFile(filepath.Join(dir, "summary.md"), []byte(md), 0o644)
	fmt.Print(md)
	os.Exit(0) // a client Close still parked in tailcat's relay-only path (ticket 035) must not keep us alive
}

func mustCreate(p string) *os.File {
	f, err := os.Create(p)
	if err != nil {
		log.Fatal(err)
	}
	return f
}

func firstLine(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

// mintKey is `keys add --json` with limits high enough that the run measures the layers, not the
// key's own meters: the per-key concurrency is the one limit under test.
func mintKey(bin, hostDir, name string, maxConc int) (id, secret, addr string, err error) {
	out, err := exec.Command(bin, "keys", "add", "--data-dir", hostDir, "--json", "--force", "--no-qr",
		"--rpm", "1000", "--tpm", "100000000", "--daily-tokens", "1000000000", "--max-output-tokens", "4096",
		"--max-concurrent", strconv.Itoa(maxConc), name).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%w: %s", err, ee.Stderr)
		}
		return "", "", "", err
	}
	var v struct {
		KeyID  string `json:"key_id"`
		Invite string `json:"invite"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", "", "", fmt.Errorf("keys add --json: %v: %s", err, out)
	}
	inv, err := invite.Decode(v.Invite)
	if err != nil {
		return "", "", "", err
	}
	return v.KeyID, inv.Secret, inv.Addr, nil
}

// hostEvents reads the host's usage.jsonl for this run's keys since the run started: the host's
// side of every request (queued_ms, ttft_ms, prompt_tokens, the settle's status/code).
func hostEvents(hostDir string, since time.Time, keyIDs map[string]bool) []usage.Event {
	b, err := os.ReadFile(filepath.Join(hostDir, "usage.jsonl"))
	if err != nil {
		return nil
	}
	var out []usage.Event
	for _, line := range strings.Split(string(b), "\n") {
		var e usage.Event
		if json.Unmarshal([]byte(line), &e) == nil && keyIDs[e.KeyID] && !e.TS.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// ---- summary ----

func pct(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[min(len(s)-1, int(float64(len(s))*p))]
}

func summary(r *run, sessions []*session, events []usage.Event, before, after *hostSample, dur time.Duration, n int, engine string, settle time.Duration) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p("## %s — N=%d (%d sessions), %s, model %s, %.0f s", r.name, n, len(sessions), r.hostDir, r.model, dur.Seconds())

	var conn []float64
	paths, failed := map[string]int{}, 0
	for _, s := range sessions {
		if s.connectErr != "" {
			failed++
			continue
		}
		conn = append(conn, float64(s.connectMS))
		k, _, _ := strings.Cut(s.path, " ")
		paths[k]++
	}
	p("- connect (handshake): %d ok, %d failed; p50 %.0f ms · p95 %.0f ms · max %.0f ms; paths %v", len(conn), failed, pct(conn, .5), pct(conn, .95), pct(conn, 1), paths)

	// Client view.
	var chat, other int
	codes := map[string]int{}
	var cttft, ctoks []float64
	var up, down int64
	aborted, overDeadline := 0, 0
	for _, x := range r.recs {
		up, down = up+int64(x.BytesUp), down+int64(x.BytesDown)
		if x.Endpoint != "/v1/chat/completions" {
			other++
			continue
		}
		chat++
		key := fmt.Sprintf("%d %s", x.Status, x.Code)
		if x.Status == 200 && x.Code == "" && x.Err == "" {
			key = "200 served"
			cttft = append(cttft, float64(x.TTFTMS))
			if x.TokS > 0 {
				ctoks = append(ctoks, x.TokS)
			}
		} else if x.Err != "" && x.Status == 0 {
			key = "transport: " + firstLine(x.Err)
		}
		codes[key]++
		if x.Aborted {
			aborted++
		}
		if (x.TTFTMS == 0 && x.TotalMS > 155_000) || x.TotalMS > 400_000 {
			overDeadline++
		}
	}
	p("- client: %d chats, %d other requests; outcomes %v; aborted by the run's end %d; **past every deadline: %d**", chat, other, codes, aborted, overDeadline)
	p("- client: chat TTFT p50 %.0f ms · p95 %.0f ms; tok/s per stream p50 %.1f; uplink %.1f KB/s total = **%.2f KB/s per friend**; downlink %.1f KB/s total",
		pct(cttft, .5), pct(cttft, .95), pct(ctoks, .5), float64(up)/1024/dur.Seconds(), float64(up)/1024/dur.Seconds()/float64(n), float64(down)/1024/dur.Seconds())

	// Host view, by prompt size.
	type bucket struct {
		lo, hi                                     int
		queued, prefill, ttft, toks, total, prompt []float64
	}
	buckets := []*bucket{{0, 1000, nil, nil, nil, nil, nil, nil}, {1000, 4000, nil, nil, nil, nil, nil, nil}, {4000, 8000, nil, nil, nil, nil, nil, nil}, {8000, 16000, nil, nil, nil, nil, nil, nil}, {16000, 1 << 30, nil, nil, nil, nil, nil, nil}}
	hcodes := map[string]int{}
	served, promptSent, compTotal := 0, 0, 0
	for _, e := range events {
		if e.Endpoint != "/v1/chat/completions" {
			continue
		}
		key := fmt.Sprintf("%d %s", e.Status, e.Code)
		if e.Status == 200 && e.Code == "" {
			key = "200 served"
		}
		hcodes[key]++
		if e.Status != 200 || e.Code != "" {
			continue
		}
		served++
		promptSent += e.PromptTokens
		compTotal += e.CompletionTokens
		for _, bk := range buckets {
			if e.PromptTokens >= bk.lo && e.PromptTokens < bk.hi {
				bk.prompt = append(bk.prompt, float64(e.PromptTokens))
				bk.queued = append(bk.queued, float64(e.QueuedMS))
				bk.prefill = append(bk.prefill, float64(e.TTFTMS-e.QueuedMS))
				bk.ttft = append(bk.ttft, float64(e.TTFTMS))
				bk.total = append(bk.total, float64(e.TotalMS)/1000)
				if gen := float64(e.TotalMS-e.TTFTMS) / 1000; gen > 0 && e.CompletionTokens > 0 {
					bk.toks = append(bk.toks, float64(e.CompletionTokens)/gen)
				}
			}
		}
	}
	p("- host events: %v; served %.1f/min; completion %.1f tok/s aggregate; prompt tokens sent %d", hcodes, float64(served)/dur.Minutes(), float64(compTotal)/dur.Seconds(), promptSent)
	p("")
	p("| prompt tokens | n | prompt p50 | queued p50 / p95 ms | prefill (TTFT−queued) p50 / p95 ms | TTFT p50 / p95 ms | tok/s p50 | total p50 s |")
	p("|---|---|---|---|---|---|---|---|")
	for _, bk := range buckets {
		if len(bk.prompt) == 0 {
			continue
		}
		hi := strconv.Itoa(bk.hi)
		if bk.hi == 1<<30 {
			hi = "∞"
		}
		p("| %d–%s | %d | %.0f | %.0f / %.0f | %.0f / %.0f | %.0f / %.0f | %.1f | %.1f |", bk.lo, hi, len(bk.prompt), pct(bk.prompt, .5),
			pct(bk.queued, .5), pct(bk.queued, .95), pct(bk.prefill, .5), pct(bk.prefill, .95), pct(bk.ttft, .5), pct(bk.ttft, .95), pct(bk.toks, .5), pct(bk.total, .5))
	}
	p("")

	// Host process and engine, from the samples.
	var gMax, cMax, ifMax, wMax int
	var rssMax int64
	eng := map[string][]float64{}
	var rel []map[string]float64
	for _, s := range r.samples {
		if s.Host != nil {
			gMax, cMax, ifMax, wMax, rssMax = max(gMax, s.Host.Goroutines), max(cMax, s.Host.Clients), max(ifMax, s.Host.InFlight), max(wMax, s.Host.Waiting), max(rssMax, s.Host.RSSKB)
		}
		for k, v := range s.Engine {
			// vLLM's metric names carry labels (num_requests_running:engine=0,model_name=…); fold to
			// the base name so a single-engine, single-model run's series is found by its plain name.
			if i := strings.IndexByte(k, ':'); i >= 0 {
				k = k[:i]
			}
			eng[k] = append(eng[k], v)
		}
		if s.Relay != nil {
			rel = append(rel, s.Relay)
		}
	}
	p("- host process: goroutines before %d → after %d (max %d, %s after the last session closed); RSS before %d MB → after %d MB (max %d MB); tunnel clients max %d; in_flight max %d; waiting max %d",
		before.Goroutines, after.Goroutines, gMax, settle, before.RSSKB/1024, after.RSSKB/1024, rssMax/1024, cMax, ifMax, wMax)
	delta := func(k string) float64 {
		v := eng[k]
		if len(v) < 2 {
			return 0
		}
		return v[len(v)-1] - v[0]
	}
	mx := func(k string) float64 { return pct(eng[k], 1) }
	switch {
	case len(eng["prompt_tokens_total"]) > 0 && len(eng["n_decode_total"]) > 0: // llama.cpp
		pp, ps, gt, gs := delta("prompt_tokens_total"), delta("prompt_seconds_total"), delta("tokens_predicted_total"), delta("tokens_predicted_seconds_total")
		cache := 0.0
		if promptSent > 0 {
			cache = 100 * (1 - pp/float64(promptSent))
		}
		p("- engine (llama.cpp): prefilled %.0f prompt tokens in %.1f s = **%.0f tok/s prefill**; generated %.0f tokens in %.1f s = %.0f tok/s; processing max %.0f, deferred max %.0f; RSS max %.0f MB; **slot cache recovered %.0f%% of prompt tokens sent** (%d sent, %.0f prefilled)",
			pp, ps, pp/max(ps, 0.001), gt, gs, gt/max(gs, 0.001), mx("requests_processing"), mx("requests_deferred"), mx("rss_kb")/1024, cache, promptSent, pp)
	case len(eng["num_requests_running"]) > 0: // vLLM
		hq, hh := delta("prefix_cache_queries_total"), delta("prefix_cache_hits_total")
		p("- engine (vLLM): prompt tokens %.0f, generation tokens %.0f; running max %.0f, waiting max %.0f; KV cache max %.0f%%; **prefix cache hit %.0f%%** (%.0f of %.0f queried blocks)",
			delta("prompt_tokens_total"), delta("generation_tokens_total"), mx("num_requests_running"), mx("num_requests_waiting"), 100*mx("kv_cache_usage_perc"), 100*hh/max(hq, 1), hh, hq)
	default:
		p("- engine: no samples (%s)", engine)
	}
	if len(rel) >= 2 {
		f, l := rel[0], rel[len(rel)-1]
		secs := l["t"] - f["t"]
		var cpuSum, cpuMax, rssM, gor float64
		for _, m := range rel {
			cpuSum, cpuMax, rssM, gor = cpuSum+m["cpu_pct"], max(cpuMax, m["cpu_pct"]), max(rssM, m["process_resident_memory_bytes"]), max(gor, m["go_goroutines"])
		}
		recv := l["derp_bytes_received"] - f["derp_bytes_received"]
		dropped, why := relayDropped(f, l)
		p("- relay: accepts +%.0f; recv %.1f KB/s (**%.2f KB/s per friend**), sent %.1f KB/s; derper CPU %.1f%% of one core; box CPU avg %.1f%% max %.1f%%; derper RSS max %.0f MB, goroutines max %.0f; **packets dropped +%.0f** %s",
			l["derp_accepts"]-f["derp_accepts"], recv/1024/secs, recv/1024/secs/float64(n), (l["derp_bytes_sent"]-f["derp_bytes_sent"])/1024/secs,
			100*(l["process_cpu_seconds_total"]-f["process_cpu_seconds_total"])/secs, cpuSum/float64(len(rel)), cpuMax, rssM/1024/1024, gor, dropped, why)
	}
	for _, e := range r.events {
		p("- event %s", e)
	}
	return b.String()
}

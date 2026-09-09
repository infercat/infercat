# LIMITS — what one relay, one host, the gateway, and each engine hold

Plain-words answer to "how much can this take?", from ticket 028's load test (2026-09-03; every number
has a command and a table in `docs/MEASURE.md` § Load). One laptop (M5 Max) ran every simulated friend,
each a real tunnel client with its own key, against real hosts, the real relay droplet, and both real
engines. Read this before the public launch.

## The one thing to know first

**Nothing below is a launch-day blocker.** No layer's ceiling is under what a social-media launch of a
single demo host needs. The binding limit is the **engine**, by design: a home engine serves two
requests at once, and the gateway turns a crowd into an orderly queue rather than a crash. The relay and
the host tunnel are nowhere near their limits at the numbers a launch produces. The two defects the test
found (tickets 035, 036) are correctness/polish, not capacity.

## Layer 1 — the relay (`derp.2185lab.com`, 2 vCPU / 4 GB)

- **Idle browser friends are almost free.** 100 friends connected at once, each dialing now and then:
  derper used **3.5 % of one core**, 30 MB RAM, and the droplet was bored (CPU < 5 %). Connections are
  not the ceiling; the box would hold many hundreds.
- **An active browser friend (relayed, mid-conversation) costs ~12 KB/s each way and ~1 % of one derper
  core.** Twelve of them at once: 12.5 % of one core, 142 KB/s each way. Extrapolated, one derper core
  saturates somewhere around **80–100 simultaneously-streaming browser friends**; the 2-vCPU droplet,
  ~150–200. Bandwidth is not the limit (100 active friends ≈ 1.4 MB/s, a rounding error on a droplet).
- **No data was dropped.** The `derp_packets_dropped` counter climbs into the thousands under load, but
  that is **`disco` path-discovery chatter to peers that already left** (benign DERP churn). The
  data-carrying drops (`kind=other`) were 0–33 across every run, and none at the connection counts we
  will see. Handshakes stayed 65–90 ms at N=100; nothing timed out.
- **Native (direct) friends put essentially nothing on the relay:** a direct client at N=12 sent the
  relay **0.14 KB/s** vs **11.9 KB/s** for a relayed one — 85× less. Every friend who gets a direct path
  is a friend the relay stops paying for.

**Relay-sizing rule.** Budget **~12 KB/s each way and ~1 % of one derper core per browser friend who is
actively streaming**, and ~nothing per idle or per direct friend. One 2-vCPU / 4-GB droplet comfortably
carries **100+ connected browser friends with a couple dozen streaming at once** — far past a single
demo host's launch day. Add a vCPU (or a second region) per ~80 concurrent streamers beyond that.

## Layer 2 — the host tunnel (one host binary's tailcat server)

- **No leak.** Host RSS was flat at steady load — 83 MB before and after the N=100 session test, 74 MB
  at N=12 of full chat, never climbing run over run. After a burst of 4 MiB bodies a host's RSS rose to
  ~371 MB and did not fall on idle, but that is Go's runtime holding freed memory (live objects were
  12 MB; a fresh host is 30 MB), not a leak — macOS just keeps the reclaimable pages counted. Goroutines
  rise with connected clients and fall to baseline (~115) once tailcat's lazy-peer cleanup runs (a
  ~9-minute wireguard timer, not ours). The host held 100 concurrent sessions without strain.
- **`--relay-only` client shutdown hangs (ticket 035).** A *native* Go client with UDP forced off parks
  for minutes inside tailcat's `Close`. It never touches the host and never touches the browser (wasm) —
  but the future `connect` command must bound its own shutdown so it cannot hang on a permanently-relayed
  network. Filed; the load instrument works around it.

## Layer 3 — the gateway (admission, per-key concurrency, FIFO queue, keepalives, settle)

**This layer did its job in every single run**, which is the important result: it is the structure that
keeps a home engine safe under a crowd.

- **The promise held exactly.** With an engine that serves S=2 at once, the gateway ran **at most 2**,
  let **at most 4 wait** (2×S), and told everyone else to retry — `in_flight` never exceeded 2 and
  `waiting` never exceeded 4 in any run, at any N up to 30. No burst ever reached the engine.
- **A crowd degrades to honest 503s, not a crash.** At N=12 on the 2-slot laptop engine, ~82 % of
  requests got `503 queue_timeout` with a `Retry-After`; at N=30, ~95 %. The served ones still streamed
  at full speed. Nothing hung: **zero requests, in ~4,000 sent, ended without either completing or
  erroring within their deadlines.**
- **Every resource came back.** Bodies-in-flight, slots, and per-key counters returned to zero after
  every run; RSS bounded even when 12 friends each pushed multi-megabyte bodies (transient spike to
  ~370 MB, then released).
- **Streaming while queued works:** a waiting stream gets its `200` head and `: queued` keepalives at
  once, so a busy host reads as busy, not broken; a queued stream that times out ends with an SSE error
  event, which is why some rows read `status 200 · code queue_timeout`.
- **Every abuse and failure shape stayed contained** (each tested once): a 4 MiB+ body → `413` before a
  slot; a burst past a key's `max_concurrent` → `429 concurrency_limited`; one key opening 50 sessions →
  bounded to its own concurrency (`in_flight` max 1), so an invite cannot monopolize the engine; the
  engine killed mid-run → honest `503 upstream_down` then automatic recovery; the relay restarted
  mid-run → sessions re-established; two hosts on one relay → both served. A non-reading client did not
  pin a slot (the host finished and freed it); forcing the 60 s write-deadline cut needs a reply larger
  than the socket buffer, which this mix did not produce — a test gap, noted in `docs/MEASURE.md`.

## Layer 4 — the engine (the real ceiling)

| Engine (this test) | Parallel | Per-request context | Served throughput | Per-stream speed | Prefill |
|---|---|---|---|---|---|
| **llama.cpp** (Gemma 4 E2B Q4, laptop, `-np 2 -c 65536`) | 2 slots | 32K tokens | ~12–13 chats/min | 108–133 tok/s | ~2,800–4,300 tok/s (a 12K prompt ≈ 3–4 s cold) |
| **vLLM** (gemma4-12b-w4a16, workstation, max-num-seqs 2) | 2 seqs | 8K tokens | ~30 chats/min at N=12 | ~62 tok/s | fast; 8K ceiling reached quickly by long chats |

- **Two concurrent long chats is the home-engine ceiling.** Beyond that the gateway queues; the engine
  itself never overcommitted, never OOM'd, never returned a raw 502. "No slot available" surfaced as the
  gateway's `queue_timeout`/`upstream_down`, never as a broken-looking error.
- **vLLM serves more chats per minute** (shorter context = shorter prefill, good batching) **but each
  stream is slower** (a bigger 12B model) and its **8K context is a real wall** for mature conversations —
  long-history prompts hit `422 context_too_long` (and, at the boundary, the ticket-036 defect).
- **llama.cpp's shared context matters:** `-c 65536 -np 2` gives **32K per slot**, so a 30K prompt is
  fine but two 20K prompts on the two slots is the real budget. No `context_too_long` appeared below the
  advertised 32K.

**Recommended public-demo engine and slots.** For the founder's workstation demo, **vLLM with
`--slots` = its real `max-num-seqs`** (2 in this test; set it to the GPU's true batch width on the demo
box) is the right choice: it batches, serves the most chats per minute, and prefix-caches across
requests. Raise `max-num-seqs` as far as the GPUs allow **before** launch — that number *is* the layer-3
`S`, and every extra slot is one more friend who streams instead of queueing. Keep a context that fits a
mature chat (≥ 32K); the 8K test context would wall real conversations. llama.cpp is the right *home*
default (simple, `--metrics` gives the host live truth), but for a public crowd, slots win.

## The founder's stateless-vs-stateful question (not for the demo)

Resending the whole conversation each turn is stateless. Measured cost, from the realistic growing-chat
mix:

1. **Relay bytes (the only place resend shows up):** an actively-streaming browser friend in a mature
   conversation sends **~5 KB/s up** (the history resend) and pulls ~25–30 KB/s down (the reply). The
   uplink grows with chat length — a 16K-token prompt is ~64 KB resent each turn — but even 100 such
   friends is ~0.5 MB/s of uplink at the relay: **not a problem.**
2. **Engine prefill (the real cost) is already recovered by the engines' own caches, with no host
   state:** llama.cpp's slot prompt-cache recovered **52–87 %** of the prompt tokens sent (higher when a
   chat keeps landing on the same slot, lower as N grows and chats bounce between the two slots); vLLM's
   prefix cache hit **~64 %** at N=2 (higher-N reads were contaminated by the shared workstation's other
   traffic — a measurement caveat, not a product one). So most of the "resend" prefill is never actually
   recomputed.
3. **Privacy is unaffected** by staying stateless (the client owns history; the host stores no content —
   BELIEFS Protection 3).

**Recommendation: (a) stateless as-is. Do not build a stateful conversation API.** The numbers say the
resend is not a problem the founder should spend on: the relay cost is negligible, the engines already
cache the prefill, and a stateful API would invert the privacy property and couple the host to one
engine's cache model. **Revisit only if** a future engine ships with *no* prefix cache *and* relay
bandwidth becomes the dominant cost — then the cheapest fix is **(b) prefix-hash slot routing / cache
hints in the gateway** (send a chat back to the slot that already holds its prefix; keeps the client as
the owner of history, adds no host storage), not a stateful API.

## Defects found (own tickets, with repro)

- **035** — `tailcat.Client.Close()` hangs for minutes when the client is relay-only (UDP off). Affects
  the future `connect` command on a permanently-relayed network; not the host, not the browser.
- **036** — the gateway's context pre-check counts the *raw* prompt, but the engine counts the
  *chat-templated* prompt (~13+ tokens more); near the context ceiling a request the gateway admits is
  rejected by the engine as a raw `400`, shown to the friend as `invalid_request` instead of the true
  `422 context_too_long`, so the friend's client retries instead of shortening.

## Audio budgets (078)

`keys add` / `keys limits` accept `--daily-audio-seconds` (default 3600) and
`--daily-speech-chars` (default 200000). Absent or zero legacy fields receive
these defaults in memory; reading a key does not rewrite it. `keys list` shows
both limits. Negative limits mean unlimited, as with the existing limits.
Audio calls share per-key RPM/concurrency and the existing global engine queue;
they do not spend tokens. Model allowlists and the host's model pin apply to audio
model IDs too; include them in a host pin if using one.

Before dispatch, transcription reserves measured duration from complete PCM or
IEEE-float WAV headers, or FLAC STREAMINFO. Other containers (including MP3,
OGG/Opus, and MP4/M4A in this slice), missing duration, or unparseable headers
reserve `--max-transcription-seconds` (default 300). A measured upload above this
ceiling is refused. Bytes are never called seconds. Speech reserves the number
of Unicode code points in `input`. Live reservations count against the daily
allowance so parallel requests cannot reserve the same remaining capacity.
Insufficient room returns `audio_budget_exhausted` or `speech_budget_exhausted`
(429, Retry-After to the next UTC midnight) before the engine is called.

Default or `response_format=json` requests are sent upstream as `verbose_json`
so the host can reconcile duration, then returned to the client as `{"text"}`.
Explicit `verbose_json` is passed through. `text`, `srt`, and `vtt` responses
have no duration metadata and charge the reservation (300 seconds for an
unmeasurable upload, or its measured container duration), even for short clips.

Before-dispatch failures release reservations. After dispatch, a complete
transcription response's finite nonnegative `duration` is charged in full;
otherwise the reservation is charged. Interrupted calls charge their reservation.
Unknown-duration fallback charges set `seconds_estimated:true`. Speech charges
its reserved characters after dispatch, including interrupted calls. The UTC day
of settlement is used for both live charges and history replay.

The bound for unmeasurable audio is on admission, not actual decoded duration:
a file may exceed its policy reservation. Its full engine-reported duration is
recorded as `seconds`, with the reservation in `reserved_seconds` and the excess
in `overrun_seconds`. An overrun can put the day over budget; subsequent requests
are refused until the next UTC day. Already-dispatched calls still settle in full.
The host ceiling does not truncate audio or falsify the engine's measurement.
For refused calls, `reserved_seconds` names the requested reservation, not a
charge; `seconds` is zero/absent, and no reservation remains held.

## Remote console

Opt-in admin access has budgets separate from friend keys: reads allow 240 starts per rolling
minute and six concurrent requests; writes allow 20 starts per rolling minute and one concurrent
request. Reads cannot spend the write budget. Saturation returns 429 with Retry-After. No model
token, audio, or queue budget is charged to the admin code. The request body is bounded to 16 KiB,
the proxied response to 8 MiB, and loopback forwarding to ten seconds. Caller headers and response
authorization headers are not forwarded. No redirects are followed. Off is 404, not a login hint.
Rotation/off refuse subsequent old-bearer requests; already accepted operations are not replayed.

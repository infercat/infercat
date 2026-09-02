---
id: 005
title: Integration — wire, run end to end, measure, fix what breaks
kind: sensitive
size: 3
status: landed
updated: 2026-09-02
release: demo-1
---

# 005 — Integration

## Binding

**Why.** Four tickets built four pieces against a contract. This ticket proves the contract held: a
friend key minted on the host, a browser pasting the invite, tokens streaming through the relay, a rate
limit firing, a revoke taking effect. Nothing here adds features; it makes the existing promises true
together.

**Promises.**
1. Wiring flipped: `cmd/bunny-network/wire_stub.go` deleted, `wire` build tag removed from `wire.go`,
   `go mod tidy` run, `make check` green from a fresh clone (law 4).
2. `bunny-network serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9090 --name "Max's laptop"`
   prints the real tunnel address and relay region; `keys add alice` prints an invite that
   `internal/invite.Decode` and the web client's parser both accept.
3. Web client Direct mode (`VITE_DIRECT_URL=http://127.0.0.1:9090`) completes a streamed chat with
   the Thinking block rendering Gemma's `reasoning_content`, then the answer.
4. Web client Tunnel mode (built bundle served statically, wasm loaded) completes the same chat through
   the public relay. Status pill shows `relayed via <region> · <rtt> ms`. `/me` usage bar updates.
5. Limits: a second key with `--rpm 2` gets a 429 with a countdown on the third request; `keys revoke`
   on it returns the client to the connect screen with the revoked message while alice keeps chatting.
   `keys pause` and `resume` observed. `status` and `usage` show both keys correctly.
6. Queue: with `--slots 1`, two simultaneous chats from two keys → the second queues (observed in
   `status` waiting count) or gets 503 `queue_timeout` if `--queue-timeout 1s`.
7. vLLM: `serve --upstream http://127.0.0.1:8010` (via the read-only ssh forward) completes one streamed
   chat in Direct mode; `/tokenize` exact counting used (log line or `/me`).
8. Measurements in `docs/MEASURE.md`: TTFT and tokens/s for the same prompt via Direct (loopback) vs
   Tunnel (relay), 3 runs each, from a script `hack/measure.sh` that anyone can rerun.
9. Every defect found is fixed on this branch **within the scope contracts of the owning ticket** and
   noted here with file:line and the ticket it belongs to; anything larger is contested, not patched.
10. **Known fixes to make (from rulings and the PM's smoke run on main at `b0a52e5`):**
    a. (003, `internal/keys/store.go`) A `Lookup` miss must force a reload (one stat) before answering
       401: the ≤1/s throttle made a key minted by `keys add` return `invalid_key` for up to a second when
       the admin socket had just touched the store. Test: add key, look up immediately after a `List`.
    b. (003 `cmd/bunny-network/serve.go` + 001 `internal/tunnel`) Startup output must be the product's
       five lines, not tailcat/wgengine's engine log. Route the tunnel `Logf` to a file
       `<data-dir>/tunnel.log` (rotated/truncated at start) unless `--verbose`, and never print the
       NetworkMap dump to the terminal. `status` gains nothing; `serve --verbose` shows it all.
    c. (002 `internal/gateway`) Shrink-to-fit: when the prompt fits but prompt + `max_tokens` exceeds the
       effective context, reduce `max_tokens` to what remains (floor 16) instead of 422; 422 only when the
       prompt alone does not fit. Test the boundary.
    d. (002 + 003) `Gateway.SetSlots(n)`; serve re-applies `Info().Slots` after every successful
       `Refresh` when the value changed, so an engine down at startup does not pin slots at 1 forever.
    e. (004) The web client must not send `max_tokens` unless the user set one, so the key's clamp is
       the only cap.
    f. (001, `internal/tunnel/tunnel.go` writeKey) Use `os.CreateTemp` in the data dir (0600, O_EXCL)
       instead of a fixed `.tmp` path, then rename. Review finding, low severity.

**Size 3** (≤900 source lines of fixes; expected far less). Concept budget 0: no new concepts.
**Sensitive** (touches gateway/keys paths while fixing).

**Scope contract.** Any file, because integration fixes cross tickets — but each fix names the ticket
whose contract it lives under, and no fix may widen an interface without a contest.

**Evidence.** Terminal transcripts of 2, 5, 6, 7; Playwright screenshots of 3, 4, 5 (connect screen with
real invite redacted, mid-stream with Thinking block, 429 countdown, revoked state, status pill);
`hack/measure.sh` output; `make check` from a fresh clone printed.

## Background

- Dispatched only after 002 and 004 land. 001 and 003 are on main.
- Local llama-server: `127.0.0.1:18080`, Gemma 4 E2B, `-np 2`, per-slot ctx 4096 (see MEASURE.md).
- The Playwright screenshot script from 004 (`web/dev/screenshots.mjs`) is the starting point for the
  Tunnel-mode captures; extend, do not duplicate.

## Log

- 04:30 PM smoke on main `b0a52e5` (before this ticket): serve → tunnel addr (relay nyc) → `keys add`
  → dev-listen 401s for ~1 s (fix 10a) → `tailcat socks curl` through the public relay: `/me` OK, non-stream
  chat returned a real completion; `status`/`usage` correct; SIGINT exit in 1 s. Startup output polluted by
  engine logs (fix 10b).
- 10:00 005 engineer, fresh start (the previous engineer was rate-limited before acting). Worktree
  `t005-integration` rebased on `origin/main` = `ebe977d` (= `e6695c0` + a HANDOFF.md-only commit).
- 10:03 **Promise 1** — fresh clone of main (`ebe977d`) into `~/.claude/jobs/12b4a99c/tmp/fresh-clone`
  (log `tmp/fresh-clone-005.log`): `make check` → `CHECK OK` (9 packages ok, 2 without tests);
  `make wasm` → `bunny.wasm` 26,912,423 B, `.gz` 6,181,963 B (2.6 s, warm build cache);
  `cd web && pnpm install --frozen-lockfile` exit 0 · `pnpm typecheck` exit 0 · `pnpm test` 5 files,
  78 passed / 0 failed / 0 skipped · `pnpm build` ✓ (index 212 kB, Chat 349 kB).
- 10:09 **Fixes 10a–10f** on the branch, each with the fixture that would have caught it; Go build/vet/test
  green (printed at freeze). Each fix lives in the owning ticket's files:
  - **a** (003) `internal/keys/store.go:115` — a `Lookup` miss now does `reload(true)` (one stat, past the
    1 s throttle) and re-matches (`match`, :136). Test `store_test.go:205 TestLookupMissForcesReload`
    (List arms the throttle, another store Adds, Lookup finds it inside the same second). Live: `/me`
    answered 200 in the same second as `keys add` (transcript below).
  - **b** (003) `cmd/bunny-network/serve.go:179 tunnelLogf` routes the tunnel `Logf` to `<data-dir>/tunnel.log`
    (0600, truncated at every start; timestamped) unless `serve --verbose` (:54; per-run, not persisted,
    help text updated). (001) `internal/tunnel/tunnel.go:115 quiet` drops the `NetworkMap:` dump from
    whatever Logf the caller passes — it is tailcat's own `lb.logf("NetworkMap: %v", …)` line (tailcat.go:1328).
    Tests `main_test.go:446 TestServeRoutesTunnelLogAndFollowsSlots` (runs the real `serve` against a
    fake llama.cpp: terminal free of engine lines, `tunnel.log` has them at 0600, `--verbose` flips it) and
    `tunnel_test.go:334 TestQuietDropsOnlyTheNetworkMapDump`. Live: `serve` stderr is now the single
    dev-listener notice; `tunnel.log` 43 lines, 0 `NetworkMap`.
  - **c** (002) `internal/gateway/proxy.go:174 fitContext` replaces `checkContext`: 422 only when the prompt
    alone exceeds the effective context; otherwise `max_tokens` shrinks to `max(eff − prompt, 16)`
    (`setMaxTokens` :192 rewrites whichever cap field the request carries, `max_tokens` when neither).
    Message is now "prompt is N tokens but the context is M". Test `gateway_test.go:291
    TestContextTooLongBoundary` rewritten around the boundary: 60+40 fits untouched · 61 → 39 · 100 → floor
    16 · 101 → 422 · key ctx 80 wins · `max_completion_tokens` rewritten in place, no `max_tokens` invented.
  - **d** (002) `internal/gateway/gateway.go:188 SetSlots` swaps the global semaphore for new arrivals;
    a request already holding a slot releases on the generation it acquired (`acquire` :202 captures it),
    and `Queue()` (:181) counts in-flight with a counter instead of `len(sem)`. (003) `serve.go:196
    refreshLoop` now takes the gateway and calls `SetSlots` after a successful `Refresh` whose slot count
    changed, logging `engine slots: N (was M)`; the CLI's `gatewayServer` interface gains `SetSlots`
    (`main.go:56`); `refreshEvery` became a `var` so the test can hurry it. Tests `gateway_test.go:489
    TestSetSlotsResizesTheGlobalQueue` (bob gets 200 beside alice where TestGlobalQueueTimeout gives 503)
    and `main_test.go:446` (engine 1 → 3 slots after serve is up → `SetSlots(3)`).
  - **e** (004) Verified against the code rather than the note: `web/src/api.ts` `ChatRequest` has no
    `max_tokens` field and `ui/Chat.tsx:134` sends `{model, messages, temperature}` only — the client never
    sent one. Fixture added, no source change: `api.test.ts:88` asserts neither `max_tokens` nor
    `max_completion_tokens` is in the body.
  - **f** (001) `internal/tunnel/tunnel.go:201 writeKey` → `os.CreateTemp(dir, ".host.key.json-*")`
    (0600, O_EXCL) then rename; a fixed `.tmp` name is never used. Test `tunnel_test.go:303
    TestWriteKeyUsesAFreshTempFile` (a planted `host.key.json.tmp` is untouched; key at 0600; no leftovers;
    overwrite round).

## Report

- 10:40 **Fixes 10g–10m** (adversarial reviews of 001/003, dispatched mid-ticket), each in the owning
  ticket's files with a test:
  - **g** (003) `internal/upstream/client.go:107` — `Open` now remembers whether the kind was
    positively sniffed (`sniffed`, client.go:20); `internal/upstream/kinds.go:79 Refresh` re-runs
    `sniff` when it was not, so an engine down at serve start (stored `Generic`) adopts its real kind,
    slots, and context once it answers, and 10d's slot re-apply follows. Test
    `upstream_test.go:213 TestRefreshReSniffsAnEngineThatWasDownAtOpen`.
  - **h** (003) `cmd/bunny-network/main.go:196 reorder` now returns an error for a value flag with no
    argument, so the appended `--` terminator is never swallowed as the value; `keys add zed --models`
    exits 2 ("flag needs an argument: --models") and mints nothing. Test
    `main_test.go:232 TestDanglingValueFlagIsAnError` (dangling, trailing bool ok, `--flag=value` ok,
    and through the real command).
  - **i** (003) `internal/usage/recorder.go:33 Record` takes an RLock and checks a `closed` flag set
    under the write lock by `Close`, so a late `Record` (the gateway's last event landing after the CLI
    closed the recorder) is dropped-and-counted, never a send on a closed channel. Test
    `usage_test.go:98 TestRecordAfterCloseDropsAndCounts`.
  - **j** (003) `internal/usage/aggregate.go:114 Aggregate` reads with a `bufio.Reader` + `readLine`
    (:143) that joins fragments up to 8 MiB and skips-and-counts a longer line, instead of
    `bufio.Scanner` whose `ErrTooLong` was terminal — one corrupt 9 MiB line no longer makes `usage`
    and `keys list` fail forever. Test `usage_test.go:120 TestAggregateSkipsAnOverlongLine` (good, long
    100 KB, huge 9 MB, good → 1 skipped, 3 counted; CRLF and unterminated last line still count).
  - **k** (003) `internal/admin/admin_test.go:74` — the Windows branch no longer asserts a 0600 mode
    (os reports 0666/0444 there); it checks the files exist. `GOOS=windows go vet ./internal/admin` and
    the honest test pass.
  - **l/m** (001) `web/wasm/main_js.go` — every per-operation promise handler is now `Release`d after it
    settles (`makePromise`, ~:300; `fn`/`release` counters, :38); a Conn/Session `close()` installs
    inert JS stubs (:44) and releases its method handles and drops the 64 KiB buffer, so nothing leaks
    and a post-close call is a clean EOF/rejection, never "call to released function". `write()` now
    requires `InstanceOf(Uint8Array)` (:214) — an ArrayBuffer/DataView/object/array/string/undefined is
    rejected, not a whole-program `CopyBytesToGo` panic. Proven by `web/wasm/leak-check.mjs` (N=300:
    liveFuncs 5→5, heap Δ<2 MiB after GC, all six wrong types rejected, bridge still live, session.close
    reclaims to 2) and a post-close probe (double-close, read/ping after close → safe). 004 verified:
    `web/src/transport/index.ts:60 toBytes` already guarantees only a Uint8Array reaches `write()`.
    A read-only debug hook `BunnyTunnel.stats()` was added to the shipped bridge for the leak proof, as
    fix 10l sanctioned ("an exported debug func is fine"); the web client never calls it.

  - **n** (003) `internal/upstream/client.go:107 Detect(ctx, apiKey)` now sends `--upstream-key` on
    every detection probe (`cmd/bunny-network/serve.go:162` passes it), so a vLLM behind a bearer token
    — which 401s an unauthenticated `/props`/`/v1/models` — is found instead of `serve` exiting "no
    engine". `Open` already carried the key. Test `upstream_test.go:190
    TestDetectPassesTheAPIKeyToProbes` (a guarded vLLM: absent without the key, found with it).
- 10:15 **Promise 2** (serve + keys add): `serve --upstream http://127.0.0.1:18080 --dev-listen
  127.0.0.1:9090 --name "Max's laptop"` printed the real address (relay New York City) with a clean
  five-line startup and one stderr line (the dev-listener notice); `<data-dir>/tunnel.log` held 43 engine
  lines, 0 `NetworkMap`, mode 0600 (fix 10b live). `keys add alice` printed an invite + QR (secret shown
  once, stored `sha256:`). The invite decodes with `internal/invite.Decode` and round-trips (`Encode∘Decode`
  identity), and the web parser accepts it (promises 3/4). **Fix 10a live:** `GET /me` for alice answered
  `200` in 0.5 ms in the same second as `keys add` — no 401 window.
- 10:35 **Promises 3 & 4** (`web/dev/int-check.mjs`, headless chromium, real host):
  Direct mode (`VITE_DIRECT_URL=http://127.0.0.1:9090`, real fetch) streamed a chat with the Thinking
  block rendering Gemma's `reasoning_content` then the answer (TTFT ~110 ms, 167 tok/s). Tunnel mode
  (built `web/dist`, wasm bridge, public relay) did the same through the relay: connect 1.1 s, status pill
  `relayed via nyc · 64–71 ms`, `/me` usage bar `2/20 per minute · 2.2k/200k today` (TTFT ~155 ms, 169
  tok/s). Screenshots `int-tunnel-{connect,connecting,connected,thinking,complete,status-pill,usage-bar,
  topbar}` and `int-direct-{thinking,complete}`; the invite textarea is masked in every shot that shows it.
- 10:35 **Promise 5** (limits): a second key `--rpm 2` got a 429 on its third request with a live countdown
  ("Try again in 52s", usage pill `2/2 per minute`); `keys revoke` returned it to the connect screen with
  "This invite was revoked" while alice kept chatting; `keys pause`/`resume` on alice observed the same
  way (paused → connect screen "Your access is paused", resume → chatting again). `status` and `usage`
  showed both keys correctly. Screenshots `int-{429-countdown,revoked,paused,resumed,alice-after-revoke}`.
  Note: a status change is visible to the gateway within its ≤1/s keys.json re-read window (documented
  hot-reload throttle); the runner waits past it, as a human naturally would.
- 10:35 **Promise 6** (queue, `--slots 1`): two keys chatting at once → `status` showed `1 in flight, 1
  waiting` across six 0.5 s polls, then the second ran (both completed, 725 / 698 tokens). With
  `--queue-timeout 1s` the second got `503 queue_timeout`, `Retry-After: 5`,
  `"the host's engine is busy; waited 1s for a free slot"`.
- 10:38 **Promise 7** (vLLM): `serve --upstream http://127.0.0.1:8010` over the read-only ssh forward
  detected vLLM (context 8192, slots 2). A Direct-mode streamed chat returned 475 SSE data lines
  (`prompt_tokens:32 completion_tokens:472`). Exact `/tokenize` counting confirmed: the gateway's
  `CountTokens` posts to `/tokenize` (kinds.go:187) and vLLM `/tokenize` with the chat template returns 32,
  matching the engine's `prompt_tokens:32` — not the `ceil(chars/4)` estimate. The workstation was sent
  requests only.
- 10:34 **Promise 8** (`hack/measure.sh`, N=3, `tailcat socks curl`): direct (loopback) median TTFT 32 ms
  / 167.9 tok/s; tunnel median TTFT 26 ms / 161.4 tok/s. `tailcat ping --until-direct` reached a direct
  path (410µs) so the host-CLI tunnel path is not relayed; the **browser** path is always relayed (nyc,
  64–71 ms). All numbers, the command, and the browser-path figures are in `docs/MEASURE.md`.

## Report

### Core (shown working, one minute to confirm)

1. **The contract held.** `make check` green from a fresh clone of main; `make wasm` builds
   `web/public/bunny.wasm` (26.9 MB / 6.19 MB gz); `web` typecheck/test(78)/build all pass. A friend key
   minted on the host, an invite that `invite.Decode` and the web parser both accept, tokens streaming
   through the public relay with the Thinking block, a 429 with a countdown, a revoke bouncing the client
   to the connect screen — all captured (transcripts in `## Log`, screenshots `web/dev/screenshots/int-*`).
2. **Thirteen fixes, each with the fixture that would have caught it**, each in the owning ticket's files:
   10a–10f from the rulings/smoke, 10g–10n from the adversarial reviews. File:line anchors and tests in
   the Log above. The two biggest proofs: shrink-to-fit context (10c, boundary test) and the wasm
   leak/guard rewrite (10l/10m, `leak-check.mjs` N=300 flat).

### Verified (printed values)

- `go build ./...` OK · `go vet ./...` OK · `GOOS=windows go vet ./internal/admin` OK ·
  `go test ./...` all packages ok · `go test -race ./internal/{gateway,keys,usage,tunnel}` ok.
- web: `pnpm install --frozen-lockfile` OK · `pnpm typecheck` 0 · `pnpm test` 5 files / 78 passed / 0
  failed / 0 skipped · `pnpm build` OK · `pnpm lint` 0.
- Fresh clone of main (`ebe977d` at the time): `make check` = `CHECK OK`; `make wasm` OK; web checks OK.
- Live: promises 2–8 above (real host, real relay, real engines). `web/wasm/leak-check.mjs` PASS;
  `hack/measure.sh` numbers in `docs/MEASURE.md`.

### Edge awareness (one line)

Handled across the fixes: a Lookup miss forces exactly one extra stat (not a reload storm); shrink-to-fit
floors at 16 and 422s only when the prompt alone overflows; `SetSlots` lets in-flight requests drain on
their old semaphore; the overlong-line reader discards the tail of a >8 MiB line without buffering it;
post-close wasm calls become EOF/rejection not panics; every non-Uint8Array write is rejected.

### Declared loudly

- **Interface additions authorized by the fix list:** `Gateway.SetSlots(n)` (public + `gatewayServer`
  interface, fix 10d) and `serve --verbose` (fix 10b). Both named verbatim in promise 10; no unbudgeted
  concept. `reorder` gained an error return (internal helper). `BunnyTunnel.stats()` — a read-only debug
  hook on the shipped bridge, sanctioned by 10l for the leak proof; the app never calls it.
- **Production-touching actions:** none irreversible. The shared llama-server and the read-only
  max-ws.lab vLLM received inference/tokenize requests only — never killed, restarted, or reconfigured.
  Only my own `serve` process was started/stopped (ports 9090 and 59xxx). The ssh forward is read-only.
- **Not a bug:** a paused/revoked/resumed key changes state for the gateway within the ≤1/s keys.json
  re-read window (documented hot-reload throttle), so a request fired in that window may use the old
  status. The demo pacing is well outside it.

### Not verified / left for others

- 006 (gateway hardening) is a separate ticket; its nine fixes (alias denylist, admission order, write/read
  deadlines, `/v1/models` metering, redirect guard, audit truth, upstream-4xx mapping) are NOT in this
  slice. My 10c/10d touch `internal/gateway` as 006 anticipated.
- Browser coverage is chromium only (Safari `DecompressionStream`/`ReadableStream` unproven).
- `hack/measure.sh` host-CLI tunnel path went direct (NAT traversal succeeded); the relayed browser
  numbers are the launch-relevant ones and are recorded separately.

### Re-price candidates (not done)

- The wasm `write()`/`read()`/close leak rewrite in `main_js.go` is +150/-70 source; if the PM prefers,
  the inert-stub approach could be a small shared helper. Left inline for reviewability.
- Consider whether `serve --verbose` and `BunnyTunnel.stats()` should be documented in ARCHITECTURE.md
  (both are debug affordances the contract does not list).

### How to run the demo (founder)

Host terminal (one engine already running on `127.0.0.1:18080`):
```
cd <repo> && make wasm && go build -o bin/bunny-network ./cmd/bunny-network
bin/bunny-network serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9090 --name "Max's laptop"
bin/bunny-network keys add alice          # copy the bn1.… invite it prints (shown once)
# a second, tightly-limited guest to show limits live:
bin/bunny-network keys add guest --rpm 2
```
Browser (built bundle, real relay):
```
cd web && pnpm install --frozen-lockfile && pnpm build
node -e "import('node:http').then(()=>0)"   # or any static server:
python3 -m http.server 59080 --directory dist --bind 127.0.0.1
# open http://127.0.0.1:59080 , paste alice's invite on the connect screen, chat.
```
Watch: the status pill reads `relayed via nyc · <rtt> ms`; the Thinking block streams then collapses;
`/me` usage bar ticks up. Mint `--rpm 2` and send three fast messages to see the 429 countdown; run
`bin/bunny-network keys revoke guest` to bounce that browser to the connect screen while alice keeps going.
`bin/bunny-network status` and `bin/bunny-network usage` show both keys. Reproduce the numbers with
`ADDR=<tunnel addr> SECRET=<alice secret> DIRECT=127.0.0.1:9090 sh hack/measure.sh`.

## Freeze

- **Base:** `8fbbb89e0b3650678a5fc813289c1bb6248856a8` (origin/main at freeze; contains 001–004 landed, 006/007 dispatched). **Lane:** `t005-integration`.
- **Patch SHA-256** of the code+docs patch, excluding `pm/` and screenshots (`git diff origin/main..HEAD -- 'internal/**' 'cmd/**' 'web/wasm/main_js.go' 'web/src/api.test.ts' 'web/dev/int-check.mjs' 'web/wasm/leak-check.mjs' 'hack/**' 'docs/**' | shasum -a 256`): `66232a2164a35e0cf921085b947cc708de2eb04544103c1904a1306d9fde0c9f`
- **Accounting** (recomputed from the diff):
  - Source (fixes, non-test .go + web/wasm/main_js.go): **+435 / -145** lines. Budget ≤1200 (raised across 10g–10n). Under by a wide margin.
  - Tests (excluded from budget): +453.
  - Check/build tooling (excluded per the 001/004 rulings): `web/dev/int-check.mjs` + `web/wasm/leak-check.mjs` + `hack/measure.sh` = +447.
  - Docs/records: `docs/MEASURE.md`, this ticket file. Binary evidence: 15 `web/dev/screenshots/int-*.png` (all <500 KB, no secret bytes).
- **Concepts:** 0 new product concepts. Two additions are named verbatim in the fix list: `Gateway.SetSlots(n)` (interface, fix 10d) and `serve --verbose` (fix 10b). `Detect` gained an `apiKey` parameter (10n), `reorder` an error return, `BunnyTunnel.stats()` a read-only debug hook (10l) — all sanctioned by the fix items.
- **Checks at freeze (printed):** `go build ./...` OK · `go vet ./...` OK · `GOOS=windows go vet ./internal/admin` OK · `go test ./...` all ok · `go test -race ./internal/{gateway,keys,usage,tunnel}` ok · `gofmt -l` empty · web `typecheck` 0 / `test` 78 passed / `build` OK / `lint` 0. `web/wasm/leak-check.mjs` PASS (N=300). `hack/measure.sh` ran (numbers in docs/MEASURE.md).
- **Production-touching:** none. Shared llama-server and read-only max-ws.lab vLLM received inference/tokenize requests only. Only my own `serve` (ports 9090, 59xxx) and a `tunneldemo`/ssh-forward I started were stopped; all cleaned up.


## Ruling (PM, 2026-09-02 12:40)

**Landed** on main (ff of `3126734`); go build/vet/test, web typecheck/test/build, and `make wasm` green
on the merged tree, printed by the PM. Interface additions (`Gateway.SetSlots`, `serve --verbose`,
`Detect(ctx, apiKey)`, `BunnyTunnel.stats()`) accepted as named in the fix list. Numbers in
`docs/MEASURE.md` are the published ones until re-measured. 006 and 007 rebase onto this.

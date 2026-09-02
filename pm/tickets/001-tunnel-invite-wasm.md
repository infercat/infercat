---
id: 001
title: Tunnel wrapper, invite format, and browser wasm bridge
kind: sensitive
size: 3
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 001 — Tunnel wrapper, invite format, and browser wasm bridge

## Binding

**Why.** Everything else stands on this: the host's tailcat server, the invite string a friend pastes,
and the wasm bridge the web app dials through. It is the novel piece nobody has exercised; it lands first.

**Promises (must be true when done).**
1. `internal/tunnel`: `Start(ctx, Options)` creates or loads the host key at `<DataDir>/host.key.json`
   (tailcat `PrivateKey` JSON, region pinned) and starts a `tailcat.Server`. `Addr()` is identical across
   restarts with the same data dir; `Ephemeral: true` never touches disk and yields a new address.
   `SavedAddr(dataDir) (string, error)` derives the address from the saved key **without** starting a
   server (ticket 003 prints invites from it when the daemon is not running).
2. `Listener()` is a `net.Listener` whose `Accept` yields exactly the connections addressed to tunnel
   port 80. Any other port gets a nil handler. `OnTCPForward`, `AllowProxy`, SSH and file services are
   never set (Protection 1). `Status()` reports address, relay region name, start time, and connected
   client count. `Close()` stops accepting and closes the tailcat server.
3. `internal/invite`: `Encode(addr, secret) string` and `Decode(s) (Invite, error)` implement
   `bn1.<tc…>.<secret>` exactly as `docs/ARCHITECTURE.md` specifies. Distinct error values for: wrong
   or missing prefix (message says "needs a newer app" for unknown `bn<N>`), wrong part count, empty
   part, secret containing characters outside base64url. Whitespace is trimmed. Round-trip property test.
4. `web/wasm`: a `GOOS=js GOARCH=wasm` Go package exposing `window.BunnyTunnel` with **exactly** the
   API in `docs/ARCHITECTURE.md` §wasm bridge. `connect` builds one `tailcat.Client`, resolves after the
   first successful ping (retry loop like tailcat's `pingUntil`, 60 s cap), and `dial` reuses that client
   (`DialTCPPort`) with no new handshake. `ping` reports rtt, via, and direct. Reads are pull-based with
   backpressure (copy tailcat's `makeJSConn` approach). Build tags are pinned in `web/wasm/build-tags.txt`,
   generated once from tailcat v0.4.0's `internal/buildtags.WasmTags()` (run a throwaway `go run` inside
   the tailcat clone to print it). `web/wasm/build.sh` produces `web/public/bunny.wasm`,
   `web/public/bunny.wasm.gz`, and copies `wasm_exec.js` from `$(go env GOROOT)/lib/wasm/`. The build
   passes from a fresh clone (`GOTOOLCHAIN=auto`).
5. `hack/tunneldemo/main.go`: starts the tunnel with a given data dir and serves a tiny HTTP handler on
   the tunnel listener (`GET /healthz` → `{"ok":true}`, `GET /stream` → 20 SSE events 100 ms apart, flushed).
   Prints the address on stdout. Ticket 004 develops against it.
6. `web/wasm/demo.html` + `demo.js`: loads the bridge, takes an address, connects, dials 80, sends a raw
   `GET /healthz HTTP/1.1` and prints the response bytes and a `ping()` result. This is the manual and
   automated check for promise 4.
7. **Evidence of completion**, in the report appended to this file:
   - `go test ./internal/tunnel/... ./internal/invite/...` output, including an integration test that
     starts the tunnel server and a `tailcat.Client` in-process, dials port 80, and gets the healthz
     body. Use tailcat's local DERP test affordance (`TS_DEBUG_TAILCAT_LOCAL_DERP=1`; read how
     `tailcat_test.go` uses it) so the test needs no network; if that proves impossible, say so and gate
     the test behind an env var with the public relay.
   - A test that dialing port 81 does not reach the handler.
   - The demo page run in a real browser (Chrome via the browser tool if available, else Playwright
     chromium) against `hack/tunneldemo` over the public relay: paste the printed `/healthz` response and
     the `ping()` result (rtt, via). Record the wasm.gz size in bytes.
   - The measured numbers go into the report verbatim; the PM copies them to `docs/MEASURE.md`.

**Size 3** (≤900 source lines, tests excluded). Concept budget 3: host key file, invite, session.
**Sensitive** (Protection 1): tests at "coverage plus adversarial fixtures" for promises 2 and 3; an
adversarial review runs after landing. Promise 4 is verified by the demo page, not by unit tests.

**Scope contract (files you may touch).** `internal/tunnel/**`, `internal/invite/**`, `web/wasm/**`,
`hack/tunneldemo/**`, `go.mod`, `go.sum`. Nothing else. Need another file → stop and contest.

**Non-goals.** No CLI (003). No gateway (002). No web app (004). No WebRTC. No keep-alive pooling.
No allowlist / `AddAllowedClient`. Do not vendor tailcat; import it.

**Handoff.** Worktree branch `t001-tunnel-invite-wasm`, rebased on current `main`, checks green
(`go build ./... && go vet ./... && go test ./...` printed, not chained into a push). Append your report
under `## Report` below: what is verified, what is not, measured numbers, and any judgment calls. Commit
the ticket file change on your branch. Push the branch. Do not merge.

## Background (hypotheses and pointers; re-verify against the code)

- tailcat v0.4.0 source is cloned at `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/tailcat` (read-only
  reference; also in the module cache). Key files: `tailcat.go` (Server: `Key`, `Logf`, `Region`,
  `DERPMapURL`, `OnTCP func(port) func(net.Conn)`, `ServedTCPPorts []filter.PortRange`, `Start`,
  `ConnBlob`, `Status`, `Close`; Client: `Server ConnBlob`, `Key`, `DERPMapURL`, `Ping`, `DialTCPPort`),
  `web/main_js.go` (`makeJSConn`, `pingUntil`, promise helpers), `internal/wasmbuild/wasmbuild.go`,
  `internal/buildtags/buildtags.go` (`WasmTags()` — internal, so print it once and pin),
  `cmd/tailcat/tailcat.go` (how `genkey` persists `PrivateKey` JSON with a pinned region, and how
  `--full-address` / `resolve` produce a self-contained blob).
- Ping "via" strings come from tailcat's ping result; the wasm build short-circuits endpoint
  advertising on `GOOS=js`, so `direct` is always false in the browser today. Report it honestly.
- `Status().Clients`: tailcat's `Server.Status()` (ipnstate) may or may not expose peers; if not, count
  accepted connections and say so.
- Go 1.27 toolchain is auto-downloaded (`GOTOOLCHAIN=auto` is set in the Makefile). Homebrew `tailcat`
  0.4.0 is installed for CLI cross-checks (`tailcat ping <addr>` from the host is a good sanity test).
- Public relays are rate-limited; the demo check may see throttling. Note observed rtt anyway.

## Log

All times 2026-09-02 EDT, engineer Claude Fable 5.1 on the founder's laptop.

- 02:03 ACK. Base `f171164` (origin/main), lane `t001-tunnel-invite-wasm`. Read BELIEFS, ARCHITECTURE, ticket, seams, tailcat v0.4.0 (`tailcat.go`, `web/main_js.go`, `cmd/tailcat`, tests).
- 02:10 Finding: `Server.ConnBlob()` always embeds the full DERP region; the short form is built from `ConnInfo{ServerPublic, ServerDiscoPublic, RegionID}` the way `cmd/tailcat` does. `ConnBlob()` strips region code/name on encode.
- 02:11 Finding: `TS_DEBUG_TAILCAT_LOCAL_DERP` is a `cmd/tailcat` affordance (`runDevDERP`); the library tests use `tstest/integration.RunDERPAndSTUN`, which imports ipnlocal/testcontrol. Chose to mirror `runDevDERP` (derpserver + STUN, ~45 test lines) and point `Start` at it via `Options.DERPMapURL` + `Region: "1"`. No network in tests.
- 02:12 Finding: `Server.Status()` builds an `ipnstate.Status` with `WantPeers=false`; no peer list is reachable. `Status().Clients` = distinct clients with an open port-80 connection (documented).
- 02:13 Printed `buildtags.WasmTags()` with a throwaway `cmd/printwasmtags` in a scratch copy of the module (`/tmp/tailcat-tags`, not the read-only clone); 82 tags pinned in `web/wasm/build-tags.txt`. Differs from the release list by keeping bakedroots/c2n/dbus/gro/ssh omitted.
- 02:16 Confirmed in tailscale.com source: netstack's `acceptTCP` never closes a handed-over conn; gVisor runs handlers in goroutines; `gonet.TCPConn.RemoteAddr()` can be a nil interface (guarded).
- 02:18 invite + tunnel written; `go test` green. `-race` run then caught a real bug: `deliver` after `Close` had two ready `select` cases, so a conn could be parked after the drain. Rewrote the listener as a mutex-guarded queue + wake channel; 3/3 race runs green.
- 02:20 PM update received: headless Playwright, not the Chrome tools. Playwright + chromium installed in scratch `/tmp/bunny-pw` (default browser cache), driver at `web/wasm/demo-check.mjs`.
- 02:22 `hack/tunneldemo` up over the public relay: region auto-picked = nyc (301). Demo page in headless Chromium: healthz 200 through the tunnel, 20 SSE events. CLI cross-check `tailcat ping`: ~28 ms via DERP(nyc).
- 02:25 Browser `ping()` reported `0.1 ms via DERP(301)` — wrong. Root cause after logging: `Client.DiscoPing` gets no pong under js/wasm (10 s `context deadline exceeded`), and the fallback `Client.Ping` returns instantly after the first handshake (its `meowWait` is already closed), so neither is an RTT. Judgment call: `ping()` measures a TCP connect to port 80 through the relay and names the relay from the DERP map (or the embedded relay's hostname). Declared below.
- 02:28 Size: first coherent cut 904 Go lines vs 900. Trimmed comments and two type aliases only (no behavior change) to 885.
- 02:30 Restart with the same data dir: address byte-identical. `build.sh` re-run failed on `cp` over the read-only `wasm_exec.js`; fixed (`cp -f` + chmod). `tunneldemo` now refuses a non-loopback `-demo-listen` before touching the relay.
- 02:31 `git fetch && git rebase origin/main`: already up to date. Checks printed below. Frozen.

## Report

### Core (what must be true, shown working)

**1. Host key + stable address.** `tunneldemo -data-dir /tmp/bunny-t001-data` printed
`tco2FwWCAfk4JLOfZHStPar2gxsR6SS-jhyJbRKNLhxUCkmiixPmFrWCDKspO0yl-RjsjwW8ZYHMDi2EVk5zFj-9JpJQlApjAnHmFpGQEt`
(short form, region 301 pinned). Killed and restarted with the same dir: `cmp` says IDENTICAL. `host.key.json` is 319 bytes, mode `0600`, dir `0700`, fields `Private`, `Public{ServerPublic, ServerDiscoPublic, RegionID: 301}`. With the server stopped:
```
BUNNY_TUNNEL_DATA_DIR=/tmp/bunny-t001-data go test ./internal/tunnel -run SavedAddrLive -v
    tunnel_test.go:468: SavedAddr(/tmp/bunny-t001-data) = tco2FwWCAfk4JLOfZHSt…HmFpGQEt   (== the server's address)
```
`Ephemeral: true` never writes (test asserts the dir stays empty) and yields a new address each Start.

**2. Port 80 only, nothing else.** `TestListenerServesPort80Only` (in-process DERP+STUN on loopback, no network): a real `tailcat.Client` pings, dials 80, `GET /healthz` → `200 {"ok":true}`; `Status().Clients` is 1 during the request and 0 after; dial to **81 rides out its deadline** (filter drop, handler never runs) while 80 keeps working; after `Listener().Close()` port 80 gets a fast RST and `Accept` returns `net.ErrClosed`. `TestProtection1Config` pins the tailcat config: `OnTCPForward == nil`, `AllowProxy == nil`, no allowlist, `ServedTCPPorts == {80,80}`; `TestOnTCPGate` checks ports 0, 1, 22, 79, 81, 443, 8080, 65535 → nil.

**3. Invite.** `bn1.<tc…>.<secret>`; `Decode(Encode(a, s)) == {a, s}` for 2000 random base64url pairs plus a real `ConnBlob`; 33 adversarial fixtures (missing/wrong/case/`bn0`/`bn01`/huge-N prefix, `bn2`/`bn10` say "needs a newer app", 1/2/4 parts, trailing/leading dot, empty parts, non-`tc` address, `+ / =`, inner space, zero-width space, newline in secret, NUL/unicode in address). Whitespace trimmed only at the ends.

**4. wasm bridge, in a real browser over the public relay** (headless Chromium via Playwright, `web/wasm/demo-check.mjs`, page served by `tunneldemo` on `127.0.0.1:19080`, laptop on home Wi-Fi, relay `tc301a.ipn.dev` = nyc):
```
wasm loaded; bunny.wasm.gz is 6182826 bytes
  bridge: handshake attempt 1: ok, 93ms
  bridge: handshake up after 176ms
  bridge: relayed via DERP(nyc)
connected in 183 ms; client identity 283 bytes of key JSON
ping: rtt 77.6 ms via DERP(nyc) direct=false        ← first: includes the WireGuard handshake
ping: rtt 31.2 ms via DERP(nyc) direct=false
ping: rtt 32.9 ms via DERP(nyc) direct=false
GET /healthz: dial 32 ms, first byte 64 ms, total 64 ms, 138 bytes
HTTP/1.1 200 OK / Content-Type: application/json / Content-Length: 11 / Connection: close
{"ok":true}
GET /healthz again: dial 33 ms, first byte 66 ms, total 66 ms → {"ok":true}     ← no new handshake
GET /stream: 20 events, dial 32 ms, first byte 66 ms, total 2087 ms (events arrive every ~100 ms as sent)
```
An earlier run of the same page saw `connect 422 ms` (handshake 330 ms) — relay variance. CLI cross-check from the host, tailcat 0.4.0: `pong in 27.9ms / 28.07ms / 28.97ms via DERP(nyc)`.

**5. tunneldemo** serves `/healthz`, `/stream` (20 flushed SSE events, 100 ms apart), `/status` on the tunnel listener and the `web/` directory on loopback; stdout is exactly the address. `-demo-listen 0.0.0.0:…` is refused in 0.3 s before any relay contact.

### Measured numbers (verbatim, for `docs/MEASURE.md`)

| What | Command | Result |
|---|---|---|
| wasm bundle | `sh web/wasm/build.sh` | `bunny.wasm` 26,912,436 B; `bunny.wasm.gz` 6,182,826 B; build 8.8 s wall with warm cache |
| Relay rtt, browser | demo page `ping()` ×3 (TCP connect through relay) | 77.6 / 31.2 / 32.9 ms via DERP(nyc), direct=false |
| Relay rtt, host CLI | `tailcat ping <addr>` ×3 | 27.9 / 28.07 / 28.97 ms via DERP(nyc) |
| Browser connect | `BunnyTunnel.connect` to first successful handshake | 183 ms (another run: 422 ms) |
| Browser healthz | raw `GET /healthz` over `session.dial(80)` | dial 32 ms, response complete 64 ms, 138 B; repeat 33 / 66 ms |
| Browser SSE | `GET /stream` | first event 66 ms, 20 events in 2087 ms |
| Region picked | `tunnel.Start` auto (netcheck) from this laptop | 301 nyc "New York City" |
| Checks | `go test ./...` | invite 0.27 s · tunnel 1.83 s · web/wasm 1.22 s (warm cache) |

### Verified / not verified

Verified: promises 1–6 as above; `go build ./... && go vet ./... && go test ./...` printed green after rebase; `gofmt -l` clean; `-race` on tunnel 3/3; `GOOS=js GOARCH=wasm go vet` with the pinned tags clean; the wasm compile test passes; `make wasm` works (build.sh idempotent now).

Not verified: a direct path (always `false` in browsers, by design: no UDP); a self-hosted DERP over the real network (the full-form/embedded-relay path is exercised in-process with the local DERP only); reuse of a persisted client identity via `privateKey` in the browser (mirrors tailcat's code; not driven by the demo); relay throttling (none observed); relay reconnect / network change; non-darwin native builds; `go test` from a cold module and build cache (the first js/wasm compile takes ~1–2 min; `-short` skips it).

### Judgment calls (declare loudly)

1. **`ping()` does not use tailcat's ping.** Under js/wasm in v0.4.0 `Client.DiscoPing` never receives its pong (10 s timeout, reproduced twice), and `Client.Ping` returns in ~0.1 ms after the first handshake because it only waits on an already-closed channel. Reporting either would violate "surfaces tell the truth". `rttMs` is now a TCP connect to port 80 through the tunnel (the round trip every request pays; matches the CLI within ~4 ms); `via` is the DERP map's region code (`nyc`) or, for an embedded relay whose code `ConnBlob` strips, its hostname; `direct` is `false`. Cost: each ping opens and closes one tunnel connection to the gateway. Candidate upstream issue for tailcat; if the PM wants disco-fidelity `via`, that is upstream work.
2. **`Status().Clients`** counts distinct clients with an open port-80 connection; a browser between requests counts as 0. tailcat exposes no peer list. A "seen in the last N minutes" window would be ticket 003's usage data anyway.
3. **Address form.** Short form by default (the browser fetches `tailcat.dev/derpmap.json`, CORS fine, and gets the real region code). `DERPMapURL` or `Region` set → the relay (2 nodes) is embedded in the key file itself, so `SavedAddr` and `Addr()` agree offline. `Options.Region` accepts ID / code / name substring / DERP hostnames (tailcat genkey semantics) and applies to a new key only; a saved key keeps its region (tested).
4. **Local DERP in tests** mirrors `cmd/tailcat`'s `runDevDERP` (derpserver + STUN + a one-region map over `httptest`) instead of importing `tstest/integration`; same pattern, lighter test binary.
5. **invite** adds `ErrAddr` (address must be `tc` + base64url) beyond the four listed errors; secret length is not enforced (charset only, as the ticket specifies).
6. **`web/wasm/build_test.go`** compiles the bridge for js/wasm unless `-short`: ~1–2 min on a cold cache in a fresh clone, ~1 s warm. Keeps "green from nothing" honest at the cost of first-run time.
7. **Playwright**, per the PM's update: `npx playwright install chromium` (default cache) and the `playwright` package in scratch `/tmp/bunny-pw`; `demo-check.mjs` takes `PLAYWRIGHT_PATH` so it needs no package.json in `web/wasm`.
8. **Concept budget 3/3:** host key file, invite, session. No new product concepts elsewhere (tunneldemo flags are a hack tool).

Edge cases handled, one line: corrupt/empty/array/`null`/no-private-key key files, `RegionID: -1` (tailcat's "auto" → treated as unpinned and pinned), tampered `Public` block (address derives from `Private`), nil `RemoteAddr`, double `Close`, deliver-after-Close, parked-connection close, >1024 parked conns shed, `CloseWrite` through the wrapper, ports 0/65535, unknown region code lists the codes, non-loopback demo listen refused.

### Accounting (recomputed from the diff)

- Base `f171164`, lane `t001-tunnel-invite-wasm`, code commit `60fc7b9`; patch SHA-256 (`git diff origin/main..60fc7b9`): `c6378f20d4818f4fec365edc33ee629ede3d8a705745fb243942cff2da6576b8`. This report commit changes only this file.
- Go source, tests excluded: **885 / 900** — `internal/tunnel/tunnel.go` 294, `listener.go` 143, `internal/invite/invite.go` 88, `web/wasm/main_js.go` 265, `hack/tunneldemo/main.go` 95.
- Check/build tooling, not counted above — **PM to confirm the bucket**: `web/wasm/demo.html` 22, `demo.js` 82 (the ticket's "manual and automated check for promise 4"), `build.sh` 11, `build-tags.txt` 1 = 116. If these count as source the total is 1001.
- Tests: `tunnel_test.go` 469, `invite_test.go` 131, `build_test.go` 59, `demo-check.mjs` 20 = 679.
- `go.mod`/`go.sum`: tailcat and tailscale.com become direct requirements; 141 sum lines added by `go mod tidy`.
- Tests: 12 top-level passed / 0 failed / 1 skipped (`TestSavedAddrLive`, opt-in via env), 52 including subtests.
- Concepts: 3 of 3.

### Candidates for the PM (not done here)

- Re-price if wanted: disco-fidelity `via`/`direct` in the browser (upstream tailcat work); a recency window for `Clients`; the demo page's bucket.
- Adjacent, not fixed: tailcat v0.4.0 `DiscoPing` under js/wasm gets no pong (upstream issue candidate); tailcat `Client.Ping` is not a repeatable RTT after the first handshake (document or upstream).
- For 004: `connect()` resolves after ~0.2–0.4 s on this relay; per-request dial costs ~32 ms plus one RTT for the response; `onLog` carries the bridge's progress lines ("handshake attempt n", "relayed via DERP(nyc)") for a truthful connecting state.

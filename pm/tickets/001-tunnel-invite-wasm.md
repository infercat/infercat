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

## Report

---
id: 035
title: tailcat.Client.Close() hangs for minutes when the client is relay-only (UDP disabled)
kind: defect
size: 2
status: landed
updated: 2026-09-03
release: demo-1
found_by: 028 (load test)
severity: medium
component: tunnel client (tailcat v0.4.0 / wireguard-go), affects ticket 026 connect command
---

# 033 — a relay-only native client hangs on Close

**Found.** Ticket 028's load instrument (`hack/load`), building browser-like friends with UDP disabled
(`TS_DEBUG_ALWAYS_USE_DERP=true`, tailcat's own knob for "force all peer traffic over DERP"). At
teardown the process would not exit: `main` was parked in `tailcat.Client.Close()` for six minutes and
counting, with thousands of goroutines still live.

**What hangs.** `Client.Close()` → `wgengine.(*userspaceEngine).Close` → wireguard-go
`(*Device).BindClose` → `sync.WaitGroup.Wait`, which never returns because the device's
`RoutineReceiveIncoming` goroutines are still blocked in
`magicsock.(*blockForeverConn).ReadFromUDPAddrPort` on a `sync.Cond.Wait`. When UDP is disabled the
UDP conn is a `blockForeverConn` whose read parks on a condition variable; on Close the receive
goroutines are never woken, so `BindClose`'s WaitGroup never drains and Close blocks indefinitely.

**Repro (deterministic at N≥~12; intermittent below):**
```
# a running host, then N relay-only native tunnel clients that connect, do a little traffic, and Close
TS_DEBUG_ALWAYS_USE_DERP=true \
  go run ./hack/load --host-dir <host> --bin <bin> --n 12 --minutes 2 --relay-only --out /tmp/x
# → the run's work completes, then the process parks at teardown:
#   goroutine 1 ... [sync.WaitGroup.Wait, 6 minutes]:
#   wireguard-go/device.(*Device).BindClose → sync.(*WaitGroup).Wait
#   (thousands of magicsock blockForeverConn.ReadFromUDPAddrPort goroutines, [sync.Cond.Wait, 10 minutes])
```
Captured stacks: any run directory's own `<run>.log` after a SIGQUIT during teardown; the 028 run
`R3-llama-ours-n12` first exhibited it.

**Blast radius.** *Not* the host: the host binary (`bunny-network serve`) is unaffected — during 028 its
goroutines and RSS stayed flat and it drained normally. *Not* the browser: the web friend is the wasm
bridge, a different client that never runs this Go path. The exposed path is a **native** client that is
relay-only — which today means (a) this instrument, and (b) the future `connect` command (ticket 026) on
a network where NAT traversal never yields a direct path and the client stays on DERP for the whole
session: its Ctrl-C / clean shutdown would hang the same way.

**Fix options (for PM sizing).**
1. Bound `Close` in our `internal/tunnel` client wrapper (when 026 builds one): close in a goroutine,
   wait a few seconds, then return and let the process exit — the workaround 028's instrument already
   uses (`hack/load/main.go`, the 15 s bound + `os.Exit`). Cheap; hides rather than fixes.
2. Upstream fix in tailcat/wireguard: `blockForeverConn.Close` must broadcast its cond so the receive
   goroutines unblock and `BindClose` completes. Correct fix, but it lives in the pinned dependency
   (`github.com/tailscale/tailcat v0.4.0`); needs a patch or a version bump, and a test that
   `Client.Close()` returns within a bound with UDP disabled.

**Recommendation.** Take option 1 inside ticket 026 so `connect` never hangs on exit, and open an
upstream issue for option 2. Verify with a test: a relay-only client's `Close` returns within N seconds.

## Log
- 2026-09-03 — filed from 028. Instrument guarded (bounded Close + `os.Exit`); runs unaffected thereafter.
- 2026-09-03 05:19 EDT — ACK. Lane `t035-defects` on main `a69ec2a` (dispatched with 036 as one lane). Premise checked against the pinned sources before editing: `blockForeverConn.Close` in tailscale `v1.103.0-pre` **does** broadcast its cond (`wgengine/magicsock/blockforever_conn.go:43-52`), so "never woken" is not the mechanism. The chain is one step earlier: `bindSocket`'s `TS_DEBUG_ALWAYS_USE_DERP` branch (`magicsock.go:3738-3741`, and the `GOOS=js` branch above it) swaps a fresh `blockForeverConn` into the `RebindingUDPConn` **without** the `ruc.closeLocked()` the normal path does at `:3760`; a receive goroutine already parked in the previous conn's `cond.Wait` (`readFromWithInitPconn` cannot observe a swap until its read returns) is never closed, so `Conn.Close` closing only the *current* pconn leaves it parked, and wireguard-go's `closeBindLocked` → `device.net.stopping.Wait()` (`device/device.go:747-758`) never drains — the stack 028 captured. Every rebind (link change, `Rebind`) grows the pile, which is why the hang is deterministic only at higher N and after a while. Fix as dispatched: a bounded `Close` in `internal/tunnel` (client `Session` and host `Server`, same helper), `connect` and `serve` log "tunnel close timed out; continuing". Own hosts under `…/tmp/bn035-data` on 6870–6879, connect on 11437; the founder's host on 9091 untouched.
- 2026-09-03 05:24 EDT — Fix, in `internal/tunnel` so both sides get it: `closeWithin(d, fn)` runs a close in a goroutine and waits at most `closeTimeout` (3 s; a variable so a test can hurry it); `Session.Close` and `Server.Close` use it; past the bound they answer `ErrCloseTimeout` — the text is the ticket's line, "tunnel close timed out; continuing" — and the close goes on in the background. `connect`'s deferred close and `serve`'s `tun.Close()` print the error when there is one; `Redial` prints it under `--verbose` and dials on. Tests: `TestSessionCloseIsBounded` — a `Session` whose close parks forever answers the error at the bound (302 ms with the bound hurried to 300), a second `Close` answers at once with the same; then a live relay-only client (`envknob.SetenvForTest("TS_DEBUG_ALWAYS_USE_DERP")` after the host is up, so only the client's bind sees it): netcheck `udp=false`, path `{Direct:false Via:1}`, three GETs through it, `Close` within the bound — and `TestConnectCommandBannerAndRefusals` now has the fake session's close park at Ctrl-C and asserts the line on stderr with exit 0.
- 2026-09-03 05:30 EDT — Live, own host on 6870 (`…/bn035-data/a`, llama.cpp), `TS_DEBUG_ALWAYS_USE_DERP=true bunny-network connect <invite> --listen 127.0.0.1:11437`: banner `path      relayed via New York City · 29 ms` (UDP off, so relayed on the same laptop where 026 went direct), a chat through it `142ms ok`, SIGINT → `shutting down` → process gone **0.03 s** later. The hang itself did not reproduce at N=1 — as 028 saw (intermittent below N≈12) and as the mechanism predicts (a rebind has to strand a receive goroutine first) — so the bound's own proof is the parked-close fixture; the live run proves the path and that a clean close is not slowed. Host, connect, both stopped.
- 2026-09-03 05:33 EDT — Freeze: rebased on `origin/main` (still `a69ec2a`); checks printed in the report.

## Report

**Core — `connect` can no longer hang on exit.** Every shutdown path of the client session (Ctrl-C's deferred close, a redial after the host went away, a dial that failed) goes through one bounded `Close` in `internal/tunnel`, and the host's `Server.Close` takes the same bound:

```
$ go test -run TestSessionCloseIsBounded -v ./internal/tunnel/
    client_test.go:138: parked close returned "tunnel close timed out; continuing" after 302ms
    client_test.go:177: relay-only path: {Direct:false Via:1 RTT:327.625µs} (<nil>)
    client_test.go:182: relay-only Close: <nil> after 0s (bound 3s)
--- PASS: TestSessionCloseIsBounded (0.33s)

$ TS_DEBUG_ALWAYS_USE_DERP=true bunny-network connect bn1.<invite> --listen 127.0.0.1:11437 --log-requests
host      t035-llama  ·  gemma-4-E2B-it-Q4_K_M.gguf
path      relayed via New York City · 29 ms
local     http://127.0.0.1:11437
05:30:41  t035-llama  chat  142ms  ok
                                          ← SIGINT
shutting down                             ← exited 0.03 s later
```

**The upstream-issue candidate (not patched; it is tailscale's magicsock, which tailcat pins).** The ticket's "the receive goroutines are never woken" is right in effect and off by one step in cause: `blockForeverConn.Close` does broadcast its cond (`wgengine/magicsock/blockforever_conn.go:43-52`). What never happens is that `Close`. In `(*Conn).bindSocket` (`magicsock.go:3738-3741`; the `GOOS=js` branch just above is the same shape) the `TS_DEBUG_ALWAYS_USE_DERP` path does `ruc.setConnLocked(newBlockForeverConn(), …)` **without** the `ruc.closeLocked()` the normal path does before every re-listen (`:3760`). A wireguard receive goroutine parked in the *previous* blockForeverConn's `cond.Wait` cannot see the swap (`RebindingUDPConn.readFromWithInitPconn` only re-reads the pointer after its read returns), and on `Conn.Close` only the *current* pconn is closed, so that goroutine stays parked and wireguard-go's `closeBindLocked` → `device.net.stopping.Wait()` (`device/device.go:747-758`) never drains. Every rebind (link change, `Rebind`) strands one more, which is why 028 saw thousands of parked readers and a hang that is deterministic only at higher N and after a while — and why N=1 with no rebind closes cleanly (this lane, twice). The one-line fix upstream is a `closeLocked()` before the swap in those two branches (or a `blockForeverConn` read that observes the swap); a test is "a client with UDP disabled, one `Rebind()`, then `Close()` returns". Filing is the PM's call; I have not opened anything.

**Edges seen.** Second `Close` returns at once with the first answer (`sync.Once`); `Dial`'s handshake-failed path closes through the same bound; `Redial` never waits on the old session's close beyond the bound; `Server.Close` bounded too, so `serve`'s exit cannot inherit the same class; the load instrument (`hack/load`) builds `tailcat.Client` directly and keeps its own 15 s guard + `os.Exit` — it does not go through `internal/tunnel` and is untouched.

**Verification.** `go build ./... && go vet ./... && go test ./...` on the rebased lane (`a69ec2a`, origin/main unmoved): build 0, vet 0, every package ok — **161 passed / 0 failed / 2 skipped** top-level (the two skips are the pre-existing opt-in live tests `TestLiveLlamaCPP`, `TestSavedAddrLive`), **109 subtests passed / 0 failed**. `go test -race -count=1 ./internal/gateway/` ok (18.9 s). `gofmt -l` clean on everything I touched (`hack/load/main.go` is unformatted at base; not mine). Manual: the live `connect` run above; own host and `connect` stopped after.

**Accounting** (raw added / deleted from `git diff a69ec2a..HEAD`, this ticket's files):

| Bucket | Budget | Measured |
|---|---|---|
| Go source: `internal/tunnel/client.go` +42/−11, `internal/tunnel/tunnel.go` +3/−2, `cmd/bunny-network/connect.go` +7/−1, `cmd/bunny-network/serve.go` +3/−1 | ≤150 | **+55 / −15 = 70 raw** (15 of the added lines are comment) |
| Go tests: `internal/tunnel/client_test.go` +75, `cmd/bunny-network/connect_test.go` +13/−8 | — | +88 / −8 |
| Concepts | 0 | **0** — no flag, code, config key, state file or dependency; `ErrCloseTimeout` and `closeTimeout` are package symbols (`envknob` already a dependency via tailscale) |

**Declared.** Live actions: own host started on 6870 with data dir `…/tmp/bn035-data/a` (key `friend` minted; invite in `a/invite.json`, mode 0600) and stopped; `connect` on 11437 started with the env knob and stopped by SIGINT. The founder's host and both llama-servers were not touched (requests only to 18080). Intended, not a bug: a `Close` that timed out returns `ErrCloseTimeout` while tailcat keeps closing in the background — the process is expected to exit right after; `Redial`'s line is only printed under `--verbose`, the Ctrl-C line always.

**Adjacent, not fixed.** (1) `hack/load` could hold `tunnel.Session` instead of its own `tailcat.Client` and drop its 15 s guard and `os.Exit` (~20 lines). (2) `hack/load/main.go` is not gofmt-clean at base.

**Freeze.** Base `a69ec2a` (`origin/main` at freeze, unmoved since dispatch) · lane `t035-defects`, code commit `e52f563` · patch SHA-256 (`git diff a69ec2a..HEAD -- internal/tunnel cmd/bunny-network/connect.go cmd/bunny-network/serve.go cmd/bunny-network/connect_test.go | shasum -a 256`): `128c5fe3049d5adf000ab3e70344ce8f366172205850b7d2e98b9d4d7fa756ef` · source 70 raw of ≤150 · concepts 0 of 0 · contests 0 · bought beyond the ticket: the host's `Server.Close` bound (the PM's "so both `connect` and the host benefit"), 3 source lines.

## Ruling (PM, 2026-09-03 07:00)

**Landed.** Bounded `Close` (3 s) in `internal/tunnel` for both Session and Server; `connect` exits in 0.03 s relay-only. Root cause in magicsock (rebind without closing the old conn under ALWAYS_USE_DERP) recorded as an upstream-issue candidate — file it against tailscale/tailscale after launch.

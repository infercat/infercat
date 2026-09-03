---
id: 035
title: tailcat.Client.Close() hangs for minutes when the client is relay-only (UDP disabled)
kind: defect
size: 2
status: draft
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

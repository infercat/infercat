---
id: 011
title: Engine state (Unknown kind, Health) and the three-method Engine seam (DESIGN §3)
kind: sensitive
size: 2
status: landed
updated: 2026-09-02
release: demo-1
---

# 011 — Engine state and seam

## Binding

**Why.** `docs/DESIGN.md` §3: the engine was modelled as a value read once; 005 patched it with a
`sniffed` flag and `defaultSlots`, and 006/010 read slots through plumbing. The engine is a state
(`Unknown → identified`, `Health{OK, Since, Err}`) that every consumer reads at decision time, behind a
seam so small the engine's URL cannot leak into the gateway.

**Promises.** Implement §3.2–3.5 as written: `Kind == Unknown` replaces `sniffed` and `defaultSlots`;
`Health` replaces `Healthy`; `Detect` returns the first candidate that reaches OK; the `Engine`
interface (`Info`, `CountTokens`, `Do`) replaces `BaseURL`/`Transport` in the gateway's imports; the
redirect guard, bearer, first-byte timeout and `probeTimeout` live in the one `http.Client` the engine
owns; `countTimeout` deleted; banner/`status`/`/me` print "not identified yet / NOT ANSWERING for Ns"
and `kind:"unknown"`. Evidence: E1–E5 (E4 is a compile-time check: the gateway imports only `Engine`).

**Size 2** (≤400 source lines net). Concept budget 0 (+1 kind constant, −1 flag, −2 seam methods).
**Sensitive** (Protection 1 gains a structural form).

**Scope contract.** `internal/upstream/**` (including `upstream.go` — the PM releases the seam file to
this ticket), `internal/gateway/**` for the import change only, `cmd/bunny-network/serve.go`/`status.go`
for the banner/status words. Lands after 010 (same engineer, same worktree, rebased).

## Log

- 2026-09-02 12:03 ACK (PM's message: start 011 on the rebased 010 without waiting). Base 19e3598 (origin/main with 009) + 010's commits on the same lane. Read DESIGN §3, this ticket, internal/upstream/** and every seam consumer (gateway, cmd, admin, wasm, hack). No contest. One item outside the scope contract, declared up front: `status` cannot print "NOT ANSWERING for Ns" without the time, so `admin.Upstream` gains one field (`since`, when health last changed) — one line in internal/admin, filled by `buildStatus`.
- 2026-09-02 14:44 FROZEN on base 3c1efb2 (010 landed at 56bc3c7; a WIP commit was made mid-slice when the session was cut by a rate limit, then folded into the one code commit). Code commit 7c7edd4. Patch SHA-256 in the freeze block.

## Report

Engineer Claude Fable 5.1, 2026-09-02. Lane `t010-gateway-settle` (same worktree as 010, as briefed), rebased on
`origin/main` `3c1efb2` (010 landed at `56bc3c7`; the 014/handoff commit touched none of these files). Code commit
`7c7edd4`; this report is a docs-only commit on top. Not merged.

### The core, shown working

1. **The engine is a state, moved by one probe.** `upstream.go`: `Unknown` kind, `Health{OK, Since, Err}`, `Info` as
   §3.2 gives it. `kinds.go` `Refresh` is the one `probe(ctx)`: every transition in the §3.2 table goes through it and
   nothing else moves the state. `sniffed` and `defaultSlots` are gone. `TestE1UnknownEngineHasNothingButOneSlot`:
   `Open` against a dead port, then `SetSlots(7)` → `Kind unknown, Health{OK:false, Err:"Get …/v1/models: … connection
   refused"}, Slots 1, ModelContext 0, Models []`. `TestE2…`: a healthy llama.cpp goes away → `OK false`, `Since`
   moved, `Err` set, **kind/slots/models/context kept**. `TestE3…`: down at `Open` (Unknown), a second failed probe
   keeps `Since`, the engine comes up → `llama.cpp, OK since now, 4 slots, context 8192, 1 model`; the queue follows
   with no push (010's `TestQueueFollowsEngineSlots`; `cmd` `TestServeRoutesTunnelLogAndFollowsSlots` waits on
   `Info().Slots` alone). `Detect` returns the first candidate whose probe reaches OK.
2. **The seam is three methods.** `Engine{Info, CountTokens, Do}` is the gateway's whole view (`Gateway.up
   upstream.Engine`, `New(…, up upstream.Engine, …)`); `Upstream = Engine + Refresh` is the host's. `Do` owns the
   bearer, the redirect refusal, the first-byte deadline (`ResponseHeaderTimeout = FirstByteTimeout`, 120 s) and the
   probe deadline on GETs (3 s, ended when the caller closes the body). Deleted from the gateway: `upstreamURL`,
   `doUpstream`, `countTimeout`, the 010 first-byte timer and its knob. **E4 at compile time:** `BaseURL`/`Transport`
   no longer exist on anything the gateway can name; `grep -rn 'BaseURL\|Transport()\|net/url\|upstreamURL\|doUpstream'
   internal/gateway/*.go` (tests excluded) prints nothing; the test fake declares `var _ upstream.Engine =
   (*fakeUpstream)(nil)` and has exactly `Info`, `CountTokens`, `Do`. **E5:** `TestE5DoNeverFollowsRedirectsNorLeaksHeaders`
   — a 302 comes back as the 302, `/landed` hit 0 times, the engine saw only `Bearer sk-host`, and a GET's body is the
   `cancelOnClose` that ends the probe deadline.
3. **Surfaces tell the truth.** Banner (`TestDownUpstreamNamesTheWayOut`): `upstream  (not identified yet)
   http://127.0.0.1:NNNNN  NOT ANSWERING for 0s` and never "openai-compatible" for an engine nobody met; `status`
   (`TestStatusWordsForTheEngineState`): `upstream  (not identified yet)  http://127.0.0.1:1  NOT ANSWERING for 12s`,
   `upstream  llama.cpp  …  healthy`; `/me` `host.upstream.kind` is `"unknown"` with `healthy:false`; the refresh loop
   logs "upstream is back: URL (kind)".
4. **Live, through the new seam** (shared llama-server `127.0.0.1:18080`, requests only): stream 200, TTFT 81 ms,
   **176.9 tok/s**, usage `{27, 120}`; `n_predict 40` through the gateway → completion_tokens **8**; 422 and 429 with
   real counts (`TestLiveLlamaCPP`, whose `liveLlama` now implements `Do`).

### Verification (printed, on base `3c1efb2`)

- `go build ./...` exit 0 · `go vet ./...` exit 0 · `go test ./...`: **11 packages ok, 0 failed**. `gofmt -l` empty.
- `go test -race -count=1 ./internal/gateway/`: **48 passed / 0 failed / 1 skipped** (opt-in live), 59 subtests, TestMain
  gate `all 14 error codes exercised; 28 log lines captured, secret absent`.
  `go test -race -count=1 ./internal/upstream/`: **14 passed / 0 failed / 0 skipped**, 5 subtests. Both stable ×2.
- `BN_LIVE_UPSTREAM=… go test -race -run TestLiveLlamaCPP -v`: **1 passed** (numbers in core 4).

### Adaptations to existing tests (each named)

- `internal/upstream/upstream_test.go`: `TestOpenSniffsEveryKind`, `TestDetectPassesTheAPIKeyToProbes` (`Health.OK`,
  plus `Err ""`/`Since`/`ProbedAt` set) · `TestRefreshReSniffsAnEngineThatWasDownAtOpen` → **`TestE3…`** (Unknown, not
  Generic; `Since` kept across a failed probe, moved on success) · `TestUnreachableUpstreamOpensUnhealthy` → **`TestE1…`**
  (+ the override while Unknown) · `TestRefreshKeepsTheLastGoodInfoWhenTheEngineGoesAway` → **`TestE2…`** (+ `Err`,
  `Since`, kind and slots kept) · `TestTransportAddsTheUpstreamKey` → `TestDoAddsTheUpstreamKey` · `TestBaseURLIsACopy`
  deleted (no `BaseURL`; the address staying private is E5) and `TestVLLMSlotsDefaultToTwoOnceIdentified` added.
- `internal/gateway`: `fakes_test.go` (the fake is an `Engine`: `Do` mirroring the real one, `firstByte(d)` replaces
  the gateway knob, `setBase`) · `live_test.go` (`liveLlama.Do`) · `TestUpstreamFailures`, `TestI1` rows "first
  byte"/"unhealthy"/"unreachable", `TestI6` "EngineErr: first byte", `TestI8` "no headers" and "slow but live" (knob and
  the message "did not answer in time") · `TestUpstreamRedirectNotFollowed` unchanged: through the seam it now pins the
  gateway's handling of a 3xx (502, nothing charged); the not-following proof is E5 in `upstream`.
- `cmd/bunny-network/main_test.go`: `TestDownUpstreamNamesTheWayOut` (+ the banner words); `TestStatusWordsForTheEngineState`
  (new). Test-only edits to a file outside the scope contract.

### Contract lines that changed (docs/ARCHITECTURE.md is the PM's; not edited)

- **§Upstream:** the gateway's seam is `Engine{Info, CountTokens, Do}`; the host's is `Upstream = Engine + Refresh`;
  `BaseURL`/`Transport` gone. Kinds gain `unknown`; `Info.Healthy` → `Info.Health{ok, since, err}` + `probed_at`; slots
  are 1 while unknown; vLLM's default 2 is applied at identification. "Engine down at start" is no longer Generic.
- **Deadlines (§1.6 table):** engine first byte = the engine transport's `ResponseHeaderTimeout` (120 s); auxiliary calls
  (tokenize, model list) 3 s, engine-owned — the gateway's 10 s `countTimeout` is gone.
- **§Admin API:** `upstream.since` (when healthy last changed) added; `upstream.kind` may be `"unknown"`.
  **`/me`:** `host.upstream.kind` may be `"unknown"`. **Banner / `status` words:** "(not identified yet)",
  "NOT ANSWERING for Ns".

### Judgment calls (declared)

1. **`internal/admin/admin.go` gained one field, `since`** — outside the scope contract, announced in the ACK: `status`
   cannot print "for Ns" from a bool. If refused, `status` says "NOT ANSWERING" without the duration (one line).
2. **The friend's message for an engine timeout has no number** ("the host's engine did not answer in time"): the engine
   owns the bound now; the gateway classifies a `net.Error` timeout. The host's log carries the error text.
3. **GETs through `Do` are bounded by `ProbeTimeout`** via a context whose cancel runs on body `Close` (`cancelOnClose`):
   that is how "Do owns timeouts" reaches the model list. The list is bounded at 3 s (was 10 s).
4. **vLLM's default of 2 slots is the override's default at identification** (§3.2's words), so an Unknown vLLM has 1.
5. **`Health.Err` is the probe's error text**, URL included — host surfaces only; `/me` exposes `healthy` alone.
6. **`refreshLoop` keeps its slot-change line** (010's judgment) and now names the kind when the engine comes back.
7. **E4 is compile-time by construction**, not an AST test: nothing the gateway can name has an address or a transport.
8. `candidates[].kind` stays as documentation (the contract's table); detection sniffs, never assumes.

### Not verified

- vLLM/Ollama/LM Studio live (the fakes only); the 120 s first-byte bound on a real slow engine (the live one answers in
  81 ms). Through 001's real tunnel listener: httptest only.

### Candidates for the PM (not done)

- `intOr` in `config.go` is still dead. `web/src/api.ts` types `upstream.kind` as `string` — "unknown" renders as-is;
  a friend-facing word for it is 014's lane.

### Freeze

- **Base:** `3c1efb2` (`origin/main` at freeze). **Lane:** `t010-gateway-settle`, commits after 010's landing. Code
  commit `7c7edd4`; this report is a docs-only commit on top.
- **Patch SHA-256** (`git diff 3c1efb2..7c7edd4 -- internal cmd | shasum -a 256`):
  `b5f09c12cb0623818dcf0d608c3106af60faacaabdefe142efc40cd46a5b9905`
- **Source diff** (tests excluded): +238 / −200, **net +38** — `internal/upstream` `client.go` +79/−50, `kinds.go`
  +45/−30, `upstream.go` +40/−20; `internal/gateway` `gateway.go` +17/−17, `proxy.go` +18/−35, `request.go` +6/−30;
  `cmd/bunny-network` `serve.go` +10/−10, `status.go` +17/−3; `internal/admin/admin.go` +6/−5. Non-blank-non-comment:
  upstream 377 → 417, gateway 1403 → 1363. Size 2 ceiling ≤400: 10 % net, 438 changed.
- **Tests:** +208 / −57 — `upstream_test.go` +97/−31 (E1, E2, E3, E5, `DoAdds…`, vLLM default), `fakes_test.go`
  +55/−8, `live_test.go` +22/−6, `invariants_test.go` +9/−9, `gateway_test.go` +3/−3, `main_test.go` +22/−0.
  Upstream 14 tests (was 13); gateway 48 unchanged.
- **Concepts:** budget 0; **+1** kind constant (`unknown`), **−1** hidden flag (`sniffed`), **−2** seam methods
  (`BaseURL`, `Transport`); `Health` replaces `Healthy` (0); `Engine` is the seam the ticket buys. `ProbeTimeout`,
  `FirstByteTimeout` are exported constants, not concepts. Net −2. No new flags, config keys, error codes, routes,
  files, or dependencies (`go.mod` untouched).
- **Files outside the scope contract:** `internal/admin/admin.go` (judgment 1), `cmd/bunny-network/main_test.go`
  (tests only); this ticket file.
- **Production-touching actions:** none. The shared llama-server received the opt-in live test's requests only; no
  restarts, no secrets, no dotenvx, no max-ws.lab.

## Ruling (PM, 2026-09-02 15:05)

**Landed** on main (ff of `d1bd630`); build/vet/test green, `-race` on gateway and upstream green, three
cross-compiles OK, printed; E4 confirmed by grep (the gateway names only `Engine`, `Info`, `Kind`).
The one-field `internal/admin` addition (`since`) is accepted as declared. `docs/ARCHITECTURE.md` v1
follows from the PM.

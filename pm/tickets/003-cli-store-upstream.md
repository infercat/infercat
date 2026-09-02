---
id: 003
title: CLI, key file store, upstream adapters, usage recorder, admin status
kind: sensitive
size: 8
status: landed
updated: 2026-09-02
release: demo-1
---

# 003 — CLI, key store, upstream, usage recorder, admin status

## Binding

**Why.** This is the host's whole experience: one command to serve, one to mint a friend, one to see
what is happening. It wires 001's tunnel and 002's gateway into a binary a stranger on social media can
run in two minutes on Mac, Linux, or Windows.

**Promises.**
1. `cmd/bunny-network` builds on darwin/linux/windows (`GOOS=… go build` for all three succeeds). Name
   comes from `internal/product`. Subcommands: `serve`, `keys add|list|pause|resume|revoke|rotate|limits`,
   `status`, `usage`, `version`. `--data-dir` global flag (default per contract). Help text is a product
   surface: short, correct, example-led.
2. `serve` flags: `--upstream URL`, `--upstream-key`, `--slots N`, `--queue-timeout`, `--request-timeout`,
   `--max-body`, `--log-prompts`, `--dev-listen ADDR`, `--ephemeral`, `--derpmap-url`, `--region`,
   `--name` (host display name for `/me`). Flags given are persisted to `config.json`; a later `serve`
   with no flags reuses them (flags win over file). Startup prints, in order: product + version; upstream
   kind, URL, model(s), context, slots; the tunnel address; the relay region; and the sentence
   `Mint a friend: bunny-network keys add <name>` if no keys exist, else `N keys active`. Ctrl-C shuts down
   cleanly (drain up to 10 s). Upstream down at start is a warning, not a failure; the gateway answers
   503 `upstream_down` until `Refresh` succeeds (poll every 10 s).
3. `internal/upstream`: `Detect(ctx) (Upstream, error)` in the contract's order; `Open(ctx, url, apiKey)`
   for an explicit URL with kind sniffing (llama.cpp `/props`; vLLM `/version` or `owned_by:"vllm"`;
   Ollama `/api/tags`; LM Studio by `/v1/models` shape; else Generic). `Refresh` fills `Info` (models,
   context, slots, healthy). `CountTokens` exact via `/tokenize` for llama.cpp and vLLM (vLLM's takes
   `{"model":…,"prompt":…}`; llama.cpp's takes `{"content":…}`), estimate otherwise. `Transport` adds the
   upstream bearer if set. All probes have 3 s timeouts.
4. `internal/keys.FileStore`: implements `keys.Admin` over `keys.json` per contract. Atomic writes
   (temp + rename). `Lookup` hashes the presented secret with `keys.HashSecret` and compares in constant
   time. Hot reload: re-read when mtime changes, checked at most once per second. IDs `k_` + 6 hex.
   `Add` returns the plaintext secret once. `Rotate` keeps id and limits. Concurrent-safe.
5. `keys add NAME [--rpm --tpm --max-concurrent --max-output-tokens --max-context --daily-tokens --models a,b]`
   prints: the invite string (`invite.Encode(addr, secret)`), a QR code of it in the terminal, and a
   one-line reminder that the secret is shown once. `addr` comes from the running daemon's admin socket
   if present, else `tunnel.SavedAddr(dataDir)`; if neither exists, refuse with "run `serve` once first".
   `keys list` is a table: id, name, status, rpm/tpm/daily, created, last seen (from usage). `keys
   rotate` prints a new invite. `keys limits ID --rpm …` edits limits.
6. `internal/usage.FileRecorder`: appends JSONL to `usage.jsonl` via a buffered channel (never blocks
   the request path beyond a bounded channel; drops with a counter and a warning if full). `usage
   [--key ID] [--since 24h|7d]` aggregates the file: requests, errors by code, prompt/completion tokens,
   median and p95 ttft and total, per key. Runs without the daemon.
7. `internal/admin`: unix-socket HTTP server at `admin.sock` with `GET /status` per contract, fed by the
   tunnel `Status()`, `upstream.Info()`, and the gateway's `usage.Snapshot`. Socket mode 0600. `status`
   subcommand renders it as a readable block; exits non-zero with a clear message if no daemon. On
   Windows, use a loopback TCP port stored in `admin.port` with a random token in `admin.token` (0600);
   document it.
8. **Evidence.** `go test` at "the promises" for FileStore (add/lookup/hash/rotate/hot-reload/atomicity),
   upstream adapters against `httptest` fakes of all four kinds (detection order, tokenize shapes, info),
   usage aggregation, invite printing. A manual run against the local llama-server at
   `http://127.0.0.1:18080` and, via `ssh -N -L 8010:127.0.0.1:8010 max@max-ws.lab`, against vLLM at
   `http://127.0.0.1:8010` (read-only): paste `serve` startup output for both, `keys add` output (redact
   the secret), and `status`. Cross-compile proof: print the three `go build` results.

**Size 5** (≤2000 source lines). Concept budget 6: config file, key file, usage file, admin socket,
detection order, upstream kinds.
**Sensitive** (Protection 2: secrets hashed, shown once; Protection 1: admin never on TCP except the
documented Windows loopback fallback).

**Scope contract.** `cmd/bunny-network/**`, `internal/keys/store*.go` and `internal/keys/*_test.go`
(never `keys.go`/`hash.go`), `internal/usage/recorder*.go`, `internal/usage/aggregate*.go`, tests,
`internal/upstream/**` except `upstream.go`, `internal/admin/**`, `go.mod`/`go.sum`. You depend on
`internal/tunnel` (001) and `internal/gateway` (002) which land in parallel: code against the
signatures in `docs/ARCHITECTURE.md` and ticket 002's promise 1; until they land, keep a thin local
adapter behind a build tag or interface so your package tests do not import them. When they land the
PM will ask you (or a follow-up) to wire and re-verify. If a signature you need is missing, contest
with the exact symbol.

**Non-goals.** No gateway logic. No tunnel logic. No model downloads. No web serving. No daemonizing /
launchd / systemd units (a doc line "run it under your supervisor of choice" is enough).

**Handoff.** Branch `t003-cli-store-upstream`, rebased on `main`, checks printed green. Report under
`## Report`. Push. Do not merge.

## Background (hypotheses)

- QR in terminal: `github.com/skip2/go-qrcode` or `github.com/mdp/qrterminal` are small; either is fine.
  CLI framework: stdlib `flag` with a tiny subcommand dispatcher, or `peterbourgon/ff/v4` (tailcat uses
  it). Keep the dependency count low.
- Local llama-server: `/props` → `total_slots`, `default_generation_settings.n_ctx`; `/v1/models`
  returns the GGUF filename as id. vLLM: `/v1/models[].max_model_len`; `/tokenize` exists in 0.25.
- Ollama `/api/tags` lists models; its `/v1/chat/completions` is OpenAI-compatible; context unknown →
  0. LM Studio `/v1/models` OpenAI-shaped; no tokenize.

## Log

- 2026-09-02T00:00Z ACK. Base `f171164` (branch `t003-cli-store-upstream`, worktree). Read BELIEFS, ARCHITECTURE, tickets 001/002/003, seam packages.
- 2026-09-02T00:05Z Probed live upstreams. llama.cpp `127.0.0.1:18080`: `/props` -> `total_slots:2`, `default_generation_settings.n_ctx:4096` (PM Background said 8192 -- **4096 is what the running server reports**), `/v1/models[].owned_by:"llamacpp"`, `/tokenize` `{"content":...}` -> `{"tokens":[...]}`, **no `/version` (404)**. vLLM via ssh tunnel `127.0.0.1:8010`: `/version` -> `{"version":"0.25.0"}`, `/v1/models[].owned_by:"vllm"`, `max_model_len:8192`, `/tokenize` `{"model","prompt"}` -> `{"count":7,"tokens":[...]}`. Detection signatures confirmed.
- 2026-09-02T00:10Z Added `github.com/skip2/go-qrcode` (only new dependency; terminal rendering hand-rolled as half-blocks).

- 2026-09-02T01:10Z Seam packages done (keys.FileStore, usage recorder + aggregates, upstream detection/adapters, admin socket + client). Build + vet green.
- 2026-09-02T01:40Z CLI done. Found and fixed a real defect: stdlib `flag` stops parsing at the first positional, so the ticket's own `keys add alice --rpm 60` silently ignored the flags. Added `reorder()` (positionals moved behind the flags, with a `--` terminator) plus a test.
- 2026-09-02T02:05Z Found a second real defect: a data dir whose `admin.sock` path exceeds the kernel's `sun_path` limit (104 darwin / 108 linux) failed with `bind: invalid argument`. Now refused with a message that says what is actually wrong.
- 2026-09-02T02:30Z 56 tests green, `-race` green. Live runs against llama.cpp `:18080` and vLLM `:8010` captured.
- 2026-09-02T02:45Z Measured the diff: 2790 source lines against a 2000 budget. Ran a dedup pass (-34 lines), then filed the Contest below rather than cut promised surface.
- 2026-09-02T03:00Z Rebased on `origin/main` (still `f171164`; 001 and 002 have not landed). Full check and three cross-compiles printed. Frozen.

## Contest — the size budget, measured

**Not a contest against a seam or a signature.** Everything in `docs/ARCHITECTURE.md` fitted; no
symbol was missing. This contests one number: **size 5 (≤2000 source lines) is not enough for the
eight promises as written.** Raising it before editing was not possible — the overrun is only
visible once there is a coherent cut — so per the engineer skill ("if your first coherent cut
disagrees with the ticket's numbers, freeze and contest with the measured map") here is the map.

Measured from the diff, non-test `.go` only:

| Promise | Files | Raw lines |
|---|---|---|
| 1. CLI shell: dispatch, global flag, help, version | `main.go` | 277 |
| 2. `serve` + config persistence | `serve.go`, `config.go` | 410 |
| 3. `internal/upstream` detection + 5 kinds + tokenize | `client.go`, `kinds.go` | 397 |
| 4. `keys.FileStore` | `store.go` | 348 |
| 5. `keys` subcommands + invite + QR | `keys.go`, `qr.go` | 379 |
| 6. usage recorder + aggregates + `usage` | `recorder.go`, `aggregate.go`, `usagecmd.go` | 474 |
| 7. `internal/admin` + windows fallback | `admin.go`, `sock_unix.go`, `sock_windows.go` | 261 |
| — 001/002 wiring seam | `wire.go`, `wire_stub.go` | 152 |
| **Total** | | **2790** |

Non-blank, non-comment: **2366**. Tests (excluded from the budget): 1494.

Three of those numbers are load-bearing and I did not want to cut them without a ruling:
- **128 raw lines are the help constants.** Promise 1 makes help "a product surface: short,
  correct, example-led". Trimming it to `flag.PrintDefaults()` would save ~110 lines and cost the
  surface the ticket asked for.
- **59 lines are the Windows admin fallback** that promise 7 requires and that nothing on this
  machine can exercise.
- **89 lines are `wire_stub.go`**, which is scaffolding: it is deleted the moment 001 and 002 land,
  taking the total to 2701.

I already did the obvious dedup (one `e.open` preamble across the `keys` subcommands, one
`dataDirUsage` string): −34 lines. Getting under 2000 from here means dropping promised surface,
not tightening code. **Ask: re-price 003 to 8 (negotiated), or rule which promise to cut.** I did
not cut anything on my own authority; the branch delivers all eight promises.

Concept budget was **met exactly: 6 of 6** — config file, key file, usage file, admin socket,
detection order, upstream kinds. No new CLI verb, flag, error code, config key, or state file
beyond the ticket's own list. (The Windows `admin.port` / `admin.token` pair is the admin socket
concept on a platform without one, as promise 7 specifies.)

## Report

### The core, working

**1. One binary, three platforms, from a fresh tree.**

```
GOOS=darwin  GOARCH=amd64 go build ./cmd/bunny-network: OK  (11431344 bytes)
GOOS=linux   GOARCH=amd64 go build ./cmd/bunny-network: OK  (11187233 bytes)
GOOS=windows GOARCH=amd64 go build ./cmd/bunny-network: OK  (11326464 bytes)
GOOS=darwin|linux|windows GOARCH=amd64 go build ./...:   OK (all three)
```

**2. `serve` against the two real engines.** Both runs are the real CLI against the real servers;
only the tunnel and the gateway are stubbed (see *Wiring status*).

llama.cpp b9553, `http://127.0.0.1:18080`:
```
Bunny Network 0.0.1-dev
upstream  llama.cpp  http://127.0.0.1:18080
          gemma-4-E2B-it-Q4_K_M.gguf  context 4096  slots 2
tunnel    tc-no-tunnel-in-this-build
relay     none

Mint a friend: bunny-network keys add <name>
```

vLLM 0.25.0 over `ssh -N -L 8010:127.0.0.1:8010 max@max-ws.lab` (read-only; nothing was changed
there):
```
Bunny Network 0.0.1-dev
upstream  vllm  http://127.0.0.1:8010
          entropy-v2-gemma4-12b-w4a16-group128  context 8192  slots 2
tunnel    tc-no-tunnel-in-this-build
relay     none

Mint a friend: bunny-network keys add <name>
```

Both kinds were **sniffed, not assumed from the port**: llama.cpp by `/props`, vLLM by `/version`.
Context and slots come from the engine (`default_generation_settings.n_ctx` = 4096 and
`total_slots` = 2 for llama.cpp; `max_model_len` = 8192 for vLLM).

**3. `keys add` prints an invite once, with its QR** (secret redacted here; the terminal prints a
scannable black-on-white half-block QR, stripped from this paste):
```
key k_65f33b  alice
limits: 30 rpm · 20000 tpm · 1 concurrent · 2048 max output · the upstream's context · 200000 tokens/day · all models

Invite for alice — it is shown once and stored only as a hash:

  bn1.tc-no-tunnel-in-this-build.<SECRET-REDACTED>

Lost it? `bunny-network keys rotate k_65f33b` issues a new one and retires this.
```
`keys.json` holds `"secret_hash": "sha256:…"` and never the secret; a test asserts the printed
secret is absent from the file and from `keys list`.

**4. `status` reads the running host over the unix socket:**
```
Bunny Network 0.0.1-dev — up 1s
upstream  llama.cpp  http://127.0.0.1:18080  healthy  context 4096  slots 2
tunnel    tc-no-tunnel-in-this-build  relay none  0 clients
queue     0 in flight, 0 waiting

ID        NAME   STATUS  IN FLIGHT  RPM  TODAY  LAST SEEN
k_65f33b  alice  active  0          0    0      never
```
`admin.sock` is `srw-------`; `keys.json`, `usage.jsonl`, and `config.json` are `0600`; the data
dir is `0700`. Without a running host, `status` exits 1 with
`no host is running for <dir> — start one with 'bunny-network serve'`.

**5. An upstream that is down is a warning, not a failure** (promise 2), while detection finding
*nothing* is fatal, because there is then no URL to retry:
```
upstream  openai-compatible  http://127.0.0.1:39999  (not answering)
WARNING: http://127.0.0.1:39999 is not answering. Friends get 503 upstream_down until it does; retrying every 10s.

$ bunny-network serve            # nothing on 8080/11434/1234/8000
bunny-network: no local inference server found on 127.0.0.1 ports 8080 (llama.cpp),
11434 (Ollama), 1234 (LM Studio), 8000 (vLLM); pass --upstream URL     # exit 1
```

### Two real defects found and fixed (each with the fixture that would have caught it)

- **`keys add alice --rpm 60` silently ignored the flags.** The standard `flag` package stops
  parsing at the first positional, so every documented `keys <verb> ID --flag` invocation in
  promise 5 was broken. `reorder()` in `main.go` moves positionals behind the flags (respecting
  `--flag=value`, bool flags, and an explicit `--`); `TestFlagsAfterPositionals` covers it.
- **A long `--data-dir` failed with `bind: invalid argument`.** `sockaddr_un.sun_path` is 104 bytes
  on darwin, 108 on linux. Now refused up front with the length and the limit;
  `TestTooLongDataDirSaysWhy` covers it. The default data dir is far below the limit.

### Edge awareness

Handled: a half-written final line in `usage.jsonl` (counted, skipped, never fatal); a stale
`admin.sock` from a killed host (replaced) versus a live one (second host refused); a write that
cannot land leaves the store byte-identical (rollback, tested for `Add` and `SetLimits`); the
hot-reload stat is throttled to once a second so `Lookup` on the request path is cheap; `Record`
drops-and-counts rather than blocking when the disk cannot keep up; a tokenizer that errors
degrades to the estimate instead of failing the request; `Refresh` against a dead engine keeps the
last known models and context while flipping `Healthy`; `--upstream http://h:8000/v1` and bare
`h:8000` both normalise.

### How it was verified

```
go build ./...   OK (exit 0)
go vet ./...     OK (exit 0)
go test ./...    ok — 56 passed / 0 failed / 0 skipped   (5 packages, 1 with no test files)
go test -race ./...   ok (all 5 packages)
```
Plus the manual runs above (llama.cpp, vLLM, upstream-down, no-upstream-found), the full
`keys` lifecycle (`add`/`list`/`pause`/`resume`/`revoke`/`rotate`/`limits`, by id and by unique
name), `--data-dir` in both positions, and every documented `-h`.

### Judgment calls — please confirm or overrule

1. **`--log-prompts` and `--ephemeral` are deliberately NOT persisted to `config.json`**, though
   promise 2 says flags are persisted. Reason for `--log-prompts`: a host who debugs once must not
   silently keep recording their friends' conversations forever (BELIEFS Protection 3 / "prompts
   are never logged by default"). `--ephemeral` is a per-run mode that changes the host address,
   not a setting. Both are documented in the help and asserted by a test. Everything else persists.
2. **`--upstream-key` IS persisted** to `config.json` (mode 0600), because `serve` must be
   restartable without retyping it. Flagging it because it is the only secret written to disk in
   plaintext; it is the host's own key, not a friend's.
3. **ID arguments accept a unique key name as well as a `k_…` id** (`keys pause alice`). This adds
   no flag or verb, only a resolution rule, documented in the help; an ambiguous name is refused
   with "use the key id instead".
4. **`Add` applies defaults to zero fields; `SetLimits` does not.** So `keys add alice` gets the
   contract's defaults, and `keys limits alice --rpm 0` means the host explicitly saying "no
   limit". Tested both ways.
5. **`upstream.Slotted`** is one extra exported name beyond the ticket's `Detect`/`Open`, because
   `Open(ctx, url, apiKey)` has nowhere to carry `--slots`. `Detect` and `Open` keep the documented
   signatures exactly.
6. **The QR is drawn with explicit ANSI black-on-white.** A QR in the terminal's own colours comes
   out inverted on a dark theme and not every phone camera reads an inverted code. It is always
   coloured, even when piped.

### Wiring status — READ THIS BEFORE LANDING

`origin/main` is still `f171164`: **001 and 002 have not landed**, so nothing here imports
`internal/tunnel`, `internal/invite`, or `internal/gateway`.

- `cmd/bunny-network/wire.go` (`//go:build wire`) is the **real** wiring, written against
  `docs/ARCHITECTURE.md` and ticket 002 promise 1. It parses clean but has never been compiled,
  because the packages do not exist yet. Expect it to need a nudge if 001/002 land with any drift
  — specifically `tunnel.Status`'s field names (`RegionName`), `tunnel.SavedAddr`'s signature, and
  whether `gateway.New` returns an error.
- `cmd/bunny-network/wire_stub.go` (`//go:build !wire`) is the default build. It has **no tunnel
  and no gateway**, prints a three-line warning at the top of every `serve` saying so, and hands
  out the self-describing placeholder address `tc-no-tunnel-in-this-build`. It exists so
  everything this ticket owns could be exercised through the real binary today.
- **To flip it:** delete `wire_stub.go` and remove the build tag from `wire.go`. Nothing else
  changes; the CLI talks to both through `tunnelServer` / `gatewayServer` interfaces in `main.go`.

Not verified, and not verifiable here: `ServeDev` (002's), the real relay region and client count
(001's), the real invite string (001's `invite.Encode` — the stub reproduces the documented
`bn1.<addr>.<secret>` format in one line), and the Windows admin fallback beyond
cross-compilation and its permission assertions.

### For the PM to re-price

- **Size.** See the Contest above: 2790 raw / 2366 code against a 2000 budget.
- **Follow-up, small:** flip the `wire` tag and re-verify end to end once 001 and 002 land.
- **Adjacent, not fixed (out of scope):** `docs/MEASURE.md`'s laptop command says `-c 8192` but the
  running llama-server reports `n_ctx: 4096`; the Background hypothesis inherited the same number.
  Worth correcting so the first published context figure is right.
- **Known limitation, by design:** `keys list` reads all of `usage.jsonl` to compute "last seen".
  Fine at demo volumes; if the log grows past a few hundred MB it wants an index or a tail read.
- **`go mod tidy` cannot run until 001 and 002 land.** It resolves every build tag, so it tries to
  fetch `internal/tunnel`, `internal/invite`, and `internal/gateway` and fails. `go build`, `go vet`
  and `go test` are unaffected (they honour the default tag set). Run `go mod tidy` as part of
  flipping the `wire` tag. `github.com/skip2/go-qrcode` is therefore marked direct by hand.

### Freeze

- **Base commit:** `f171164` (= `origin/main` at freeze; 001 and 002 have not landed)
- **Lane:** worktree branch `t003-cli-store-upstream`
- **Code patch SHA-256** (`git diff origin/main -- ':!pm/'`): `d7adce43940a421f74168a25bc98d8f603ee2fd404083968a62b22dbd0c7b5bf`
- **Accounting vs. declared budgets**

| Bucket | Measured | Budget | Verdict |
|---|---|---|---|
| Source lines (non-test `.go`, raw) | 2790 | ≤2000 | **over by 790 — contested above** |
| Source lines (non-blank, non-comment) | 2366 | — | over by 366 |
| — of which help text (product surface, promise 1) | 128 | — | |
| — of which `wire_stub.go` (deleted when 001/002 land) | 89 | — | |
| Test lines (excluded from the budget) | 1494 | — | |
| Concepts | 6 | 6 | **met exactly** |
| New dependencies | 1 (`github.com/skip2/go-qrcode`) | — | Background blessed it |
| Files touched outside the scope contract | 0 | 0 | met |

- **Checks at freeze:** `go build ./...` OK · `go vet ./...` OK · `go test ./...` 56 passed /
  0 failed / 0 skipped · `go test -race ./...` OK · cross-compiles darwin, linux, windows (amd64) OK.

## Ruling (PM, 2026-09-02 03:25)

**Contest accepted; re-priced to 8.** The overrun is promised surface, not padding: example-led help
(promise 1), the Windows admin fallback (promise 7), and an 89-line stub that dies at wiring. Concept
budget met 6/6, zero files outside scope, one blessed dependency. Lesson written back for the PM:
size-5 pricing under-counted help text and per-platform fallbacks; price those as surfaces next time.

**Judgment calls confirmed:** `--log-prompts` and `--ephemeral` not persisted (Protection 3; per-run
choices) — ARCHITECTURE.md now says so. `--upstream-key` persisted at 0600. Name-or-id references.
`SetLimits` does not apply defaults (explicit edit = explicit values). `upstream.Slotted` accepted.

**Landed** on main at the ff-merge of `2d4ad39`. Wiring flip (`wire.go` tag off, `wire_stub.go`
deleted) happens when 001 and 002 land. Adversarial review dispatched post-landing.

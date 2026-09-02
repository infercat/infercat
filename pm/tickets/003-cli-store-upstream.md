---
id: 003
title: CLI, key file store, upstream adapters, usage recorder, admin status
kind: sensitive
size: 5
status: dispatched
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

## Report

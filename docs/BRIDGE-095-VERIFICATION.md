# 095 — edge key-hash admission verification

The host sends only the stored SHA-256 values for active friend keys when the bridge connects and
after every key change. A snapshot replaces the object's prior set in SQLite-backed Durable Object
storage. A hibernated object restores that set before serving requests. A newly connected socket
clears the old set and remains fail-closed until its first replacement snapshot arrives.

The Worker hashes `Authorization: Bearer <key>` and performs a constant-time comparison against
every stored hash. Missing, malformed, unknown, paused, and revoked keys receive HTTP 401
`invalid_key` with the gateway's `authentication_error` shape before the rate counter, request-body
reader reservation, or host queue. The host gateway remains the final authentication authority.

Successful in-process `FileStore` commits emit a coalesced, nonblocking notification after the
atomic file replacement. The bridge re-reads the complete store rather than applying deltas. Failed
writes do not notify. Cross-process CLI mutations retain the existing local admin `/reload`; an
unchanged `bridge.json` now triggers a snapshot publication instead of an early no-op. This shared
store hook covers console mint, pause, resume, revoke, rotate, and limit writes without adding
three route-specific callbacks.

Local tests cover active-only snapshots, pause/revoke removal, set replacement, constant-time
comparison across the whole set, persisted-set restoration, unchanged-config reload publication,
committed-write notification, and edge refusal before the rate/body/queue budgets. The live preview
used Worker version `b4142c96-5254-4eaf-9edb-0d897cbcc5a1`, built by the PM from commit `fe265a4`.
The host and llama-server used a fresh isolated data directory and port 63295; the launch host and
other lanes' engines were untouched.

## Live preview evidence

A valid streamed request ran for 6.19 seconds while 200 invalid requests ran for 3.674 seconds.
Half omitted Authorization and half supplied a random bearer. Every invalid request returned:

```json
{"status":401,"code":"invalid_key","type":"authentication_error"}
```

The intervals overlapped. The friend request returned HTTP 200, streamed 6,143 characters beginning
`ALPHA ALPHA ALPHA`, and ended with `data: [DONE]`. Across the flood and stream, the host gained
exactly one usage event and printed exactly one request line:

```text
12:15:13  edge-overlap-proof  chat  via bridge  /Users/yuanpingsong/.cache/bunny-network/models/gemma-4-E2B-it-Q4_K_M.gguf  28→1024 tok  ttft 57ms  6.2s  ok
```

The 200 rejected requests produced no host request or usage event. This shows edge refusal occurs
before the host's rate window and queue while an admitted friend continues streaming.

After `keys revoke --yes` committed a disposable proof key and the existing admin reload published
the replacement set, its next checked request returned HTTP 401 on the first attempt:

```json
{"error":{"message":"unknown key; check the invite","type":"authentication_error","code":"invalid_key"}}
```

The usage log had zero new entries for that attempt, proving the refusal occurred at the edge rather
than in `Gateway.Handler()`. Both disposable proof keys were revoked. The bridge was turned off and
the isolated host and engine were stopped after the checks.

## Verification

- Go race suite: 381 passed / 0 failed / 2 skipped. The skips are the existing opt-in
  `TestLiveLlamaCPP` and `TestSavedAddrLive`.
- Worker typecheck and dry-run build: pass. Worker tests: 24 passed / 0 failed / 0 skipped.
- Clean Worker dependency install and audit: pass, 0 vulnerabilities.
- `make check`: `CHECK OK`; installer tests 18 passed / 0 failed / 0 skipped.

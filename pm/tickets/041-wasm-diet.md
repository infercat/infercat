---
id: 041
title: The browser tunnel on a diet — shrink the wasm without changing a byte of behaviour
kind: normal
size: 2
status: dispatched
updated: 2026-09-07
release: demo-1.1
---

# 041 — The wasm on a diet

**Why.** Every friend's first visit downloads the tunnel: today 26.9 MB raw, 6.19 MB gzip
(`web/public/infercat.wasm{,.gz}`, built by `web/wasm/build.sh` via `make wasm`). It is the whole cost
of "no account, no install", a few seconds on a phone over cellular. Go's wasm output is known to
shrink materially with symbol stripping and a wasm optimizer; the gzip copy is what crosses the wire
(hosting/README.md: only the gzip ships on Pages). Smaller is faster for every friend, forever.

**Promises.**
1. `make wasm` produces a smaller `infercat.wasm.gz` with **no behaviour change**: `make check` (which
   runs the wasm package tests and `web/wasm/leak-check.mjs`), the web suite, and `make launch-check`
   with a real invite against a host from your branch all pass exactly as before. The Go API surface
   of the bridge (`window.InfercatTunnel`) is untouched.
2. A table in the ticket log: bytes raw and gzip for each step tried — baseline, `-ldflags="-s -w"`
   (check whether it is already set), `-trimpath`, `wasm-opt -Oz` (binaryen; `brew install binaryen`;
   also try `-O3`/`-Os` and `--strip-debug --strip-producers`), and any Go build tag that removes an
   unused tailcat/tailscale feature safely (only if the tests prove it unused). Keep what wins; drop
   what does not; say why.
3. `make wasm` prints both sizes, and `docs/MEASURE.md` gets one line with the new numbers dated.
4. Brotli is NOT a target: the app decompresses in the browser with `DecompressionStream`, which
   supports gzip and deflate only. Say so in the log if you looked.

**Size 2**, concept budget **0** (no new flag, state, file or step a user sees). Tests: the promises —
the existing suite and leak check stay green; no new tests unless a build tag needs one to prove
the feature unused. Scope: `web/wasm/build.sh`, the `wasm` target in `Makefile`, `docs/MEASURE.md`,
this ticket's log. Non-goals: the loader (`web/src/transport/wasm.ts`), the hosting, the raw-wasm
fallback (that is R2, a separate ticket), anything in the Go host.

**Evidence of completion:** branch `t041-wasm-diet` with ONE commit "wasm: … (041)", pushed, not merged;
the size table and the gate lines in the log; the regenerated `web/public/infercat.wasm.gz` committed
(the raw `.wasm` is gitignored). Reply to the PM in the embassy conversation with the branch, the commit,
the before/after sizes, and the gate lines. The PM lands.

## Log

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

### 2026-09-07 — engineer implementation and verification

PM ruling: leave `make check` unchanged; run `node web/wasm/leak-check.mjs` explicitly
against the optimized artifact, plus `make launch-check` with a real branch-host invite.
Base `59cdbec483ddbfa132d4c8732527242d18648114`, lane `t041-wasm-diet`.

Kept `-trimpath` and a single Binaryen 132 `-Oz --strip-debug --strip-producers` pass.
`--enable-bulk-memory --enable-nontrapping-float-to-int` recognizes instructions Go
already emits; without them Binaryen rejects the input during validation. The bridge
API and Go code are untouched. Existing `-s -w`, deterministic `gzip -9 -n`, and both
size prints were already present; no credit claimed for those.

Bytes, same Go toolchain and gzip command, cumulative from the stripped baseline:

| Step tried | Raw bytes | Gzip bytes | Decision |
|---|---:|---:|---|
| Baseline / existing `-ldflags="-s -w"` | 26,933,654 | 6,189,634 | Already set |
| Add `-trimpath` | 26,855,021 | 6,187,792 | Keep |
| Add `wasm-opt -O3` | 25,259,002 | 6,164,553 | Loses to Oz |
| Add `wasm-opt -Os` | 25,260,910 | 6,161,775 | Loses to Oz |
| Add `wasm-opt -Oz` | 24,507,365 | 6,116,556 | Best optimization level |
| Oz, then a separate strip-debug/strip-producers pass | 24,950,848 | 6,519,133 | Discard: second serialization grows gzip |
| Oz + strip-debug/strip-producers in one pass | 24,507,292 | 6,116,486 | Keep |
| Final `make wasm`, edited tree; artifact used by both behavior gates | 24,507,300 | 6,116,514 | Ship |

Go embeds VCS dirty status, so the final edited-tree build differs slightly from the
clean-tree experiment. The shipped gzip saves **73,120 bytes (1.18%)**; raw saves
**2,426,354 bytes (9.01%)**. These are measured gains, not the larger gain originally
hoped for. `make wasm` still prints both byte counts.

No extra Go tag tried: tailcat v0.4.0 `internal/buildtags/buildtags.go` keeps only
`netstack` for wasm and omits the remaining registered optional features already;
`web/wasm/build_test.go` explicitly forbids omitting the browser's required TCP stack.
No safe unused feature was identified. Brotli was not tried: the product loader's
DecompressionStream path supports gzip/deflate; Brotli is outside this ticket.

Build prerequisite: Binaryen/`wasm-opt` (tested 132; `brew install binaryen`), now
required by the build script. No new product flag, configuration, state or user step.
No hosting/CI provisioning changes were made within this ticket's scope.

Gates (all exit 0):

```text
make check
go vet ./...
go test ./...
ok  	github.com/2185Lab/infercat/cmd/infercat	(cached)
?   	github.com/2185Lab/infercat/hack/load	[no test files]
?   	github.com/2185Lab/infercat/hack/tunneldemo	[no test files]
ok  	github.com/2185Lab/infercat/internal/admin	(cached)
ok  	github.com/2185Lab/infercat/internal/gateway	(cached)
ok  	github.com/2185Lab/infercat/internal/invite	(cached)
ok  	github.com/2185Lab/infercat/internal/keys	(cached)
?   	github.com/2185Lab/infercat/internal/product	[no test files]
ok  	github.com/2185Lab/infercat/internal/tunnel	(cached)
ok  	github.com/2185Lab/infercat/internal/upstream	(cached)
ok  	github.com/2185Lab/infercat/internal/usage	(cached)
ok  	github.com/2185Lab/infercat/web/wasm	(cached)
CHECK OK

go test -json ./... (count detail): 270 passed / 0 failed / 2 skipped (tests and subtests)
```

The two existing opt-in live tests, `TestSavedAddrLive` and `TestLiveLlamaCPP`, skip
under the standard command. The explicit leak and real-invite launch checks below
ran live and passed. Web: 266 passed / 0 failed / 0 skipped.

```text
pnpm typecheck
$ tsc --noEmit
pnpm test
$ vitest run

 RUN  v3.2.7 /private/tmp/infercat-wt-041/web

 ✓ src/ui/clipboard.test.ts (4 tests) 2ms
 ✓ src/ui/pointer.test.ts (2 tests) 2ms
 ✓ src/product.test.ts (2 tests) 2ms
 ✓ src/stream.test.ts (43 tests) 6ms
 ✓ src/storage.test.ts (37 tests) 16ms
 ✓ src/invite.test.ts (46 tests) 16ms
 ✓ src/session.test.ts (48 tests) 10ms
 ✓ src/transport/http1.test.ts (17 tests) 32ms
 ✓ src/transport/transport.test.ts (6 tests) 444ms
   ✓ abort during the dial > rejects while the dial is still pending, and closes the conn when it lands  412ms
 ✓ src/connect.test.ts (14 tests) 696ms
 ✓ src/api.test.ts (47 tests) 1204ms

 Test Files  11 passed (11)
      Tests  266 passed (266)
   Start at  19:31:23
   Duration  1.47s (transform 389ms, setup 0ms, collect 842ms, tests 2.43s, environment 1ms, prepare 403ms)

pnpm lint
$ eslint .
make wasm
sh web/wasm/build.sh
 24507300 web/public/infercat.wasm
 6116514 web/public/infercat.wasm.gz
 30623814 total
make notices-check
sh hack/notices.sh check
notices: OK — 49 Go + 110 npm dependencies + 2 bundled fonts, licences all in ALLOWED, verbatim texts present
```

Leak command: `node web/wasm/leak-check.mjs "http://127.0.0.1:59081/wasm/demo.html?addr=<test tunnel address>"`.
The branch-built `hack/tunneldemo` served the optimized raw artifact; 200 real tunnel
dials, six malformed write types rejected, session stayed alive, handles bounded.
Verbatim output:

```text
{
  "before": {
    "heapAllocBytes": 807016,
    "liveFuncs": 5
  },
  "samples": [
    {
      "i": 50,
      "liveFuncs": 5,
      "heapAllocBytes": 1136720
    },
    {
      "i": 100,
      "liveFuncs": 5,
      "heapAllocBytes": 1472128
    },
    {
      "i": 150,
      "liveFuncs": 5,
      "heapAllocBytes": 1804608
    },
    {
      "i": 200,
      "liveFuncs": 5,
      "heapAllocBytes": 2121264
    }
  ],
  "after": {
    "liveFuncs": 5,
    "heapAllocBytes": 2038416
  },
  "bad": [
    "ArrayBuffer: rejected (write requires a Uint8Array)",
    "DataView: rejected (write requires a Uint8Array)",
    "object: rejected (write requires a Uint8Array)",
    "array: rejected (write requires a Uint8Array)",
    "string: rejected (write requires a Uint8Array)",
    "undefined: rejected (write requires a Uint8Array)"
  ],
  "alive": true,
  "closed": {
    "liveFuncs": 2,
    "heapAllocBytes": 903680
  }
}
N=200: liveFuncs 5 → 5 (bound 13); heap 0.77 → 1.94 MiB (Δ 1.17); after session.close() liveFuncs 2
PASS
```

Launch command: `INVITE=<isolated host invite> CHECK_PORT=59083 make launch-check`.
Branch-built host used a fresh data directory and port 59082, upstream 127.0.0.1:18080;
invite made with `keys add x --no-qr`. Desktop and phone sent real model requests,
dark chat connected, and the phone screenshot was visually inspected. Verbatim output:

```text
sh web/wasm/build.sh
 24507300 web/public/infercat.wasm
 6116514 web/public/infercat.wasm.gz
 30623814 total
cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm build
Already up to date
Done in 170ms using pnpm v11.13.0
$ tsc --noEmit
$ vite build
vite v7.3.6 building client environment for production...
transforming...
✓ 506 modules transformed.
rendering chunks...
computing gzip size...
dist/manifest.webmanifest         0.63 kB
dist/index.html                   2.78 kB │ gzip:   0.95 kB
dist/assets/index-DRPxfiVY.css   21.10 kB │ gzip:   5.35 kB
dist/assets/index-CaMaWFS_.js   236.15 kB │ gzip:  76.15 kB
dist/assets/Chat-CLVUu8jx.js    363.39 kB │ gzip: 111.42 kB
✓ built in 648ms
cd web && pnpm launch-check
$ node dev/launch-check.mjs
launch-check on http://127.0.0.1:59083
  connect: title, description, OG/Twitter metas, theme-color, manifest "Infercat" (3 icons), 7 assets served
  30-connect-desktop: 7 requests, 0 to a third party — none, all same-origin
  30-connect-desktop: 7 controls in the accessibility tree, 0 unnamed
  30-connect-desktop: 16 text runs, lowest 6.31:1 (p.promise) — AA met
  30-connect-desktop: Tab → a[Host your own] → a[Source] → textarea[Invite code] → button[Paste] → a[host your own] → a[Source on GitHub]
  dev/screenshots/30-connect-desktop.png  59 KB
  30-connect-dark: 7 requests, 0 to a third party — none, all same-origin
  30-connect-dark: 7 controls in the accessibility tree, 0 unnamed
  30-connect-dark: 16 text runs, lowest 7.5:1 (a) — AA met
  30-connect-dark: Tab → a[Host your own] → a[Source] → textarea[Invite code] → button[Paste] → a[host your own] → a[Source on GitHub]
  dev/screenshots/30-connect-dark.png  60 KB
  30-connect-phone: 7 requests, 0 to a third party — none, all same-origin
  30-connect-phone: 5 controls in the accessibility tree, 0 unnamed
  30-connect-phone: 12 text runs, lowest 6.31:1 (p.pitch) — AA met
  30-connect-phone: Tab → textarea[Invite code] → button[Paste] → a[host your own] → a[Source on GitHub] → summary[About]
  30-connect-phone: no horizontal overflow at 390px
  dev/screenshots/30-connect-phone.png  94 KB
  dev/screenshots/30-og-preview.png  112 KB
  dev/screenshots/30-home-screen-icon.png  170 KB
  chat: connected in 0.8 s (relayed via New York · 81 ms)
  /private/tmp/infercat-wt-041/docs/media/friend-chat.png  59 KB
  chat: 12 controls in the accessibility tree, 0 unnamed
  chat (light): 41 text runs, lowest 5.63:1 (button.conv-del) — AA met
  chat: Tab → button[New chat] → button[Why is the sky blue? Answe] → button[Delete Why is the sky blue] → button[Disconnect] → button[What these limits mean] → button[Settings] → button[Edit] → button[Thinking191 words] → div[Analyze the Request: The u] → button[Copy]
  chat: 15 requests, off-origin — https://tailcat.dev, https://tc301a.ipn.dev (the relay is the only one Protection 3 allows)
  dev/screenshots/30-chat-desktop.png  59 KB
  /private/tmp/infercat-wt-041/docs/media/friend-chat.gif  869 KB
  chat-phone: 13 controls in the accessibility tree, 0 unnamed
  chat-phone: 37 text runs, lowest 5.63:1 (button.conv-del) — AA met
  chat-phone: no horizontal overflow at 390px
  dev/screenshots/30-chat-phone.png  126 KB
  chat-dark: 18 text runs, lowest 7.18:1 (button.conv-del) — AA met
  dev/screenshots/30-chat-dark.png  59 KB

launch-check: OK
```

No production service changed. Live actions: isolated tunnel/host processes, local test
invite, and model requests to the PM-authorized engine. Founder demo port 9091 and
~/.claude/jobs untouched. Generated launch images were retained outside the diff.
No promised work left undone; build-tool provisioning outside these scoped files is
called out above for the PM. The gzip is force-added because the base ignores it,
as explicitly required by the ticket; raw wasm and wasm_exec.js remain untracked.

Accounting: size 2 ceiling 400 source lines; build script +5/-1, 6 changed source lines;
no test changes, no UI surfaces, docs/measurement/log and one generated gzip artifact
accounted separately. Product concept count 0/0. Makefile unchanged per ruling.

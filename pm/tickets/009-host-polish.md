---
id: 009
title: Host polish — first-run truth, invite destination, key hygiene, accounting words (from the stranger pass)
kind: normal
size: 3
status: frozen
updated: 2026-09-02
release: demo-1
---

# 009 — Host polish (CLI first ten minutes)

## Binding

**Why.** Three fresh-context strangers set up the host from the README and `--help` alone (llama.cpp,
Ollama-user-without-Ollama, remote vLLM). All reached an invite in 2–6 minutes; none gave up; all
three praised `--help`, the banner, the error house style, and the privacy posture — keep those. What
they hit is below, ranked by the synthesis. The founder's theme is polish: a stranger's first ten
minutes must be clean.

**Promises (each with a test where logic is involved; copy items get a before/after in the report).**
1. **First-run address bug (blocker).** On a fresh data dir, the first `serve` prints a tunnel address
   that differs from the address every later `serve` (and `SavedAddr`) produces, so invites minted in the
   first session never connect. Find the cause in `internal/tunnel` (hypothesis: the region pinned into
   `host.key.json` and the region the first start actually used diverge; the address must be derived
   from the saved key exactly as `SavedAddr` does, and the server must start with that pinned region).
   Test: fresh dir → `Start` → `Addr()` equals `SavedAddr(dir)` equals `Addr()` after restart. This is
   001's file; you may edit it under this ticket.
2. **Invite destination.** Add `product.WebURL` (string constant, may be empty until hosting is decided)
   and a `serve --web-url` override persisted in config.json. When set, `keys add`/`rotate` print
   `Send Alice this link: <WebURL>#<invite>` and the QR encodes the link; the banner names the web URL
   on its own line. When empty, print `Alice pastes this code into the web app (serve web/dist yourself
   for now — see README)`. The web app already accepts a `#bn1.…` fragment (ticket 007 adds it).
3. **Duplicate names.** `keys add <name>` refuses when an active or paused key with that name exists:
   `alice already has an active key (k_…, last seen …). Lost the invite? keys rotate alice. Second
   device? keys add alice-laptop.` `--force` mints anyway.
4. **`--name` default** = OS hostname; banner shows `name  <value>  (shown to your friends)`; if truly
   empty the `/me` host name is empty and the client omits the sentence (007 handles the client side).
5. **No-upstream error** ends with the runnable fix: `bunny-network serve --upstream http://127.0.0.1:<port>`
   and names that `--upstream` is a `serve` flag.
6. **Accounting words.** `usage` counts model calls as requests and shows web-app polls separately
   (`requests 1 model call (+5 app polls)`); `status` RPM and `usage` agree on the definition. Latency
   percentiles are over successful completions only. Limits line says what the numbers count
   (`completions only; /v1/models is free` — verify against the gateway: 006 made `/v1/models` consume
   RPM; pick the truthful sentence).
7. **Blast radius line** in the banner: `access  friends reach only /v1/models and /v1/chat/completions
   on <upstream> — nothing else on this machine`. Verify the route list against the gateway.
8. **Data dir truth.** Banner gets a `data  <dir>` line. First creation of `host.key.json` prints a
   one-time note: it is the host identity, back it up, do not sync it, deleting it invalidates every
   invite. `--help` for `--data-dir` names the files.
9. **Revoke hygiene.** `keys revoke` asks `[y/N]` (or `--yes`) and names `pause` as the reversible
   alternative. After any key write, the CLI pokes the running daemon over the admin socket
   (`POST /reload`, unix socket only, Protection 1 intact) so the change is live before the command
   returns; if no daemon, nothing. Test: revoke → next request 403 without waiting a second.
10. **Remembered upstream that is down.** The warning names the config file and the way out:
    `serve --upstream auto` re-runs detection and forgets the remembered URL.
11. **Machine-readable invite.** `keys add --json` prints `{"key_id","name","invite","link"}` only; the
    QR is skipped automatically when stdout is not a TTY, and by `--no-qr`.
12. **One-liners.** Pluralise `1 keys`; the zero-active banner line is actionable; `keys list` LAST SEEN
    derives from model calls, not polls.

**Size 3** (≤900 source lines). Concept budget 1: `web_url` config key. **Normal**.

**Scope contract.** `cmd/bunny-network/**`, `internal/admin/**` (reload endpoint + client),
`internal/keys/store*.go` (name uniqueness helper), `internal/usage/aggregate*.go` (accounting words),
`internal/product/product.go` (WebURL), `internal/tunnel/**` (promise 1 only). Not `internal/gateway`
(if the accounting needs a gateway change, contest with the symbol). Not `web/**`.

**Keep as is (from the strangers; do not "improve"):** `--help` and per-command help; the banner's
order and density; error house style (cause + fix in one sentence); flag memory; `--log-prompts`
wording; the invite output's self-explanation and QR; commands working without the daemon; graceful
shutdown wording; empty states.

## Background

- Raw reports and synthesis: workflow `wf_e6401585-7ab` journal under
  `~/.claude/projects/-Users-yuanpingsong-Desktop-repos-2185Lab-kb/12b4a99c-7f66-4439-8d81-9c1d93c1cdee/subagents/workflows/`.
- The README quickstart and the Makefile `web`→`wasm` dependency are the PM's (landing separately).

## Log

- 2026-09-02T14:05Z ACK. Base `6357ef9` (= origin/main), worktree branch `t009-host-polish`. Read BELIEFS,
  ARCHITECTURE, 009, and the 003/005 reports; read `cmd/bunny-network`, `internal/{admin,keys,usage,tunnel,product}`
  and (read-only, for truth) `internal/gateway`.
- 2026-09-02T14:20Z **Promise 1, reproduction.** Fresh `--data-dir` under `~/.claude/jobs/12b4a99c/tmp/bn009-data`,
  `serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:62090` twice: run 1 and run 2 printed the *same*
  address (`tco2FwWCDXiO…FpGQEt`) and `SavedAddr` returned that same string. **The stated symptom does not reproduce
  sequentially.** Went to the stranger's raw evidence instead (workflow `wf_e6401585-7ab`, agent
  `a5cf04d36365db8ba`), decoded the two blobs he printed, and found the real shape — see below.
- 2026-09-02T14:35Z **Promise 1, root cause (the PM's hypothesis is refuted).** The two addresses the vLLM stranger
  saw differ in the **node and disco keys**, not the region: both CBOR blobs end `19 01 2d` = RegionID 301. Run 1's
  blob carries node key `6b206e61…`; `host.key.json` (mtime 11:03, unchanged for the rest of the session) carries
  `130b99ae…`, which is runs 2 and 3's blob. So run 1 served an identity that never reached disk. His data dir also
  holds `serve1.log` (11:03) containing `another host is already serving this data dir` — a second `serve` was
  starting on the same dir in the same second. `internal/tunnel/tunnel.go:96 writeKey` is a plain temp+rename
  (last writer wins) and `Server.addr` (:101) is derived from the in-memory key, never from what landed, so two
  hosts racing on a *fresh* data dir leave the survivor advertising an address whose key was overwritten. Only the
  first run can do this (once the key is pinned, `pin` is false and nothing writes), which is exactly why it lands
  on `Start here: serve` → `keys add alice`.
- 2026-09-02T14:55Z **Promise 1, fix.** `internal/tunnel/tunnel.go`: `writeKey` became `saveKey` (:214) —
  the identity is staged in an O_EXCL temp file and claimed with `os.Link`, which is atomic *and*
  exclusive, and it returns the identity that is on disk afterwards; a filesystem without hard links
  falls back to rename and re-reads what landed. `identity` (:113) is the new front half of `Start`: it
  pins the relay, publishes the key, and adopts the winner's key if another host published first, so
  `Addr() == SavedAddr(DataDir)` always. Fixtures `TestSaveKeyIsCreateOnce` (8 concurrent claims agree)
  and `TestStartAgreesWithSavedAddr` (fresh → restart, plus two hosts starting together). Both **fail on
  the pre-fix behaviour** and pass after — the before/after is printed in the Report.
- 2026-09-02T15:20Z Promises 2–12 implemented; live pass on a throwaway data dir against the shared
  llama-server (`127.0.0.1:18080`, ports 62090/62091) — banner, first-run identity note, duplicate-name
  refusal, invite link + `--json`, revoke prompt, `usage`/`status` accounting, `--upstream auto`. Output
  in the Report.
- 2026-09-02T15:40Z Rebased on `origin/main` = `f9b1f2e` (007 and 008 had landed since dispatch; no
  conflicts, nothing of theirs is touched). Full check, race, three cross-compiles printed. Frozen.

## Report

### The core, in one minute

**Promise 1 (the blocker) — root cause, and the ticket's hypothesis is wrong.** The region was never the
problem: both of the addresses the stranger printed decode to `RegionID 301`. What differs is the *node
identity*. Run 1's blob carries node key `6b206e61…`; `host.key.json` — which never changed after 11:03 —
carries `130b99ae…`, the key in runs 2 and 3. So run 1 served an identity that was not on disk.

`internal/tunnel/tunnel.go` created the host key with a plain temp-file-and-rename and derived
`Server.addr` from its own in-memory key, so **two `serve` processes starting on a fresh data dir in the
same second each invented an identity, the last rename won the file, and the survivor kept advertising
(and minting invites for) a key that no longer existed.** The stranger's data dir contains exactly that
evidence: a `serve1.log` from 11:03 holding `another host is already serving this data dir`. Only the
first run can do it — once the key is pinned nothing writes — which is why it lands squarely on
`Start here: serve` → `keys add alice`.

The fix makes the host identity **create-once** and makes the printed address *be* what is on disk:

```
$ go test ./internal/tunnel -run 'SaveKeyIsCreateOnce|StartAgreesWithSavedAddr'

  with the old writeKey/rename behaviour restored:
  --- FAIL: TestSaveKeyIsCreateOnce
      round 1: saveKey returned tco2FwWCDNngWdt0…FpGQEv; want the first identity tco2FwWCAmA8ulGv…FpGQEu
  --- FAIL: TestStartAgreesWithSavedAddr
      host 1 advertised tco2FwWCCiDVSRyg…aHj1 but the saved identity is tco2FwWCAgHFoYU_…aHj1
  FAIL

  after the fix:
  ok  	github.com/2185Lab/bunny-network/internal/tunnel	0.266s
```

Live, on a fresh `--data-dir`: the first `serve` printed `tco2FwWCCEM0gzr1…FpGQEt`; so did the second and
third; `tunnel.SavedAddr` returns the same string; alice's invite (minted in the first session) carries it.

**The other eleven, as a host sees them.** First `serve` on a fresh dir, real engine:

```
Bunny Network 0.0.1-dev
upstream  llama.cpp  http://127.0.0.1:18080
          gemma-4-E2B-it-Q4_K_M.gguf  context 4096  slots 2
tunnel    tco2FwWCCEM0gzr16Yn_UezaFsYmqEymStLdcuKjgz57s2YZywOWFrWCA9kB…FpGQEt
relay     New York City
web       https://app.example.dev  (your friends open this and paste their invite)
name      Max's laptop  (shown to your friends)
access    friends reach only /v1/models, /v1/chat/completions and /v1/embeddings on http://127.0.0.1:18080
          nothing else on this machine — no other port, no files
data      /Users/…/bn009-data
          wrote host.key.json — this is your host identity. Back it up; don't sync it to
          Dropbox or a dotfiles repo; deleting it invalidates every invite you send.

Mint a friend: bunny-network keys add <name>
```

```
$ bunny-network keys add alice          # a second time
bunny-network: alice already has an active key (k_2b4b90, never used).
Lost the invite? `bunny-network keys rotate alice`. Second device? `bunny-network keys add alice-laptop`.
(--force mints a second key for this name anyway.)                                        # exit 1

$ bunny-network keys add bob --rpm 60
key k_427b98  bob
limits: 60 rpm · 20000 tpm · 1 concurrent · 2048 max output · the upstream's context · 200000 tokens/day · all models
        rpm counts model calls and the app's /v1/models lookup; tpm and tokens/day count model calls only

Invite for bob — it is shown once and stored only as a hash:

  bn1.tco2FwWCCEM0…FpGQEt.<SECRET-REDACTED>

Send bob this link:

  https://app.example.dev#bn1.tco2FwWCCEM0…FpGQEt.<SECRET-REDACTED>
  <QR of that link — omitted here because stdout was a pipe>

Lost it? `bunny-network keys rotate k_427b98` issues a new one and retires this.

$ bunny-network keys add carol --json
{"key_id":"k_166dd8","name":"carol","invite":"bn1.…","link":"https://app.example.dev#bn1.…"}

$ bunny-network keys revoke bob
Revoking is permanent — bob would need a new invite.
`bunny-network keys pause bob` stops them temporarily instead.
Revoke bob (k_427b98)? [y/N] n
nothing changed. Pass --yes to revoke without being asked.
```

One chat, one `/v1/models`, three `/me` polls from one key — the two surfaces now agree by construction
(`status` says `RPM 2` because rpm counts the chat *and* the models lookup, which is exactly what the
shared sentence says):

```
$ bunny-network usage                          $ bunny-network status  (tail)
requests  1 model call (+4 app polls)          ID        NAME  STATUS  IN FLIGHT  RPM  TODAY  LAST SEEN
tokens    21 prompt  333 completion            k_127dc2  dave  active  0          2    354    just now
ttft      median 2.0 s  p95 2.0 s
total     median 2.0 s  p95 2.0 s   (successful model calls only)
counts    rpm counts model calls and the app's /v1/models lookup; tpm and tokens/day count model calls only
```

```
$ bunny-network serve                    # nothing on 8080/11434/1234/8000
bunny-network: no local inference server found on 127.0.0.1 ports 8080 (llama.cpp), 11434 (Ollama),
1234 (LM Studio), 8000 (vLLM); pass --upstream URL — like this, as a flag of `serve`:

  bunny-network serve --upstream http://127.0.0.1:<port>

$ bunny-network serve --upstream http://127.0.0.1:39999
WARNING: http://127.0.0.1:39999 is not answering. Friends get 503 upstream_down until it does; retrying every 10s.
         It is remembered in /Users/…/config.json — `bunny-network serve --upstream auto` detects again and forgets it.

$ bunny-network serve --upstream auto     # config.json's "upstream" is gone; detection ran again
```

### Before / after, for the copy-only promises

| | before | after |
|---|---|---|
| 2 | invite, then nothing | `Send bob this link: <web>#bn1.…` (QR carries the link) or `bob pastes this code into the web app (serve web/dist yourself for now — see README).` |
| 4 | no name line | `name      Max's laptop  (shown to your friends)`; `--name` defaults to the hostname (minus `.local`) |
| 5 | `…; pass --upstream URL` | + `— like this, as a flag of \`serve\`:` and the runnable command |
| 7 | nothing about scope | `access    friends reach only /v1/models, /v1/chat/completions and /v1/embeddings on <upstream>` / `nothing else on this machine — no other port, no files` |
| 8 | `--data-dir DIR  where keys, usage, config, and the host key live` | `data      <resolved dir>` in the banner, a one-time identity note, and a six-line file manifest under `--data-dir` in `serve -h` |
| 10 | warning only | + `It is remembered in <config.json> — \`serve --upstream auto\` detects again and forgets it.` |
| 12 | `1 keys active` | `1 key active`; `1 key, none active — all paused or revoked; \`keys add <name>\` invites someone`; `keys list` LAST SEEN is the last model call, not the last poll |

### Two corrections to the ticket's own copy, made deliberately

- **Promise 7 named two routes; there are three.** `internal/gateway/request.go:78-86` proxies
  `/v1/embeddings` as well, so the banner says all three. `/me` and `/healthz` never reach the engine.
- **Promise 6's candidate sentence "completions only; /v1/models is free" is false.**
  `internal/gateway/proxy.go:257` calls `admitKey`, so `/v1/models` does take an RPM entry (006 promise 5);
  tokens, though, are only ever charged for a completion (`request.go:319`). The truthful sentence is the
  one now printed identically by `keys add`, `keys limits`, `usage`, and `status`.

### Edge awareness (one line)

Handled: a hard-link-less filesystem falls back to rename and still re-reads what landed; an adopted key
that is itself unpinned errors instead of looping; `--ephemeral` pins in memory and still writes nothing;
a revoked namesake frees its name but stays reachable by id (and by name while it is the only holder),
while two *live* namesakes are still ambiguous; a `--json` invite prints no QR and no prose; a
confirmation prompt with no readable stdin answers no; `keys add` refuses the duplicate before minting,
so no secret is burned; the reload poke is best-effort and a host that is not running is not an error.

### How it was verified

```
go build ./...                OK
go vet ./...                  OK
GOOS=windows go vet ./internal/admin   OK
go test ./...                 124 passed / 0 failed / 2 skipped   (the 2 are the opt-in live probes,
                                                                   TestLiveLlamaCPP and TestSavedAddrLive)
go test -race ./internal/{tunnel,keys,usage,admin} ./cmd/bunny-network   ok
gofmt -l cmd internal web hack   (empty)
GOOS=darwin  GOARCH=amd64 go build ./...:  OK
GOOS=linux   GOARCH=amd64 go build ./...:  OK
GOOS=windows GOARCH=amd64 go build ./...:  OK
```
Plus the live pass above: five `serve` runs on throwaway data dirs under
`~/.claude/jobs/12b4a99c/tmp/`, ports 62090/62091, against the shared llama-server at
`http://127.0.0.1:18080` (requests only — never restarted, never reconfigured). Four public-relay
connections in total. Every process I started was stopped; `pgrep` clean at the end.

### Declared loudly

- **Production-touching actions: none.** The shared llama-server received `/props`, `/v1/models`,
  `/tokenize` and one 333-token completion. max-ws.lab was never contacted.
- **Two fixtures were deliberately regressed to prove they bite** (`writeKey`-by-rename, and
  `reloadHost` short-circuited); both files were restored from a copy and the suite re-run green.
- **`internal/keys/store.go:263 index` changed meaning**: a name now resolves to the *live* key, so
  `keys add bob` after revoking bob leaves `keys pause bob` unambiguous. This falls out of promise 3
  allowing a revoked name to be reused; without it the promise creates a permanently ambiguous name.
  Covered by `TestNameResolvesToTheLiveKey`.
- **`admin.Serve` gained a third parameter** (the reload hook) and `admin.Reload` is new — both are what
  promise 9's `POST /reload` needs. Unix socket only; on Windows it needs the same token as `/status`
  (Protection 1 intact).
- **Not a bug:** two hosts started together on a fresh data dir now both serve the *same* identity for
  the second or so before the loser is refused at the admin socket. That is strictly better than the
  old behaviour (two different identities, one of them unreachable), and the loser still exits 1.

### For the PM to re-price / adjacent, not fixed

- `internal/upstream/client.go:127 ErrNoUpstream` still ends `; pass --upstream URL`, which now reads
  doubled next to promise 5's sentence. One-word fix in 003's file; out of this ticket's scope contract.
- The stranger's item about a mistyped `--upstream` being persisted *before* it is known to answer is
  only half-addressed: `--upstream auto` is the documented way out, but a URL that never answered is
  still written to `config.json`. Not persisting an unhealthy first-use URL is a separate decision.
- `serve`'s "another host is already serving this data dir" guard still runs *after* the tunnel starts.
  The identity is safe now either way, but moving the guard above `startTunnel` would stop a second host
  from opening a relay connection at all. Small, and a change to startup order — worth its own line.

## Freeze

- **Base commit:** `f9b1f2e` (= `origin/main` at freeze; 007 and 008 landed after dispatch, rebased onto them)
- **Lane:** worktree branch `t009-host-polish`
- **Code patch SHA-256** (`git diff origin/main -- ':!pm/' | shasum -a 256`): `54bcd1321371af1e479e65024180e65879f91eb0d361ff132c693cf1dc258d6a`
- **Accounting** (recomputed from the diff, `git diff origin/main --numstat -- '*.go'`)

| Bucket | Measured | Budget | Verdict |
|---|---|---|---|
| Source lines (non-test `.go`) | +533 / −93 | ≤900 | met, with room |
| — of which help/usage text (surface, per the 003/004 rulings) | ~19 | excluded | |
| Test lines (excluded from the budget) | +571 / −33 | — | |
| Files outside the scope contract | 0 | 0 | met |
| New dependencies | 0 | — | met |
| Concepts | 1 budgeted + 7 named by the promises | 1 (`web_url`) | see below |

**Concepts, honestly.** The budget line says 1 (`web_url`, the config key — delivered, plus
`product.WebURL` and `serve --web-url` which are the same concept's two other faces). Seven more are
named verbatim in the promises and were built because the promises name them: `keys add --force`
(promise 3), `--json` and `--no-qr` (promise 11, and the same two flags on `keys rotate`, which prints
the same invite — one concept each, not two), `keys revoke --yes` (promise 9), `admin POST /reload`
(promise 9), `serve --upstream auto` (promise 10 — a value, not a new flag). Nothing was invented
beyond the promise list.

- **Checks at freeze:** as printed in *How it was verified* above.

## Ruling (PM, 2026-09-02 16:20)

**Landed** on main (merge of `55a411a`); build/vet/test green, three cross-compiles OK, and a PM smoke on a
fresh data dir shows the same tunnel address on run 1 and run 2 — the blocker is closed with a root
cause (identity race, not region) the ticket had guessed wrong; recorded as the lesson. The two copy
corrections (embeddings is a third route; `/v1/models` takes an RPM entry) accepted. Concepts named in
the promises accepted. Re-price items → 012: `ErrNoUpstream` doubled suffix, unhealthy upstream persisted
before it answers, the "another host" guard ordering (already §4 item 15).

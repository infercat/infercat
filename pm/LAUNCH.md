# Launch checklist — demo-1 public launch (drafted 2026-09-02 16:30)

The release theme: "paste a code, chat with a friend's GPU". Every line below is a gate; the founder
gates the public ones (BELIEFS: Authority). Nothing ships while a line in **Founder decisions** is open.

## Founder decisions (open)

| # | Decision | Why it blocks | PM recommendation |
|---|---|---|---|
| F1 | **Name** | Invite prefix, binary name, web title, social post all carry it; renaming after launch breaks copied invites | Decide before the post. "Bunny Network" is do-not-ship (bunny.net); "taillama" collides with Meta's Llama mark. |
| F2 | **Relay** | Public tailcat relays are rate-limited and revocable at any time | One DigitalOcean VM + hostname running `derper`; bake into the host key at generation (`--region`); zero Tailscale infra in the product. |
| F3 | **Web app URL** | `keys add` prints a link only when `product.WebURL` is set; strangers need a destination | Vercel (founder's choice); set `product.WebURL`; the app is a static bundle. |
| F4 | **Distribution** | Strangers need a download, not a Go toolchain | **DECIDED 2026-09-03:** public repo, GitHub Releases (5 targets + web zip, already built by 017) + a Homebrew tap. Walkthrough in docs/RELEASE.md. |
| F5 | **License** | No LICENSE file; third-party notices are required by tailcat/tailscale (BSD-3) regardless | **DONE 2026-09-03: MIT**, copyright Yuanping Song; LICENSE at the repo root and in every archive; notices already ship (017). |
| F6 | **History** | A 28 MB binary sits in git history (`ff` commits before `e5b0a11`) | Rewrite once before the repo goes public (now required by F4); never after. PM runs it the day the repo is made public. |
| F7 | **Umbrella** | Decides the domain (F3), the GitHub location, and the footer attribution | Raised by the founder 2026-09-03 07:40. PM recommendation: legal and commercial entity stays 2185 Lab (no new entity before a paid signal); the product stands on its own name with its own domain and its own GitHub org (`github.com/<name>/<name>`, the canonical open-source shape; transfer is one click and GitHub redirects the old URL); "by 2185 Lab" in the footer and the post; not a Bunny-family app (cross-platform, CLI-first, MIT, GitHub-first audience), but the Local AI for Mac directory on bunnysoft.app is a launch channel. Revisit the entity only on a trigger: a paid signal or an outside party that needs the product separable. |

## Engineering gates

- [x] 014/020/022 landed. Second experiential pass (19:30): **not launch-ready this week; close** — happy path praised, four false statements in failure paths, two 014 regressions → ticket 020 (per-turn state, stream idle end, reply-end classification, one health source, tab leader). Third pass (20:20): desktop launch-ready, phone not (two false states) → ticket 022 (degraded self-probe, delivery-derived marks, honest wall copy, phone layout, durable Disconnect, revoked card keeps chats). 022 landed (`e2f0c3c`). **Fourth pass is the last (founder ruling 2026-09-02 22:50: "make the fourth pass the last; we've done enough iterations in this area").** Result (final, 00:10, both personas + synthesis): desktop launch-ready; phone **one blocker** (a re-issued invite orphans the friend's chats — conversations keyed to the invite id, not the host) + three screenshot-worthy frames → all five landed in ticket 024 (`d321368`, 01:15). Everything else → backlog (012). **Engineering gate closed.** Ranked once: launch-blocking → 024 cut to two items (context meter = what the next request carries; an oversized refused turn is dropped from history and the copy says so) + 023 (one dial per host); everything else → backlog (composer focus on mount, Escape closes sheets, returning card right after Disconnect, per-minute refill between polls, self-probe cap 10 s / probe on focus, empty-looking composer after a long paste, first ~15 s stale path pill after a kill). No fifth pass.
- [x] 017 release engineering: reproducible builds for the three platforms, notices file, version
      stamping, `make release-dry` from a fresh clone (law 4). Landed `f5e6081`.
- [~] Relay (F2): `derp.2185lab.com` is LIVE on the founder's droplet (Let's Encrypt, DERP + STUN verified with tailscale's client), a TEST host pinned to it measured equal to Tailscale NYC (docs/MEASURE.md); **the demo host has NOT been switched** — founder green light required. Relay admission (ticket 025) before anyone has a reason to look for the hostname.
- [ ] Web app deployed to F3's URL from the release commit; the invite link form verified end to end
      from a phone on cellular.
- [x] `docs/ARCHITECTURE.md` and README match the shipped flags (`serve --help` diffed against README; four flags were missing and were added, 18:40).
- [x] Full checks from a fresh clone re-run on the final main (`4fe3810`, 07:20): CHECK OK · wasm · 257 web tests · lint · build · 3 cross-compiles · notices OK (49 Go + 110 npm) · launch-check OK · 6 release archives. Earlier run (`647f62b`, 18:50): `make check` OK · wasm 6.19 MB gz · web build + 190 tests · darwin/linux/windows amd64 + linux arm64 · `notices: OK` · `make release-dry` 6 artifacts. One observation: `TestI6SettleTable` failed once in a verbose run under machine load and passed 7× after (ticket 019 makes it deterministic).
- [ ] Live proof on the published artifacts: install the release binary the way a stranger would
      (F4's path), serve DeepSeek V4 flash from the workstation, mint an invite, chat from a phone.
- [x] Protections re-read against the shipped build (18:55): `TestListenerServesPort80Only`, `TestOnTCPGate`, `TestProtection1Config` pass (tunnel exposes only port 80 → the gateway); the gateway `TestMain` gate asserts every error code was exercised and the secret never reached a log or body; 429/503 fixtures pass (I7/I8). Prompts not logged by default (`--log-prompts` per-run, disclosed to friends).

- [x] 030 launch readiness landed (05:00): web meta/OG/manifest/icons/About, README as landing page with a real chat GIF, GitHub description + 13 topics, SECURITY/CONTRIBUTING/CoC/issue forms/PR template, CI green on GitHub, release workflow (draft, tag-only), Homebrew cask generated and installed from a local tap, `docs/RENAME.md` inventory, `make launch-check`. Manual: social-preview upload (no API). Unsigned macOS binaries for now (034).

- [x] 028 load test landed (06:10): four layers, N to 30 (inference) / 100 (sessions), both engines, our relay vs Tailscale's; **no layer below launch-day need**; `docs/LIMITS.md` published; stateless stays; engine recommendation for the demo: vLLM, `--slots` = max-num-seqs, ctx ≥32K. Two defects (035 connect shutdown bound, 036 context pre-check) dispatched.

## Launch-day operations

- [ ] Host: the workstation serves with `--name`, `--web-url`, a self-hosted relay, and a handful of
      pre-minted keys with tight limits for strangers (`--rpm 6 --daily-tokens 50000`).
- [ ] `status` and `usage` watched during the post; a revoke rehearsed once.
- [ ] Support line: what to say when "my invite doesn't connect" (host asleep? rotated? relay down?) — and the one known limitation from 023: if a friend pressed Reconnect while the host was still restarting, the app heals by itself within ~40 s; a reload also fixes it.
- [ ] The post's claims come from `docs/MEASURE.md` and nothing else.

## After launch (already ticketed)

012 concept trim + cleanup · 015 capability seam + web search · sandboxes (founder-gated design).

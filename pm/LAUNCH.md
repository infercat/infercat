# Launch checklist — demo-1 public launch (drafted 2026-09-02 16:30)

The release theme: "paste a code, chat with a friend's GPU". Every line below is a gate; the founder
gates the public ones (BELIEFS: Authority). Nothing ships while a line in **Founder decisions** is open.

## Founder decisions (open)

| # | Decision | Why it blocks | PM recommendation |
|---|---|---|---|
| F1 | **Name** | Invite prefix, binary name, web title, social post all carry it; renaming after launch breaks copied invites | Decide before the post. "Bunny Network" is do-not-ship (bunny.net); "taillama" collides with Meta's Llama mark. |
| F2 | **Relay** | Public tailcat relays are rate-limited and revocable at any time | One DigitalOcean VM + hostname running `derper`; bake into the host key at generation (`--region`); zero Tailscale infra in the product. |
| F3 | **Web app URL** | `keys add` prints a link only when `product.WebURL` is set; strangers need a destination | Vercel (founder's choice); set `product.WebURL`; the app is a static bundle. |
| F4 | **Distribution** | Strangers need a download, not a Go toolchain | GitHub Releases (darwin/linux/windows) + a Homebrew tap; decide public repo vs private repo with public releases. |
| F5 | **License** | No LICENSE file; third-party notices are required by tailcat/tailscale (BSD-3) regardless | Decide before any public artifact; ship `THIRD_PARTY_NOTICES` with every binary (ticket 017). |
| F6 | **History** | A 28 MB binary sits in git history (`ff` commits before `e5b0a11`) | Rewrite once before any public mirror or collaborator clone; never after. |

## Engineering gates

- [ ] 014 web polish landed; second experiential pass (both personas) reports launch-ready or the
      remaining items are explicitly accepted by the founder.
- [x] 017 release engineering: reproducible builds for the three platforms, notices file, version
      stamping, `make release-dry` from a fresh clone (law 4). Landed `f5e6081`.
- [ ] Relay: host key regenerated against the founder's derper (F2); `docs/MEASURE.md` re-run through
      that relay; numbers in the post come from that run.
- [ ] Web app deployed to F3's URL from the release commit; the invite link form verified end to end
      from a phone on cellular.
- [x] `docs/ARCHITECTURE.md` and README match the shipped flags (`serve --help` diffed against README; four flags were missing and were added, 18:40).
- [ ] Full checks from a fresh clone: `make check`, `make wasm`, `make web`, cross-compiles, printed.
- [ ] Live proof on the published artifacts: install the release binary the way a stranger would
      (F4's path), serve DeepSeek V4 flash from the workstation, mint an invite, chat from a phone.
- [ ] Protections re-read against the shipped build: tunnel exposes only the gateway (001 review's
      probe re-run), secrets never logged, prompts not logged by default, bursts degrade to 429/503.

## Launch-day operations

- [ ] Host: the workstation serves with `--name`, `--web-url`, a self-hosted relay, and a handful of
      pre-minted keys with tight limits for strangers (`--rpm 6 --daily-tokens 50000`).
- [ ] `status` and `usage` watched during the post; a revoke rehearsed once.
- [ ] Support line: what to say when "my invite doesn't connect" (host asleep? rotated? relay down?).
- [ ] The post's claims come from `docs/MEASURE.md` and nothing else.

## After launch (already ticketed)

012 concept trim + cleanup · 015 capability seam + web search · sandboxes (founder-gated design).

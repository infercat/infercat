# HANDOFF — state of the work (succession document)

Updated: 2026-09-02 13:30. MILESTONE: founder ran the demo end to end and declared the concept proven. PM seat: Claude Fable, session on Max's laptop.

## What this is

Bunny Network (working name): share local inference with friends over a tailcat tunnel; browser-first
client; per-friend keys, limits, usage. Vision and Protections: `pm/BELIEFS.md`. Cuts: `pm/DECLINED.md`.
Seam contract: `docs/ARCHITECTURE.md`. Founder wants a working demo by the morning of 2026-09-02 (PT) and
a public social-media launch soon after, likely serving DeepSeek v4 flash from the RTX Pro workstation.

## Environment facts (verified 2026-09-02 ~02:00)

- Laptop: M5 Max 64 GB, Go 1.26 + auto toolchain 1.27, Node 22, pnpm 11, Homebrew tailcat 0.4.0, gh auth OK.
- Test upstream: llama-server b9553 (BunnyKit build) running on `127.0.0.1:18080` with
  `~/.cache/bunny-network/models/gemma-4-E2B-it-Q4_K_M.gguf`, `-np 2 -c 8192`. Log at
  `~/.cache/bunny-network/llama-server.log`. Restart command in `docs/MEASURE.md`.
- vLLM 0.25 on the workstation (`ssh max@max-ws.lab`), Docker `vllm_entropy`, loopback `:8010`, model
  `entropy-v2-gemma4-12b-w4a16-group128`, 2 seqs, ctx 8192. **Read-only** — founder's instruction. Reach
  via `ssh -N -L 8010:127.0.0.1:8010 max@max-ws.lab`. Port 8080 there is Open WebUI; 4000 is LiteLLM (key).
- tailcat v0.4.0 source clone for reference: `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/tailcat`.
- HF_TOKEN exists in kb dotenvx; not needed (unsloth GGUF is ungated).

## In flight

- **Landed on main (`8a3a3c4`):** 001–004 (tunnel/invite/wasm · gateway · CLI/store/upstream/usage/admin
  with wiring flipped · web client). A stray 28 MB binary committed at the root during the flip was
  removed at `e5b0a11`; it remains in history (private repo) — rewrite before any public mirror.
- **Reviews done:** Claude adversarial workflows on 001 (36 agents), 002 (55), 003 (46); second-model
  review (gpt-5.6-sol via ultracodex, 4 lenses, 39 findings) on integrated main. Summaries and rulings
  are appended to each ticket; confirmed defects became tickets 005 (10a–10n), 006, 007.
- **Running:** 005 integration (Fable): fixes 10a–10n, end-to-end proof Direct + Tunnel via Playwright,
  vLLM run, `hack/measure.sh` numbers. 006 gateway hardening (Fable): alias bypass, admission order,
  write/read deadlines, bounded queue, metered /v1/models, no redirects, audit key_id, upstream 4xx,
  `/me.host.log_prompts`. 007 web hardening (Opus): truthful stream ends, session leaks, degraded
  states, host-scoped storage, revoked mid-session, abort-during-dial, IME, two tabs, log-prompts copy.
- **Landing order:** 005 → 006 (rebase over 005's proxy.go edits) → 007 → experience review of the
  integrated build (fresh Opus, Playwright) → founder demo instructions + morning report.

## Standing decisions tonight

- Product name stays "Bunny Network" in constants only; founder renames after using it.
- Public tailcat relays for the demo; self-hosted derper on a DigitalOcean VM before publicity (founder).
- Web hosting out of scope tonight (founder leaning Vercel).
- No LICENSE file yet: private commercial repo; founder decides the license before any public artifact.
  Third-party notices (BSD-3 tailcat/tailscale, MIT wireguard-go, Apache-2 gVisor…) must ship with binaries.

## Next actions for whoever resumes

1. Check branch status: `git -C ~/Desktop/repos/2185Lab/bunny-network branch -a` and each ticket's Report.
2. Land in order; run the printed checks; never chain verify && push.
3. After 003 lands: run `bunny-network serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9090`,
   mint a key, run the web app in Direct mode, then in Tunnel mode. Record numbers in `docs/MEASURE.md`.
4. Write the founder's morning report: what is verified, what is not, measured latency, screenshots.

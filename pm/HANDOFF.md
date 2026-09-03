# HANDOFF — state of the work (succession document)

Updated: 2026-09-03 01:50 EDT (resume point; session-limit pause expected; alarm 03:15). Engineering launch gate closed 2026-09-03 01:15. PM seat: Claude Fable, session on Max's laptop.

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

## In flight (resume point written 2026-09-03 ~01:50 EDT, before a session-limit pause; alarm 03:15)

- **Engineering launch gate: closed** (024 landed). Nothing product-side is running.
- **Running when paused:** naming round 2 workflow `wf_9f974100-bcd` (descriptive names from the
  audience's vocabulary; generate → per-candidate collision check → synthesis) and an Opus research
  agent on LM Link market vocabulary. On resume: read both journals; if killed, resume the workflow from
  cache (`Workflow({scriptPath, resumeFromRunId})`); the founder wants a recommendation in the
  audience's words (see docs/MARKET-LMLINK.md), not the Guestroom-style list (docs/NAME.md).
- **Relay (droplet 206.189.207.168, `derp-server-1`, Ubuntu 24.04, ssh as root with this Mac's key):**
  derper built at /usr/local/bin/derper; systemd unit `derper.service` written for
  `derp.2185lab.com` (:443 LE, :80, STUN 3478), NOT enabled; ufw rules for 22/80/443/3478 staged, NOT
  enabled. **Blocked on the founder saying "apply"** for the Cloudflare A record `derp → 206.189.207.168`
  (unproxied, TTL 300) — wrangler 4.128 installed and authenticated via CLOUDFLARE_API_TOKEN in the
  env; the exact call is in ~/.claude/jobs/12b4a99c/tmp/cf/dns-change.json. After apply: enable ufw,
  start derper, confirm the cert, generate a TEST host key pinned to the relay in a throwaway data dir,
  run hack/measure.sh through it vs Tailscale's public relay. **Founder ruling: do not switch the demo
  host to our relay without an explicit green light** — Tailscale's own relays may be the better
  option; the business case is open (their blog invites this use).
- **Founder decisions still open (pm/LAUNCH.md):** F1 name (round 2 pending), F3 web URL (Vercel;
  domain with the name), F4 distribution (recommend public repo + Releases + brew tap), F5 license,
  F6 history rewrite (needed if public). F2 relay = the test above.
- **Two Reddit tabs may still be open in the founder's Chrome** from the LM Link read; harmless.
- **Rate-limit protocol:** commit worktrees as WIP, push, resume the same agent with a pointer.

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

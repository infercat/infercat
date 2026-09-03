# HANDOFF — state of the work (succession document)

Updated: 2026-09-03 01:20 EDT (machine clock). MILESTONE 2026-09-02 13:30: founder ran the demo and declared the concept proven. Engineering launch gate closed 2026-09-03 01:15. PM seat: Claude Fable, session on Max's laptop.

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

- **Engineering side of the launch is done** (2026-09-03 01:15). Main `HEAD` = 024 landed. Landed today:
  001–011, 014, 016–024. Four experiential passes; the fourth was the last by founder ruling; its
  blocker (chats keyed to the invite id) and three screenshot-worthy frames are fixed in 024; the rest
  is on the 012 backlog. Fresh-clone gate, protections, docs/flags, release engineering: all evidenced
  in `pm/LAUNCH.md`.
- **Nothing is running.** No engineer, no workflow, no host processes of ours (the founder's llama-server
  on 18080 stays up).
- **Waiting on the founder (pm/LAUNCH.md F1–F6):** name · relay VM (derper) · web app URL · distribution
  · license file · history rewrite. After F2/F3: regenerate the host key against the derper, set
  `product.WebURL`, re-run `docs/MEASURE.md` through the real relay, deploy the bundle, live proof from a
  phone on cellular (checklist "Engineering gates" lines 3, 4, 7).
- **After launch:** 012 concept trim + cleanup (+ the fourth-pass backlog), 015 capability seam + web
  search, sandboxes (Docker-class, founder-gated).
- **Rate-limit protocol:** commit the worktree as WIP, push, resume the same agent with a pointer. Four
  kills today; the fourth cost ~40 min. Engineers also tend to yield while waiting on their own
  background jobs — the PM's tree-quiet watcher (5 min of no changes, or a non-WIP commit) is the cheap
  way to know when to gate.

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

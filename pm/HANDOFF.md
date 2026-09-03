# HANDOFF — state of the work (succession document)

Updated: 2026-09-02 22:20 EDT (machine clock). MILESTONE 13:30: founder ran the demo end to end and declared the concept proven; theme since: polish toward a public launch. PM seat: Claude Fable, session on Max's laptop.

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

- **Landed on main (`150e558`):** tickets 001–011, 014, 016–022 and the 022 evidence follow-up.
  Highlights since the demo: gateway request pipeline + settle table + FIFO queue + engine-side
  deadlines (006/010/021), engine state + `Engine` seam (011), web session/message state machines
  (007) and three truth rounds (014/020/022), busy-host keepalives (018), host polish incl. the
  first-run identity race (009), seeded counters (016), release engineering with notices (017),
  `docs/DESIGN.md` (008), `docs/ARCHITECTURE.md` v1, `pm/LAUNCH.md`.
- **Running:** 023 one dial per host (Reconnect joins the self-probe's dial) and 024 context meter
  truth + five desktop items — same Fable engineer, branch `t023-one-dial`. Fourth experience pass:
  desktop spot-check done (verdict: launch-ready but for the context meter); phone half + synthesis
  re-running from cache after the fourth rate-limit kill (`wf_dc0cce6d-452`).
- **Gate:** the phone half of the fourth pass (or a fifth pass after 023/024) reports no false state
  and no embarrassing screenshot → engineering side done. Founder decisions F1–F6 in `pm/LAUNCH.md`
  are the other half.
- **Rate-limit protocol:** on every kill, commit the worktree as WIP on its branch, push, resume the
  same agent with a pointer to the commit. Four kills today (05:40, 15:30, 19:30, 22:20 resets).
- **After launch:** 012 concept trim + cleanup, 015 capability seam + web search (vision reframed to
  "share an AI"), sandboxes (Docker-class, founder-gated).

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

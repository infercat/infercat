# HANDOFF — state of the work (succession document)

Updated: 2026-09-02 10:15 (morning after build night 1). PM seat: Claude Fable, session on Max's laptop.

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

- **Landed on main (`e6695c0`):** 001 tunnel/invite/wasm · 002 gateway · 003 CLI/store/upstream/usage/admin
  (wiring flipped, stub deleted) · 004 web client. `go build ./cmd/bunny-network` is the real host;
  `make wasm && cd web && pnpm build` is the real client.
- **Incident 04:40–09:55:** the account's session rate limit killed every running agent (004 engineer
  after its report, 005 engineer at start, most review finders/refuters). Nothing ran until the founder
  said "continue" at 09:55. 004 was committed by the PM from its worktree after re-running its checks.
- **Running now:** 005 integration (Fable engineer; fixes 10a–f, end-to-end proof in Direct and Tunnel
  mode via Playwright, vLLM run, `hack/measure.sh` numbers). Review workflows for 001/002/003 resumed
  (cached results replay; failed agents re-run).
- **Review findings so far:** 001 exposure lens — no defects, empirical proof that only tunnel port 80
  reaches the gateway; invite parsing — no defects. 003 secrets/permissions — no defects; the
  "friend's secret forwarded to upstream" claim is refuted by code (`proxy.go` builds a fresh request).
  Low: host-key temp path (005 fix 10f).
- **Next:** land 005 → experience review of the integrated build (fresh Opus, Playwright) → founder
  morning report with the `docs/MEASURE.md` numbers and the how-to-run block.

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

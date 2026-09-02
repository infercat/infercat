# BELIEFS — Bunny Network (working name)

Founder: Max (Yuanping Song). PM seat: Claude (Fable), appointed 2026-09-02.
Repo: github.com/2185Lab/bunny-network (private). Name not locked; rename expected after first use.

## Vision (stated back to the founder 2026-09-02, confirmed)

Anyone with a GPU can share their inference with anyone who has a browser, by handing them one
code. The friend needs no account, no VPN, no install. The host keeps control through per-person
keys, limits, and usage. Traffic is end-to-end encrypted through a relay the host can self-host,
and becomes direct peer-to-peer when tailcat's WebRTC transport lands (tailcat issue #4).

**Differentiation.** Versus Tailscale + Ollama node sharing: the friend does nothing but paste.
Versus ngrok / Cloudflare tunnels: not plaintext through a vendor, self-hostable relay, and a
direct path later. Versus LiteLLM-style gateways: the network is built in.

**Intended user.** Host: a person with a strong machine (Mac, Linux, Windows) already running
llama.cpp, vLLM, Ollama, or LM Studio. Friend: browser-first (ChatGPT-shaped expectations); power
users get a desktop client later.

**Non-goals (all versions until re-decided).** Stranger marketplace / credits. Distributed
inference or model sharding. General VPN. Model download / management. Multi-host routing.

## Commercial framing (founder, 2026-09-02)

This is the first step toward a commercial product. The demo launches publicly on social media as
soon as it is real. Polish standard = "a stranger pastes a code and says wow", not "works on my
machine". Public demo host: founder's workstation serving DeepSeek v4 flash on two RTX Pro 6000s
via vLLM.

## The riskiest promise

Streaming chat through a **relayed** browser tunnel feels good enough that a stranger says "wow"
rather than "laggy". Browser traffic is DERP-relayed until tailcat ships WebRTC. The first build
measures this for real (TTFT, tokens/s through the relay vs direct localhost). If it fails, the
demo still works but the launch pitch changes to "native client for now".

## Taste rules (each with its reason)

- **The friend's path is one paste.** Reason: this is the entire differentiation; every extra step
  is Tailscale node sharing again.
- **Surfaces tell the truth.** The UI shows "relayed via sfo · 84 ms" not a green dot. Degraded
  states show as degraded with a reason. Reason: the launch audience is technical and will test it.
- **Host stays in control.** Every key has limits by default; nothing is unlimited unless the host
  says so. Reason: strangers will hold invites after the social launch.
- **One concept per thing.** Invite = address + key. Key = person. Limits live on the key. No
  groups, roles, or plans in v1. Reason: concept budget; simplicity is the product.
- **Product name lives in one constant** (Go and TS). Reason: rename is expected.
- **Compatibility is off** until first release: break wire formats and schemas freely. The invite
  format carries a version prefix (`bn1`) so a later client can parse an older invite; nothing else
  is versioned.
- **Prompts are never logged by default.** Reason: friends' conversations are theirs; the host sees
  counts, not content. `--log-prompts` exists for debugging and says so loudly.

## Protections (a proposed safety measure must cite a line here or go to the backlog)

1. **The host machine.** The tunnel exposes exactly one thing: the gateway. Never localhost at
   large, never a SOCKS/exit-node mode, never the admin API.
2. **Invite secrets.** Stored hashed on the host; shown once at creation; never logged.
3. **Friends' usage data.** Counts and timings only; no prompt or completion content unless opted in.
4. **The upstream engine.** Concurrency and queue caps sized to its slots so a burst degrades to
   429/503 with Retry-After rather than OOM or a stalled engine.

Deliberately not protected in v1: key sharing between two people holding one invite (bounded by
per-key limits, visible in usage); relay operator seeing ciphertext metadata (timing, sizes).

## Engineering laws in force

The six design laws from the aipm skill apply. Specific to this project:
- Verification is never inferred from a chained exit code; print the result.
- "Green from nothing": the wasm build and the Go build must pass in a fresh clone.
- Every published number (latency, tokens/s) comes from a command in `docs/MEASURE.md`.

## Size calibration (source lines, tests excluded; set 2026-09-02, uncalibrated by history)

1 ≈ ≤150 · 2 ≈ ≤400 · 3 ≈ ≤900 · 5 ≈ ≤2000 · 8 = negotiated. Concept budget per rung: 0 / 1 / 3 /
6 / negotiated.

## Authority

Founder gates: anything public (publishing the web app, social posts, DNS, buying a VPS), the
product name, pricing. PM lands every change to main. Pre-release: PM commits to main freely.

## Checks

- Go: `go build ./... && go vet ./... && go test ./...` (fast, seconds).
- Web: `cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm test && pnpm build`.
- wasm: `make wasm` (builds `web/public/bunny.wasm`; ~1–2 min).
- Full pre-release: all of the above from a fresh clone + `docs/MEASURE.md` + a live end-to-end run.

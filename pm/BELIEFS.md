# BELIEFS — Bunny Network (working name)

Founder: Max (Yuanping Song). PM seat: Claude (Fable), appointed 2026-09-02.
Repo: github.com/2185Lab/bunny-network (private). Name not locked; rename expected after first use.

## Vision (stated back to the founder 2026-09-02, confirmed; reframed the same evening — founder ruling)

Anyone with a capable computer can share **an AI** with anyone who has a browser, by handing them one
code. An AI is a model plus the capabilities the host chooses to attach — search with the host's own
subscription, a sandbox on the host's machine, documents, later browsing — under a budget. The friend
needs no account, no VPN, no install. The host keeps control through per-person keys, limits, and
usage, and shares capabilities **by name**: each one opted in per key, budgeted, isolated, and visible in
usage. Traffic is end-to-end encrypted through a relay the host can self-host, and becomes direct
peer-to-peer when tailcat's WebRTC transport lands (tailcat issue #4).

*Reason for the reframe (founder, 2026-09-02):* "people don't really care whether they are strictly
sharing LLM inference or the more general idea of AI. In 2026 you cannot say we have AI but no tools."
Inference sharing was the technical statement of the idea; the demo proved it; the product is the AI.

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

## The riskiest promise — RESOLVED 2026-09-02

Streaming chat through a **relayed** browser tunnel feels good enough that a stranger says "wow"
rather than "laggy". Measured (docs/MEASURE.md): 161 tok/s through the New York relay vs 168 direct,
browser TTFT 110–160 ms. **Founder verdict after using the demo: "the concept is decisively proven;
streaming speed is more than sufficient."** Browser traffic stays relay-only until tailcat ships WebRTC;
that is now a cost question (self-hosted relay), not an existence question.

## Next theme (founder, 2026-09-02): polish

"We still can make some polish around the user experience (both the CLI setup and the web app)."
Polish is judged experientially — a stranger's first ten minutes on each side — not by feature count.

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
- **Two reach paths, one product; the relay path is benchmarked against ngrok.** Reason (founder,
  2026-09-03): a public OpenAI-compatible endpoint per key (the ngrok shape) is the least-resistance path
  for clients we do not control, so it will exist; when it does, "we are a specialized ngrok for AI
  APIs and must do this one thing much better than the general version." The bar, measured by a
  stranger from a fresh machine on both: install-to-working-invite time; per-friend keys, limits, and
  meters on the same command; streaming and minutes-long requests as first-class (queued keepalives,
  idle deadlines, settle table); truthful failure states; a stable per-host address with no
  interstitial. **The direct (p2p) path stays the differentiator and the destination:** the relay sees
  ciphertext, and direct traffic costs us nothing per byte while relayed traffic scales with our bill —
  the product always nudges toward direct as clients grow. Pricing follows that economics.
- **The relay is never metered or charged per byte.** Reason (founder, 2026-09-03): the audience runs
  its own models to avoid paying per use; a metered connection "isn't the vibe of our product" — there
  are other ways to monetize, this is not one. Relay abuse is prevented by admission (only registered
  host keys are relayed, tier 1), not by counting. Consequence: relay cost is a cost of the product,
  bounded by nudging traffic to the direct path, not recovered from hosts.
- **The client runs the agent loop; the host runs tools.** Reason (founder, 2026-09-02): each client may
  carry a different agent implementation, and the host cannot and should not dictate it. The host's job
  is capabilities behind the gateway (metered tool routes), never orchestration. Consequence: tool calls
  pass through the gateway untouched (they do), `/me` advertises capabilities, keys carry a tools
  allowlist.
- **Host safety is paramount: Docker-class isolation or nothing.** Reason (founder, 2026-09-02): a host
  shares their own machine with strangers; a lightweight sandbox is not a sandbox. Host-side execution
  ships only with container-grade isolation, per-friend workspaces, and CPU/time budgets. Post-demo.
- **DeepSeek Harness is a catalogue, not a runtime.** Reason: researched 2026-09-02 (pm/DECLINED.md) —
  wrong shape for a static client and a one-binary host, but its ~250 packages and the community's tools
  are a large, high-quality menu of tool implementations and loop designs to borrow from, one at a time,
  under our own seams.
- **Iteration has a stop.** Reason (founder, 2026-09-02): four experiential passes on the web client were enough; "make the fourth pass the last." A review loop that keeps finding smaller items is a cost, not a safeguard; the PM ends it when the remaining items are ordinary polish a launch can carry, and says so.
- **Fix classes, not instances.** When a review finds three or more defects with one cause, the fix is
  the missing structure (a pipeline with one exit, a state machine, a stateful upstream), never N
  patches. Reason (founder, 2026-09-02): the demo-1 reviews produced ~30 confirmed defects that cluster
  into four causes; tickets 006/007 were first written as patch lists and re-briefed as designs. Software
  that grows by "one more check" becomes brittle; a great product gets simpler after release.

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
6 / negotiated. Stylesheets, help text, and per-platform fallbacks are surfaces, not source (rulings on
003 and 004, 2026-09-02): price them by surface count, exclude them from the line ceiling.

## Authority

Founder gates: anything public (publishing the web app, social posts, DNS, buying a VPS), the
product name, pricing. PM lands every change to main. Pre-release: PM commits to main freely.

## Checks

- Go: `go build ./... && go vet ./... && go test ./...` (fast, seconds).
- Web: `cd web && pnpm install --frozen-lockfile && pnpm typecheck && pnpm test && pnpm build`.
- wasm: `make wasm` (builds `web/public/bunny.wasm`; ~1–2 min).
- Full pre-release: all of the above from a fresh clone + `docs/MEASURE.md` + a live end-to-end run.

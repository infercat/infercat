# Principles

What the code cites when it says "because". These are the product's rules, each with its reason;
the code comments point here. A proposed safety measure must cite a line under Protections or it
goes to the backlog.

## Product

- **The friend's path is one paste.** One code, one field, one button. Every extra step is another
  product.
- **Surfaces tell the truth.** The app shows "relayed via New York · 84 ms", never a green dot.
  Degraded states show as degraded, with the reason. The audience is technical and will test it.
- **The host stays in control.** Every key has limits by default; nothing is unlimited unless the
  host says so.
- **One concept per thing.** An invite is an address plus a key. A key is a person. Limits live on
  the key. No groups, roles or plans.
- **The product name lives in one constant** (Go: `internal/product`; TS: `web/src/product.ts`). Copy tables (`web/src/i18n/*`) and docs may carry the name as text; the rename runbook greps them.
- **Prompts are never logged by default.** Friends' conversations are theirs; the host sees counts,
  not content. `--log-prompts` exists for debugging and says so loudly, to the host and to friends.
- **The relay is never metered or charged per byte.** People run their own models to avoid paying
  per use. Relay abuse is prevented by admission, not by counting.
- **The direct path is the destination.** The relay sees only ciphertext; direct traffic costs
  nobody anything. Clients go direct whenever the networks allow (the browser cannot yet).
- **The client runs the agent loop; the host runs tools.** The host's job is capabilities behind
  the gateway, never orchestration. Tool calls pass through untouched.
- **Host safety is paramount: container-grade isolation or nothing.** A host shares their own
  machine with strangers; a lightweight sandbox is not a sandbox.
- **Iteration has a stop.** A review loop that keeps finding smaller items is a cost, not a gain.
- **Fix classes, not instances.** Three defects with one cause get the missing structure — a
  pipeline with one exit, a state machine, a stateful upstream — never three patches. Software that
  grows by "one more check" becomes brittle; a good product gets simpler after release.
- **MIT, everything.** For an audience that reads code, a license it recognizes is part of the
  trust argument.

## Protections

1. **The host machine.** The tunnel exposes exactly one thing: the gateway. Never localhost at
   large, never a SOCKS or exit-node mode, never the admin socket.
2. **Invite secrets.** Stored hashed on the host; shown once at creation; never logged.
3. **Friends' usage data.** Counts and timings only; no prompt or completion content unless the host
   opted in with `--log-prompts`, which the app discloses.
   Chat history stays on your device. The host stores job and run inputs, outputs and trajectories under your key until they expire.
4. **The upstream engine.** Concurrency and queue caps sized to its slots, so a burst degrades to
   429/503 with Retry-After rather than an out-of-memory engine.

Deliberately not protected: two people sharing one invite (bounded by that key's limits and
visible in usage); the relay operator seeing ciphertext metadata (timing, sizes).

## Engineering

- Verification is never inferred from a chained exit code; print the result.
- "Green from nothing": the Go build, the wasm build and the web build pass in a fresh clone
  (`make check`; the CI workflow runs exactly that).
- Every published number (latency, tokens per second, limits) comes from a command in
  `docs/MEASURE.md`.
- Compatibility shims are not written. Before the first release, wire formats and schemas change
  freely; the invite format carries a version prefix so a newer client can parse an older invite,
  and nothing else is versioned. After release: migrate forward once, delete the old path.

## Decisions kept

- **No keep-alive pooling in the browser transport.** One TCP connection per request over the
  tunnel is correct and simple; the WireGuard session persists, so a new dial is cheap.
- **Our own wasm bridge, not tailcat's stock per-dial client.** The stock bridge creates a new
  client and relay handshake per dial; ours keeps one session and dials many times.
- **The browser is relay-only until tailcat's WebRTC transport ships.** Stated in the app's path
  line rather than hidden.
- **Binaries are unsigned for now** (no Apple Developer ID yet); Homebrew and Gatekeeper quarantine
  what they download, and the README says how to run it.

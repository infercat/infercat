# Changelog

Versions are tags (`v0.1.0`); the binary prints its own with `bunny-network version` and the web
app shows the same one under Settings. Dates are the tag's.

## v0.1.0 — unreleased

The first release: what the demo proved, made into something a stranger can install.

**What ships**

- One binary for macOS (arm64, amd64), Linux (amd64, arm64) and Windows (amd64): `serve` in front of
  llama.cpp, vLLM, Ollama or LM Studio (found by port, or `--upstream`), `keys` to mint and manage
  one key per friend, `status` and `usage` for what is happening and what happened.
- The web app, as a static bundle (`web-<version>.zip`) any file server can host: paste an invite,
  chat. It shows the path it is on (`relayed via nyc · 64 ms`), the model, and the usage against the
  limits; conversations stay in the browser.
- Invites: `bn1.<host address>.<secret>`, or a link when the host serves with `--web-url`. Shown
  once; stored hashed. Pause, resume, rotate, revoke, and per-key limits (requests and tokens per
  minute, concurrency, output and context ceilings, tokens per day, a model allowlist) take effect on
  the running host without a restart.
- The tunnel: [tailcat](https://github.com/tailscale/tailcat) — WireGuard end to end, relayed through
  a DERP relay the host can point at their own (`--derpmap-url`). Exactly two routes are reachable
  through it: `/v1/models` and `/v1/chat/completions` (`/v1/embeddings` when the engine has it).
- Limits that hold: a burst beyond the engine's slots queues briefly, then answers `429`/`503` with
  `Retry-After`; streams that stall are ended with a reason, not left open.
- Reproducible builds (`-trimpath`, stripped), a checksums file, third-party notices in every
  archive, MIT.

**Known limitations**

- Browser traffic is always relayed: the browser cannot hole-punch, so the direct path waits on the
  tunnel library's WebRTC transport. Measured cost: ~4 % of throughput and one relay round trip on
  time-to-first-token (docs/MEASURE.md).
- The macOS binaries are not signed or notarized yet: the first run needs a right-click → Open, or
  `xattr -d com.apple.quarantine` (README, "macOS says it cannot verify the developer").
- One host, one engine. No model download, no multi-host routing, no accounts, no marketplace — by
  design (pm/BELIEFS.md, non-goals).
- The public relay in the default map is rate-limited and revocable; a self-hosted relay is the
  supported setting for anything beyond a demo.
- `--log-prompts` is per-run and disclosed to friends in the app; there is no per-key logging.

**Thanks**

To Tailscale, for open-sourcing [tailcat](https://github.com/tailscale/tailcat) and the DERP relay
protocol this is built on, and to the llama.cpp, vLLM, Ollama and LM Studio projects for the engines
it stands in front of. Third-party licences: `THIRD_PARTY_NOTICES.md`.

# DECLINED — what we chose not to build, and why

Anyone implementing a declined item must address its recorded reason.

| Date | Item | Reason |
|---|---|---|
| 2026-09-02 | Per-client tailcat keys / allowlist enrollment as the auth model | Wrong direction: the client enrolls instead of the host minting. Composite invite (tailcat address + gateway key) gives host-side minting and revocation with one paste. Allowlist stays available for a later "device identity" feature. |
| 2026-09-02 | One tailcat server per friend (network-level revocation) | Multiplies relay connections and memory per friend; gateway-key revocation is the same user outcome. Revisit only if key sharing becomes a real abuse vector. |
| 2026-09-02 | WebRTC-only browser transport (skip WireGuard in the browser) | Direct P2P today and tiny download, but needs our own signaling + STUN + TURN and a second protocol on the host. Deferred: tailcat's WebRTC transport (issue #4) gives the same outcome without a second protocol. Reconsider if #4 stalls past launch. |
| 2026-09-02 | Embedding LiteLLM as the gateway | MIT/enterprise boundary is disputed (litellm issue #34241); heavy Python dependency in a Go host. A few hundred lines of Go reverse proxy covers per-key limits and usage. |
| 2026-09-02 | Web hosting choice (Vercel vs Pages) tonight | Founder: out of scope for the first build; localhost + built static bundle suffice. |
| 2026-09-02 | Native desktop client | Browser-first demo. The Go host binary already contains everything a native client needs; a `connect` subcommand exposing localhost is a later ticket, not v1. |
| 2026-09-02 | Model download / management in the host | Non-goal. Hosts already run llama.cpp/vLLM/Ollama/LM Studio; we point at them. |
| 2026-09-02 | Keep-alive connection pooling in the browser HTTP client | One tunnel TCP connection per request is correct and simple; the WireGuard session persists so a new TCP dial is cheap. Optimize only if measured. |
| 2026-09-02 | Prompt/completion logging on by default | Protection 3. Opt-in flag only. |
| 2026-09-02 | Admin API over TCP | Protection 1. Unix socket in the data dir, read-only status. Key edits are file edits with hot reload. |
| 2026-09-02 | Using tailcat's stock wasm bridge (`tailcatDial`) | It creates a new client + DERP handshake per dial (60 s timeout path). Our bridge keeps one session and dials many times. |

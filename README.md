# Bunny Network (working name)

Share your local inference with friends. The host runs one binary in front of llama.cpp, vLLM, Ollama,
or LM Studio and mints invites; a friend pastes the invite into the web app and chats over an
end-to-end encrypted tunnel (tailcat: Tailscale's data plane, no accounts). The host keeps per-friend
limits and usage.

Status: pre-release build. See `pm/BELIEFS.md` for the vision, `docs/ARCHITECTURE.md` for the seam
contract, `pm/tickets/` for the work.

```
make check      # go vet + go test
make build      # bin/bunny-network
make wasm       # web/public/bunny.wasm (+ wasm_exec.js)
make web        # web/dist
```

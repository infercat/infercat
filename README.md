# Bunny Network (working name)

Share your local LLM with friends. You run one binary in front of the inference server you already
have (llama.cpp, vLLM, Ollama, LM Studio) and hand each friend an invite code. They paste it into a
web page and chat with your model over an end-to-end encrypted tunnel — no account, no VPN, nothing
to install. You keep per-friend limits and see usage counts, never their conversations.

The tunnel is [tailcat](https://github.com/tailscale/tailcat), Tailscale's open-source data plane
without the control plane. Traffic is WireGuard-encrypted end to end and relayed through a DERP relay
(the browser cannot hole-punch yet); the relay sees ciphertext only.

Status: pre-release. Licensed MIT (see LICENSE); third-party notices in THIRD_PARTY_NOTICES.md. Design records in `pm/`, the seam contract in `docs/ARCHITECTURE.md`,
the design review in `docs/DESIGN.md`, measured numbers in `docs/MEASURE.md`.

## Quickstart (host)

You need Go 1.22+ to build (the right toolchain downloads itself). Your friends need only a browser.

```
make build
bin/bunny-network serve --name "Max's laptop"     # finds llama.cpp, Ollama, LM Studio or vLLM
bin/bunny-network keys add alice                  # prints alice's invite once (and a QR)
```

If your engine is on a non-default port or another machine:

```
bin/bunny-network serve --upstream http://127.0.0.1:18080
```

Flags you pass to `serve` are remembered in `config.json`, so the next `serve` needs none.

| `serve` flag | What it does |
|---|---|
| `--upstream URL` / `--upstream-key TOKEN` | your inference server (detected when absent; `--upstream auto` forgets a remembered one) |
| `--name NAME` | the host name your friends see (default: this machine's hostname) |
| `--web-url URL` | where friends open the web app; invites then print as a link |
| `--slots N` | parallel requests the engine can serve (0 = ask the engine) |
| `--region NAME` / `--derpmap-url URL` | preferred relay region / a self-hosted relay map |
| `--dev-listen ADDR` | also serve on loopback with permissive CORS, for web development |
| `--log-prompts`, `--ephemeral`, `--verbose` | per-run: log message content; throwaway host identity; tunnel log on the terminal |

What friends can reach through the tunnel: exactly `/v1/models`, `/v1/chat/completions` and
`/v1/embeddings` on your upstream — nothing else on your machine, no other port, no files.

Manage friends: `keys list` · `keys pause alice` (she gets 403 until `keys resume`) · `keys revoke alice`
(permanent; she needs a new invite) · `keys rotate alice` (new invite, old one stops) ·
`keys limits alice --rpm 30 --daily-tokens 500000 --max-output-tokens 8192`. Watch: `status` (live) and `usage` (history).

## Quickstart (friend)

Open the web app, paste the invite, chat. Until the app is hosted, the host serves it locally:

```
make web                                                             # builds the wasm too
python3 -m http.server 59080 --directory web/dist --bind 127.0.0.1   # any static server works
```

The header shows the path (`relayed via nyc · 64 ms`), the model, and your usage against the limits.

## Quickstart (friend with an app)

No browser needed: the same binary turns an invite into a local OpenAI-compatible endpoint, so
Open WebUI, Cursor, Claude Code, the OpenAI SDKs or plain `curl` use your friend's model as if it
were local. Between two machines the path goes direct once they find each other; the relay is
only the rendezvous.

```
bin/bunny-network connect bn1.tc….…          # paste the invite
```
```
Bunny Network 0.0.1-dev
host      Max's laptop  ·  gemma-4-E2B-it-Q4_K_M.gguf
path      relayed via New York City · 27 ms       # re-checked every 30 s, printed when it changes
local     http://127.0.0.1:11435
          set your app's base URL to http://127.0.0.1:11435/v1, any API key
```

The invite's key is added to every request; the app's own API key is ignored. `/v1/*` and `/me` are
forwarded, nothing else. Errors keep the host's status and code and say what to do in your words
(paused, revoked, asleep, rate limited with `Retry-After`, busy); when the host stops answering,
`connect` says so and reconnects on its own.

```
OPENAI_BASE_URL=http://127.0.0.1:11435/v1 OPENAI_API_KEY=x python3 -c '
from openai import OpenAI; c = OpenAI()
for e in c.chat.completions.create(model=c.models.list().data[0].id, messages=[{"role":"user","content":"hi"}], stream=True):
    print(e.choices[0].delta.content or "", end="", flush=True)'
```

## Data directory

`~/Library/Application Support/bunny-network` (macOS), `~/.config/bunny-network` (Linux),
`%AppData%\bunny-network` (Windows), or `--data-dir`:

| File | What it is |
|---|---|
| `host.key.json` | Your host identity. Back it up; do not sync it; deleting it invalidates every invite you sent. |
| `keys.json` | Friends' keys as hashes (never the secret), their status and limits. |
| `usage.jsonl` | One line per request: key, endpoint, status, token counts, timings. No prompt content unless you run `serve --log-prompts`. |
| `config.json` | Remembered `serve` flags. |
| `tunnel.log` | The tunnel engine's log (`serve --verbose` prints it instead). |

## Building

```
make check      # go vet + go test
make build      # bin/bunny-network
make wasm       # web/public/bunny.wasm (+ wasm_exec.js), needed by the web app
make web        # web/dist (builds the wasm first)
make notices    # regenerate THIRD_PARTY_NOTICES.md; make notices-check verifies it
make release-dry  # goreleaser snapshot for darwin/linux/windows into dist/ (publishes nothing)
```

Every binary and archive ships `THIRD_PARTY_NOTICES.md`; `bunny-network version` prints the stamped
version, commit, and date, and the web app shows the same version.

Cross-compile: `GOOS=linux GOARCH=amd64 go build -o bunny-network-linux ./cmd/bunny-network` (same for
`windows`).

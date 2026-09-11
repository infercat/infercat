# Changelog

Versions are tags (`v0.1.0`); the binary prints its own with `infercat version` and the web
app shows the same one under Settings. Dates are the tag's.

## v0.1.4 — 2026-09-13

Two days after 0.1.3: friends can ask a host for pictures, third-party coding clients can talk to a
host through the Responses API, a host can describe its whole loadout as a profile and check it,
and the console shows what the host keeps for each friend. Every slice was gated, reviewed and
proved against a real engine before it landed. Landed after this cut (the agent route, one-command
setup, host tools in chat) ships in 0.1.5.

**Friends (the chat app)**

- **Pictures.** A friend asks for an image and the host draws it with its image engine
  (stable-diffusion.cpp). Image jobs are durable per key: they queue, survive a host restart without
  re-running, show their place in the queue, and finish in place; the reply gets an image row and the
  Images sheet collects a friend's pictures. Outputs are kept per key within a bounded budget. The
  gallery loads a grid of any size four reads at a time with a polite retry when the host is busy.
  (144a, 144b, 144c, 155)
- **Honest image accounting.** The host measures image time and charges only images it stored. A
  storage fault on the host is the host's fault (`storage_failed`, no charge, a next step in the
  app). An engine that keeps failing goes "recovering" with a visible retry time instead of failing
  every request; oversized or empty engine answers never count against the friend. (156, 159)
- **Run rows.** A long job appears under the chat as a row with a live state (queued, running,
  done) and a Details view — the shape agent runs will use. (139)
- **Voice playback** picks the audio format from the host's response, so WAV and MP3 speech
  engines both play. (123)
- **Uploads** accept exactly what the host admits, and SVG images are rasterised before upload. (132)

**Hosts**

- **Loadout profiles.** `infercat setup` lists the embedded profiles (a 64 GB Apple machine first),
  finds what the machine already has (the Hugging Face cache, Ollama, LM Studio, a local checkpoint
  file) and dry-checks the engines you started by hand; it writes config only if the anchor model
  passes. Downloading and supervising the members is 0.1.5. (151a)
- **A native speech helper.** `infercat-speech` serves Kokoro v1.1-zh over `/v1/audio/speech` with
  streaming WAV and no Python (sherpa-onnx's C API; first audio under a second in English on an
  M-series Mac). It is built and published for macOS arm64 and Linux amd64 with checksums; `setup`
  fetches it in 0.1.5. (151b-1)
- **Image hosting recipe.** `docs/IMAGES.md` (English and Chinese): stable-diffusion.cpp at a pinned
  build with FLUX.2 klein 4B Q8, the flags, the budgets, the measured 15–22 s per 1024² image. (155)
- **The Stored drawer.** The console shows what the host keeps for each key — finished runs, images,
  bytes, files awaiting cleanup and files for review — and clears a key's finished runs in one
  action; `infercat status` prints the two cleanup counts. (158)
- **Run store hardening.** Settlement is bounded and per-key failures stay per key; a key at the
  storage ceiling fails closed with its bytes preserved and the operator told; interrupted runs stay
  failed after a restart and are never replayed; a stuck external runtime is quarantined instead of
  wedging the host. (154, 157)
- **Identity upgrade to `ic2`.** New hosts use pre-shared-key tunnel addresses and `ic2` invites.
  Existing identities stay unchanged until the host stops `serve` and runs `infercat identity
  upgrade`, then restarts and re-issues invites. The old identity is backed up once; the temporary
  upgrade command exists until its planned retirement after **2026-09-25**. Old clients need an
  update to accept `ic2`. (136, 137)
- An exclusive OS data-directory lock prevents concurrent hosts, and an identity upgrade while
  `serve` is running; it is released automatically on process exit. Keep the whole invite private:
  its address now carries a shared secret as well as the host's public key. (137)
- `infercat status` shows the public bridge slots the gateway negotiated. (138)
- Logs and human output mask pre-shared-key addresses. (145)
- **Agent runtime groundwork.** The host installs and supervises the pinned DeepSeek harness runtime
  and shares durable consumer settlement with image jobs. No agent route yet — it lands in 0.1.5.
  (116a, 116b)

**Developers and third-party clients**

- **`/v1/responses`.** The stateless Responses API is adapted to chat completions, so Codex-style
  clients work against a host; streamed tool arguments are counted before usage; termination and
  model accounting are exact. (142, 150)
- **`infercat configure codex`** writes a managed Codex profile (`$CODEX_HOME/infercat.config.toml`);
  connect also manages OpenCode and Harness provider blocks and reports agent status honestly. (140,
  147, 149)
- CI bounds its compiler and reconnect integration tests. (131)

## v0.1.3 — 2026-09-11

One day after 0.1.2: the host serves several engines at once, keeps its accounting exact across a
restart, speaks replies in their own language, and gives the public gateway all of its slots.
Every slice was gated and proved against a real host before it landed.

**Hosts**

- The gateway routes each request to its own destination: the text engine and each audio engine
  now have independent queues, so a transcription never takes a slot from the chat, and the
  pre-check counts tokens against the model the friend asked for. (107)
- Charged usage is recorded, not recomputed: a restart restores exactly what each friend was
  charged today, per resource class (tokens, audio seconds, speech characters), and a request that
  crosses midnight is listed on the day it started and charged on the day it settled. (108)
- `--upstream-speech-voices zh=…,en=…,default=…`: when a Listen request names no voice, the host
  picks one by the reply's script, so a Chinese reply is read in a Chinese voice and an English one
  in an English voice. (118)
- `docs/VOICE.md`: the measured recipe for giving a host voice — Speaches with Whisper, Kokoro with
  the v1.1-zh checkpoint for Chinese and English, the flags, the budgets, and the small setup patch
  that keeps English words inside a Chinese reply. (114, 130)

**Public URL**

- The host announces its slot count to gateway.infercat.ai, which now serves that many requests
  concurrently instead of one at a time; `infercat status` shows today's public-URL count from a
  running counter instead of re-reading history on every poll. The hosted gateway also settles a
  request's body before answering, at the edge and in the object. (120, 112, 119, 127)

**Developers**

- The app and `@infercat/client` share one `/me` contract; the site publishes the wasm pair under
  `/v/<version>/` as well as the root, and this release attaches the pair as assets. (111)

**Internal**

- Groundwork for long-running jobs and agent runs: a per-key run store with atomic snapshots, an
  event log with cursor replay, authenticated run routes and a per-key event stream. No run kind is
  registered, so nothing is user-visible yet. (109, 122)

## v0.1.2 — 2026-09-10

Two days after 0.1.1: the host gets a console, the chat gets images, files and voice, and a host
can serve more kinds of models and reach tools that cannot use the tunnel. Every slice below was
gated and proved against a real host before it landed.

**The console (new)**

- `infercat serve` serves a console on loopback (`infercat console` opens it): the four truths above
  the fold, every friend with limits, live in-flight and last-seen, the engine's facts, usage as
  counts, settings. Mint an invite with the code, link and QR shown once; pause, resume, rotate,
  revoke; edit limits. Settings apply per field and say which need a restart. (069, 074, 075)
- Opt in to reach the console through the tunnel from another device with an admin code (`ia1.…`):
  off by default, one code, rotate or turn off any time; even remotely the console never reads
  prompts or changes the engine URL or the console address. `infercat remote` manages it from the
  CLI. (085, 086, 090)
- The console shows who is connected, the host's public URL and its state, usage split between the
  tunnel and the public URL, and audio seconds and speech characters per friend. (094, 110)

**Chat**

- Attach images when the host's model can see them: a silent 1024 px downscale, honest cost on the
  meter, the sent file in a sheet. Attach PDFs, Word documents, text and code: the text is extracted
  in the browser and is what is sent, shown verbatim. (070, 073, 076, 088)
- Voice: tap the mic to speak a message — the host transcribes it into the composer for editing, with
  a live waveform while recording; press Listen on a reply to hear it. The microphone stays warm
  while the chat is visible so every take starts instantly, the first Stop of a session always
  responds, and a take never loses its first word. (080, 099, 101, 102)
- Drafts stay editable while reconnecting; Send waits until the connection verifies. Models that
  load on demand no longer look like failures: the app waits as long as the host does and names the
  model. Thinking shows for engines that stream it under the newer field name. (081, 089, 061)
- The app is installable: an offline shell with readable chats, a home-screen icon set, one-tap
  return to a saved chat; iOS install copy proved on the simulator. (083, 096, 097)

**Hosts**

- `serve --models a,b` pins what friends can see on multi-model hosts; llama-swap is recognised as
  an engine with per-model context and slots. (066, 065)
- `/v1/audio/transcriptions` and `/v1/audio/speech` from separately configured engines
  (`--upstream-transcribe`, `--upstream-speech`), budgeted per friend in measured seconds and
  characters, never text; `/me` reports vision per model and the audio models. (078, 070)
- A public URL (preview): `infercat expose --register <code>` gives a host an OpenAI-compatible
  HTTPS address at gateway.infercat.ai for tools that cannot use the tunnel. Every key, limit and
  usage line applies unchanged; only keys the host has issued are admitted at the edge; `infercat
  status` shows the URL, whether it is connected and today's count; `expose --off` turns it off.
  Traffic on the public URL is decrypted at the gateway; the tunnel remains the private path.
  Registration codes are issued by hand while this is a preview. (072, 095, 079, 112)
- Tailcat 0.6.0: clients accept both address forms while hosts preserve every existing invite.
  `/status` says which friends are connected. (067, 089)

**Developers and distribution**

- `@infercat/client` (workspace package, browser-only v0, not yet published): an invite in, a
  tunnel-bound fetch and OpenAI client options out. (071)
- A Docker image (`ghcr.io/infercat/infercat`, multi-arch, `/data` volume, proved under Apple's
  container tool) and `.deb`/`.rpm` packages with a systemd user unit. (091, 092)

**Fixed**

- A real race where a restarted host's admin socket could be unlinked by the old server. (077)
- The web app no longer crashes against a host that reports no vision. (082)

**Checks**

- Web lint, the host compatibility matrix (the app runs against every host version shipped), a
  platform-independent licence inventory, real asset links, and the public-URL Worker's own tests
  all run inside `make check`; CI is green end to end. (066, 082, 084, 087, 093, 106, 062)

## v0.1.1 — 2026-09-08

A small release the same day, for shared invites and the first-run path.

**Fixed**

- A friend refused by a shared invite's concurrency cap is told the truth: "This invite is busy — all
  N seats are in use. Try again in a moment." (web app, both languages, and `connect`). The gateway's
  `concurrency_limited` error now carries `limit` and `in_flight`. Invites with one seat keep the
  old wording. (060)
- `serve --log-requests` no longer misses the first request's line: the printer subscribes before
  the gateway starts serving.

**Docs and web**

- Quickstart: an optional two-line Ollama start in the body; the tuned per-engine commands are under
  "Advanced engine configuration"; the install commands sit in one code block. READMEs are modular,
  with Chinese editions. (051, 054, 058, 059)
- Landing: full-width header and footer rules frame the page; the demo video sits in its own row;
  invite anatomy shows the tunnel address and the gateway key; social cards read at feed size;
  light theme only. (043–057)
- Relay map for `derp.infercat.ai` ships with the site.

## v0.1.0 — 2026-09-08

The first release: what the demo proved, made into something a stranger can install.

**What ships**

- One binary for macOS (arm64, amd64), Linux (amd64, arm64) and Windows (amd64): `serve` in front of
  llama.cpp, vLLM, Ollama or LM Studio (found by port, or `--upstream`), `keys` to mint and manage
  one key per friend, `status` and `usage` for what is happening and what happened.
- The web app, as a static bundle (`web-<version>.zip`) any file server can host: paste an invite,
  chat. It shows the path it is on (`relayed via nyc · 64 ms`), the model, and the usage against the
  limits; conversations stay in the browser.
- Invites: `ic1.<host address>.<secret>`, or a link when the host serves with `--web-url`. Shown
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
  design (docs/PRINCIPLES.md, non-goals).
- The public relay in the default map is rate-limited and revocable; a self-hosted relay is the
  supported setting for anything beyond a demo.
- `--log-prompts` is per-run and disclosed to friends in the app; there is no per-key logging.

**Thanks**

To Tailscale, for open-sourcing [tailcat](https://github.com/tailscale/tailcat) and the DERP relay
protocol this is built on, and to the llama.cpp, vLLM, Ollama and LM Studio projects for the engines
it stands in front of. Third-party licences: `THIRD_PARTY_NOTICES.md`.

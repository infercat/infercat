# The real launch demo

Run `make demo` from the repository root on the recording laptop. Prerequisites:
`brew install vhs jq`, `brew install --cask font-ibm-plex-mono`, the pinned web dependencies
(`cd web && pnpm install --frozen-lockfile`), Playwright's Chromium, and the model
engine answering at `http://127.0.0.1:18080`. Network access to infercat.ai and its
relay is required. Nothing is published by this command.

`host.tape` runs the freshly built CLI and mints Alice's real invite, including its
QR. `launch-check.mjs --demo-dir` opens that link on the real infercat.ai at 390×720,
records the automatic connection and a default-settings streamed answer, and holds
it for reading. `friend.tape` runs the native bridge on a free loopback port and
prints a real non-streaming curl reply through `jq`; `question.json` asks "Say hello
in one sentence." The answer stays on screen for six seconds with the connection
path above it. The renderer checks that the curl/jq pipeline succeeded. The browser and native path labels are whatever each actually
observes. No transport, response or screen text is substituted.

`render.mjs` assembles the three captures with the three frozen Archivo captions.
Terminal frames use IBM Plex Mono and the ink/paper palette in `style.tape`; the host
uses smaller type so the real QR fits. The MP4 is H.264, 1280×800, 25 fps; the GIF is
960×600, 10 fps. The poster is the first observed rendered streamed token (reasoning
included), with scene 2's caption. Gates refuse a total over 60 seconds, MP4 over
8,000,000 bytes or GIF over 4,000,000 bytes before replacing the committed outputs.

Every run uses a new temporary data directory and a new invite, revokes the key on
completion or failure, and stops its own host and bridge. Raw captures/transcripts
are retained in the printed `/tmp/icdemo-*` directory for review. The commands,
framing, captions, scene order and artifact shape repeat; real model wording,
reasoning, usage, path timings and the invite may vary (PM ruling, ticket 042).

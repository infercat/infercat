English · [简体中文](README.zh-CN.md)

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
in one sentence." The answer stays on screen for 2.5 seconds with the connection
path above it. The renderer checks that the curl/jq pipeline succeeded. The browser and native path labels are whatever each actually
observes. No transport, response or screen text is substituted.

`render.mjs` assembles the three captures with six step labels and a final end card; there is no title card.
`overlays.mjs` reads all displayed copy from [CAPTIONS.md](CAPTIONS.md), uses the bundled
Latin fonts plus pinned Noto Sans SC in an ignored cache, and renders seven static PNGs per language; each step PNG is checked for zero alpha above y=720.
The enlarged labels all fit unchanged at 39 px. Terminal label cuts come from each
take’s two-second still interval; the browser records its actual Send time.
Terminal frames use IBM Plex Mono and the ink/paper palette in `style.tape`; the host
uses smaller type so the real QR fits. The MP4 is H.264, 1280×800, 25 fps; the GIF is
960×600, 10 fps. The poster is the first observed rendered streamed token (reasoning
included), with step 04. The QR/link hold is 3 s, the browser’s post-answer hold 2 s, and the end card 2.50 s (rounded to
2.52 s at 25 fps); captions and cards change by cuts only. Gates refuse a total over 60 seconds, MP4 over
8,000,000 bytes or GIF over 4,000,000 bytes before replacing the committed outputs.

One recording produces English and Chinese overlay variants: `demo.mp4` / `demo.zh.mp4`,
`demo.gif` / `demo.zh.gif`, and `demo-poster.png` / `demo-poster.zh.png` in `docs/media/`.
The browser footage stays English; both variants pass the same size/duration checks.

Every run uses a new temporary data directory and a new invite, revokes the key on
completion or failure, and stops its own host and bridge. Raw captures/transcripts
are retained in the printed `/tmp/icdemo-*` directory for review. The commands,
framing, captions, scene order and artifact shape repeat; real model wording,
reasoning, usage, path timings and the invite may vary (PM ruling, ticket 042).

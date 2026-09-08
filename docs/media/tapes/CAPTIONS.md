# Infercat demo — caption system

Spec for `docs/media/demo.mp4` (1280×800, 25 fps, 33.52 s, three scenes, no narration, no music). Companion sheet: `captions.html` (same directory; open at 1400 px; `?alone` exports the overlays). Brand: Swiss ink (`docs/brand/swiss-ink.md`); mark: `docs/brand/mark.svg`.

## Point of view

The footage is already Swiss ink — black type on white paper, a hairline, one cobalt cursor — so captions should print the way the terminal prints, not float over it. Every label lives in the 80 px band the render already reserves below y = 720 (the capture is 1280×720; the pad to 800 is white), which is the only region that can never touch a command, the QR, or the app's card. A label is a mono step number in cobalt followed by one plain sentence a friend would say. Six steps and an end card; every change is a cut.

## Decisions

**The three existing bottom-bar captions.** The bar holds one line, and with no narration the label *is* the narration; a claim sharing those 80 px with the step name loses. So the bar becomes single-purpose:

| existing caption | decision | reason |
|---|---|---|
| “One binary in front of the model you already run.” | **retired** | A claim, not a step. The end card tagline and “self-hosted” carry it. |
| “A friend opens the link. No account, no install.” | **kept** as step 03 | It already named a step. Only “A friend” → “Alice”, because the key minted on screen is called `alice`. |
| “Or any OpenAI-compatible client, over the same tunnel.” | **merged** into step 06 | “client” → “app”; “the same tunnel” → “Max's laptop” — name the destination, not the plumbing. |

**Placement.** Only the bar (y 720–800). Nothing is ever drawn on the 1280×720 capture. This is also what makes the system reproducible: no per-frame collision checks.

**Cobalt.** One place in the step system — the step number, the live thing the overlay adds. On the end card, the URL (the brand's “one thing you can act on”). The cobalt cursor and Send button in the footage are recorded, not added.

**Words.** One sentence, present tense, a person doing something. Max and Alice are the names the footage itself shows (`--name "Max's laptop"`, `keys add alice`). “OpenAI-compatible” is the one term kept — it is the vocabulary of the person scene 3 is for and the product's own; everything else is plain.

**Type.** Labels use Archivo 500 39 px (29.25 px in the 960 px GIF), with Noto Sans SC for Chinese glyphs. Mono (IBM Plex Mono) only for what is machine-issued: the number, the URL footer, the licence line, the repo path.

**Motion.** Cuts only. The ground is white in every frame, so a cut is already soft; a dissolve would be the one soft thing in a Swiss ink video.

## Elements

> **PM amendment 2026-09-08 (founder): all overlay text about 30% larger — "we can't expect people to watch the video at full screen."** Bar: number and label 39 px (the 80 px bar holds a 39 px line with ~20 px above and below; if a label no longer fits on one line at 39 px, shorten the label, never wrap), footer 20 px; title tagline 50 px; end-card tagline 40 px, licence 28 px, repo 20 px. Positions adjusted to keep the same margins.

### Bar (every step overlay; 1280×80 at y 720)

| element | copy | type | size | colour | position |
|---|---|---|---|---|---|
| rule | — | 1 px line | full width | `#0A0A0A` | y 720 |
| bar fill | — | opaque | 1280×79 | `#FFFFFF` | y 721–800 |
| step number | `01` … `06` | IBM Plex Mono 500, no tracking | 39 px | `#1F3BFF` | x 24, vertically centred in the bar (baseline ≈ y 771) |
| label | see table below | Archivo 500, −0.02 em | 39 px | `#0A0A0A` | x 80 (number width 36 + 20 gap), same baseline, one line, ≤ 52 characters |
| footer | `infercat.ai` | IBM Plex Mono 400 | 20 px | `#5C6068` | right edge x 1256, vertically centred; on every frame except the end card |

### Title card — retired by 048 (opaque, 1280×800, ≤ 2 s → 1.60 s)

| element | copy | type | size | colour | position (x, y from top-left) |
|---|---|---|---|---|---|
| ground | — | — | 1280×800 | `#FFFFFF` | — |
| mark | — | inline SVG `mark.svg`, `currentColor` | 112×112 | `#0A0A0A` | 96, 300 |
| wordmark | `Infercat` | Archivo 700, −0.04 em | 112 px | `#0A0A0A` | x 230, vertically centred on the mark (caps y ≈ 315–392) |
| tagline | `Give friends a key to the AI on your machine.` | Archivo 400, −0.015 em, lh 1.2 | 50 px | `#0A0A0A` | 96, 452 |
| rule + footer | `infercat.ai` | as bar | 16 px | `#0A0A0A` / `#5C6068` | rule y 720; footer right edge 1256 |

No cobalt on this card.

### End card (opaque, 1280×800, ≤ 3 s → 2.50 s)

| element | copy | type | size | colour | position |
|---|---|---|---|---|---|
| ground | — | — | 1280×800 | `#FFFFFF` | — |
| mark | — | inline SVG | 104×104 | `#0A0A0A` | 96, 216 |
| URL | `infercat.ai` | Archivo 700, −0.045 em | 144 px | `#1F3BFF` | 88, 344 (x optical; the glyph edge lands on 96; baseline ≈ y 460) |
| tagline (PM amendment 2026-09-08, founder: the last frame carries the logo and the slogan) | `Let friends chat with your GPU.` | Archivo 400, −0.015 em | 40 px | `#0A0A0A` | 96, 496 |
| licence line | `MIT · self-hosted · end-to-end encrypted` | IBM Plex Mono 500, no tracking | 28 px | `#0A0A0A` | 96, 566 |
| rule + repo | `github.com/infercat/infercat` | 1 px rule; IBM Plex Mono 400 | 16 px | `#0A0A0A` / `#5C6068` | rule y 720; text x 24, vertically centred in the bar |

No right-hand footer on the end card — the URL is the card. The GIF loops from here into step 01.

## Timing

Measured from the retained 048 recording, with cuts only: browser answer complete at about 6.48 s clip-local, cut at 8.48; terminal answer at about 7.68, cut at 10.20. The raw clips are 12.32 / 13.88 / 13.72 s; the retained clips are 12.32 / 8.48 / 10.20 s. The six label boundaries follow this take; the Send mark at 4.464 s appears on the 4.48 s frame. Final duration is 33.52 s after 25 fps quantization of the 2.50 s end card.

| overlay | scene / clip | clip-local s | recording s | final s | dur s | copy key | what the footage is doing |
|---|---|---|---|---|---|---|---|
| 01 | 1 · host.mp4 | 0.00 – 5.96 | 0.00 – 5.96 | 0.00 – 5.96 | 5.96 | `step-01` | serve output, then clear |
| 02 | 1 · host.mp4 | 5.96 – 12.32 | 5.96 – 12.32 | 5.96 – 12.32 | 6.36 | `step-02` | keys add, full QR and link |
| 03 | 2 · browser.webm | 0.00 – 4.48 | 12.32 – 16.80 | 12.32 – 16.80 | 4.48 | `step-03` | automatic connection, real relayed pill, question |
| 04 | 2 · browser.webm | 4.48 – 8.48 | 16.80 – 20.80 | 16.80 – 20.80 | 4.00 | `step-04` | streamed answer and about 2 s reading hold |
| 05 | 3 · friend.mp4 | 0.00 – 3.88 | 26.20 – 30.08 | 20.80 – 24.68 | 3.88 | `step-05` | connect and its real direct path |
| 06 | 3 · friend.mp4 | 3.88 – 10.20 | 30.08 – 36.40 | 24.68 – 31.00 | 6.32 | `step-06` | curl, readable answer and about 2.5 s hold |
| end | card | — | — | 31.00 – 33.52 | 2.52 | tagline / licence | host sentence and URL |

Future recordings keep these reading holds at the source: the browser's demo-only post-answer wait is 2 s; friend.tape waits 2.5 s after the answer. Inference time and therefore clip boundaries may vary; no absolute cutoff is imposed on a new answer.

## Motion

Everything is a cut: every label change, scene 3 → end card. No fades, no slides, no per-frame rendering. If a single softening is ever wanted, the only candidate is a 0.25 s fade-in on the end card (`fade=t=in:st=0:d=0.25:color=white`); it is not in this spec.

## Implementation note (render.mjs · vhs tapes + ffmpeg)

1. **Seven PNGs per language, 1280×800, straight alpha.** `overlays.mjs` reads the EN/ZH table below, embeds the bundled Latin fonts and a pinned Noto Sans SC from a gitignored cache, and renders six bars and one opaque end card. Each bar is checked for fit and transparency above y 720.
2. **Composite per clip with `overlay` + `enable`, all at 0:0.** This replaces the single `caption-N.png` overlay; the pad to 800 and the encoder flags stay. Scene 1:
   ```
   ffmpeg -y -i host.mp4 -i step-01.png -i step-02.png -filter_complex "\
   [0:v]pad=1280:800:(ow-iw)/2:0:white,setsar=1[s];\
   [s][1:v]overlay=0:0:enable='lt(t,5.96)'[a];\
   [a][2:v]overlay=0:0:enable='gte(t,5.96)',fps=25,format=yuv420p[out]" \
   -map [out] -an -c:v libx264 -crf 24 -preset slow -color_range tv -colorspace bt470bg scene-0.mp4
   ```
   Scene 2 uses step-03/04 with the boundary at 4.48 (clip-local); scene 3 uses step-05/06 at 3.88.
3. **The end card is a looped still.** Encode `end.png` for 2.50 s with the scene encoder flags. Concat scene-0, scene-1, scene-2, end (33.52 s on this retained take). Record once; composite both languages from those same clips and boundaries. Duration and byte gates run on both variants.
4. **Derive the three in-clip boundaries per recording; do not hard-code them.** Real inference moves them run to run. Two of them sit at the end of a tape's only `Sleep 2s`: the first run of ≥ 1.9 s of unchanged frames after output begins is the clear (host) and the curl keystroke (friend) — read it from `ffmpeg -vf "select='gt(scene,0.0005)',showinfo"` timestamps, or compare consecutive frames. The third is the moment `launch-check.mjs` presses Send: have it write the elapsed recording time to `browser.marks.json`. Zero-detection alternative for the host: split `host.tape` at the Ctrl+L into two tapes — the clear becomes a cut to a fresh blank shell, visually identical — and 01/02 become per-clip static overlays exactly like today's captions. Do not split `friend.tape`: the answer must keep the path line above it.
5. **Poster and GIF.** Poster = `browser-first-token.png` + `step-04.png` (was caption-1). The GIF command is unchanged; the end card holds 2.5 s and loops into step 01.
6. **Nothing here needs more than this.** No animation, no fades, no text rendered by ffmpeg (no `drawtext`, no fonts on the render host beyond what vhs already needs), no per-frame compositing. Seven static PNGs per language, `overlay` with `enable`, `-loop 1` for the cards, concat.

## Files

- `captions.html` — the sheet: bar anatomy, the eight overlays composited on real frames at 1:1, a 49 % collision strip, the tables above, this note. Self-contained (frames as data URIs, mark inline, Google Fonts only).
- `overlays/` — the eight PNGs exported from the sheet via `?alone` (proof of the export path; the pipeline should regenerate them).
- `frames/` — ffmpeg stills used on the sheet; `check/` — headless-Chromium verification screenshots at 1400 px.

## Overlay copy (renderer source)

Latin words use Archivo in Chinese overlays; Chinese glyphs use Noto Sans SC. Numbers remain Plex Mono.

| key | en | zh |
|---|---|---|
| step-01 | `Max starts Infercat on his laptop.` | `Max 在自己的笔记本上启动 Infercat。` |
| step-02 | `Max makes Alice a key.` | `Max 给 Alice 生成一串邀请码。` |
| step-03 | `Alice opens the link. No account, no install.` | `Alice 点开链接。不用账号，不用安装。` |
| step-04 | `Max's laptop answers.` | `Max 的笔记本回答了。` |
| step-05 | `The same key works from a terminal.` | `同一串邀请码，在终端里也能用。` |
| step-06 | `Any OpenAI-compatible app can talk to Max's laptop.` | `任何兼容 OpenAI 的应用都能连上 Max 的笔记本。` |
| tagline | `Let friends chat with your GPU.` | `让朋友用你的 GPU 聊天。` |
| licence | `MIT · self-hosted · end-to-end encrypted` | `MIT · 自托管 · 端到端加密` |

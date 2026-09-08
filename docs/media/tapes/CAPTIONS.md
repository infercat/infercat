# Infercat demo — caption system

Spec for `docs/media/demo.mp4` (1280×800, 25 fps, 39.28 s, three scenes, no narration, no music). Companion sheet: `captions.html` (same directory; open at 1400 px; `?alone` exports the overlays). Brand: Swiss ink (`docs/brand/swiss-ink.md`); mark: `docs/brand/mark.svg`.

## Point of view

The footage is already Swiss ink — black type on white paper, a hairline, one cobalt cursor — so captions should print the way the terminal prints, not float over it. Every label lives in the 80 px band the render already reserves below y = 720 (the capture is 1280×720; the pad to 800 is white), which is the only region that can never touch a command, the QR, or the app's card. A label is a mono step number in cobalt followed by one plain sentence a friend would say. Six steps, a title card, an end card; every change is a cut.

## Decisions

**The three existing bottom-bar captions.** The bar holds one line, and with no narration the label *is* the narration; a claim sharing those 80 px with the step name loses. So the bar becomes single-purpose:

| existing caption | decision | reason |
|---|---|---|
| “One binary in front of the model you already run.” | **retired** | A claim, not a step. The title tagline (“the AI on your machine”) and the end card's “self-hosted” carry it. |
| “A friend opens the link. No account, no install.” | **kept** as step 03 | It already named a step. Only “A friend” → “Alice”, because the key minted on screen is called `alice`. |
| “Or any OpenAI-compatible client, over the same tunnel.” | **merged** into step 06 | “client” → “app”; “the same tunnel” → “Max's laptop” — name the destination, not the plumbing. |

**Placement.** Only the bar (y 720–800). Nothing is ever drawn on the 1280×720 capture. This is also what makes the system reproducible: no per-frame collision checks.

**Cobalt.** One place in the step system — the step number, the live thing the overlay adds. On the end card, the URL (the brand's “one thing you can act on”). The title card has no cobalt: cobalt appears when the demo goes live. The cobalt cursor and Send button in the footage are recorded, not added.

**Words.** One sentence, present tense, a person doing something. Max and Alice are the names the footage itself shows (`--name "Max's laptop"`, `keys add alice`). “OpenAI-compatible” is the one term kept — it is the vocabulary of the person scene 3 is for and the product's own; everything else is plain.

**Type.** Labels stay at Archivo 500 30 px — the existing caption size, so the 960 px GIF still reads (22.5 px there). Mono (IBM Plex Mono) only for what is machine-issued: the number, the URL footer, the licence line, the repo path.

**Motion.** Cuts only. The ground is white in every frame, so a cut is already soft; a dissolve would be the one soft thing in a Swiss ink video.

## Elements

### Bar (every step overlay; 1280×80 at y 720)

| element | copy | type | size | colour | position |
|---|---|---|---|---|---|
| rule | — | 1 px line | full width | `#0A0A0A` | y 720 |
| bar fill | — | opaque | 1280×79 | `#FFFFFF` | y 721–800 |
| step number | `01` … `06` | IBM Plex Mono 500, no tracking | 30 px | `#1F3BFF` | x 24, vertically centred in the bar (baseline ≈ y 771) |
| label | see table below | Archivo 500, −0.02 em | 30 px | `#0A0A0A` | x 80 (number width 36 + 20 gap), same baseline, one line, ≤ 52 characters |
| footer | `infercat.ai` | IBM Plex Mono 400 | 16 px | `#5C6068` | right edge x 1256, vertically centred; on every frame except the end card |

### Title card (opaque, 1280×800, ≤ 2 s → 1.60 s)

| element | copy | type | size | colour | position (x, y from top-left) |
|---|---|---|---|---|---|
| ground | — | — | 1280×800 | `#FFFFFF` | — |
| mark | — | inline SVG `mark.svg`, `currentColor` | 112×112 | `#0A0A0A` | 96, 300 |
| wordmark | `Infercat` | Archivo 700, −0.04 em | 112 px | `#0A0A0A` | x 230, vertically centred on the mark (caps y ≈ 315–392) |
| tagline | `Give friends a key to the AI on your machine.` | Archivo 400, −0.015 em, lh 1.2 | 38 px | `#0A0A0A` | 96, 452 |
| rule + footer | `infercat.ai` | as bar | 16 px | `#0A0A0A` / `#5C6068` | rule y 720; footer right edge 1256 |

No cobalt on this card.

### End card (opaque, 1280×800, ≤ 3 s → 2.50 s)

| element | copy | type | size | colour | position |
|---|---|---|---|---|---|
| ground | — | — | 1280×800 | `#FFFFFF` | — |
| mark | — | inline SVG | 104×104 | `#0A0A0A` | 96, 216 |
| URL | `infercat.ai` | Archivo 700, −0.045 em | 144 px | `#1F3BFF` | 88, 344 (x optical; the glyph edge lands on 96; baseline ≈ y 460) |
| tagline (PM amendment 2026-09-08, founder: the last frame carries the logo and the slogan) | `Give friends a key to the AI on your machine.` | Archivo 400, −0.015 em | 30 px | `#0A0A0A` | 96, 500 |
| licence line | `MIT · self-hosted · end-to-end encrypted` | IBM Plex Mono 500, no tracking | 22 px | `#0A0A0A` | 96, 560 |
| rule + repo | `github.com/infercat/infercat` | 1 px rule; IBM Plex Mono 400 | 16 px | `#0A0A0A` / `#5C6068` | rule y 720; text x 24, vertically centred in the bar |

No right-hand footer on the end card — the URL is the card. The GIF loops from here back to the title.

## Timing

Measured on this recording (`demo.mp4`, 39.28 s). Boundaries are the frame at which the footage changes state. Final time = recording time + 1.60 s (the inserted title). Clip-local time is what `render.mjs` composites against (scene 2 starts at 11.88, scene 3 at 25.60).

| overlay | scene / clip | clip-local s | recording s | final s | dur s | label (exact copy) | what the footage is doing |
|---|---|---|---|---|---|---|---|
| title | card | — | — | 0.00 – 1.60 | 1.60 | `Infercat` / `Give friends a key to the AI on your machine.` | inserted before scene 1 |
| 01 | 1 · host.mp4 | 0.00 – 5.56 | 0.00 – 5.56 | 1.60 – 7.16 | 5.56 | `Max starts Infercat on his laptop.` | `serve` typed 0–3.6; status block 3.6; Ctrl+L clear 5.56 |
| 02 | 1 · host.mp4 | 5.56 – 11.88 | 5.56 – 11.88 | 7.16 – 13.48 | 6.32 | `Max makes Alice a key.` | `keys add alice` typed 5.56–7.0; QR + link 7.02; hold |
| 03 | 2 · browser.webm | 0.00 – 4.24 | 11.88 – 16.12 | 13.48 – 17.72 | 4.24 | `Alice opens the link. No account, no install.` | connect card 12.0; Opening… 12.8; welcome 13.0; typing 14.6–16.1 |
| 04 | 2 · browser.webm | 4.24 – 13.72 | 16.12 – 25.60 | 17.72 – 27.20 | 9.48 | `Max's laptop answers.` | Send 16.12; Thinking 16.3–17.8; answer streams 17.8–18.4; hold |
| 05 | 3 · friend.mp4 | 0.00 – 3.96 | 25.60 – 29.56 | 27.20 – 31.16 | 3.96 | `The same key works from a terminal.` | `connect` typed 25.6–27.5; path line 27.5; 2 s hold |
| 06 | 3 · friend.mp4 | 3.96 – 13.68 | 29.56 – 39.28 | 31.16 – 40.88 | 9.72 | `Any OpenAI-compatible app can talk to Max's laptop.` | `curl \| jq` typed 29.56–33.3; sentence 33.5; 6 s hold |
| end | card | — | — | 40.88 – 43.38 | 2.50 | `infercat.ai` / `MIT · self-hosted · end-to-end encrypted` / `github.com/infercat/infercat` | appended after scene 3; total 43.38 s |

Reading time: the shortest hold (05, 3.96 s) carries seven words; 03 (4.24 s) carries eight. Both are inside the usual three-words-per-second caption pace.

## Motion

Everything is a cut: title → scene 1, every label change, scene 3 → end card. No fades, no slides, no per-frame rendering. If a single softening is ever wanted, the only candidate is a 0.25 s fade-in on the end card (`fade=t=in:st=0:d=0.25:color=white`); it is not in this spec.

## Implementation note (render.mjs · vhs tapes + ffmpeg)

1. **Eight PNGs, 1280×800, straight alpha.** `title.png` and `end.png` are opaque. `step-01.png` … `step-06.png` are fully transparent above y 720 and opaque white from the rule down (verified: alpha mean 0 over the top 720 rows, 255 over the bar). Render them from `captions.html` with the Playwright `captionFrames()` already launches: viewport 1280 wide, load `captions.html?alone`, await `document.fonts.ready`, then `page.locator('#ov-03').screenshot({ omitBackground: true, path })` for each of `#ov-title`, `#ov-01` … `#ov-06`, `#ov-end`. For offline reproducibility replace the Google Fonts `<link>` with the repo's `web/public/fonts/archivo-latin-var.woff2` and `ibm-plex-mono-{400,500}-latin.woff2` as data-URI `@font-face` rules — the same faces, the trick `captionFrames()` already uses for Archivo.
2. **Composite per clip with `overlay` + `enable`, all at 0:0.** This replaces the single `caption-N.png` overlay; the pad to 800 and the encoder flags stay. Scene 1:
   ```
   ffmpeg -y -i host.mp4 -i step-01.png -i step-02.png -filter_complex "\
   [0:v]pad=1280:800:(ow-iw)/2:0:white,setsar=1[s];\
   [s][1:v]overlay=0:0:enable='lt(t,5.56)'[a];\
   [a][2:v]overlay=0:0:enable='gte(t,5.56)',fps=25,format=yuv420p[out]" \
   -map [out] -an -c:v libx264 -crf 24 -preset slow -color_range tv -colorspace bt470bg scene-0.mp4
   ```
   Scene 2 uses step-03/04 with the boundary at 4.24 (clip-local); scene 3 uses step-05/06 at 3.96.
3. **Cards are looped stills.** `ffmpeg -loop 1 -framerate 25 -t 1.6 -i title.png -vf format=yuv420p` with the identical libx264 / colour flags → `title.mp4`; the same for `end.png` with `-t 2.5`. `scenes.txt` becomes title, scene-0, scene-1, scene-2, end. Total 43.38 s; the 60 s gate and the 8 MB budget are untouched (four seconds of static white cost nothing). Adjust the duration check to `total + 4.1`.
4. **Derive the three in-clip boundaries per recording; do not hard-code them.** Real inference moves them run to run. Two of them sit at the end of a tape's only `Sleep 2s`: the first run of ≥ 1.9 s of unchanged frames after output begins is the clear (host) and the curl keystroke (friend) — read it from `ffmpeg -vf "select='gt(scene,0.0005)',showinfo"` timestamps, or compare consecutive frames. The third is the moment `launch-check.mjs` presses Send: have it write the elapsed recording time to `browser.marks.json`. Zero-detection alternative for the host: split `host.tape` at the Ctrl+L into two tapes — the clear becomes a cut to a fresh blank shell, visually identical — and 01/02 become per-clip static overlays exactly like today's captions. Do not split `friend.tape`: the answer must keep the path line above it.
5. **Poster and GIF.** Poster = `browser-first-token.png` + `step-04.png` (was caption-1). The GIF command is unchanged; the end card holds 2.5 s and loops back to the title.
6. **Nothing here needs more than this.** No animation, no fades, no text rendered by ffmpeg (no `drawtext`, no fonts on the render host beyond what vhs already needs), no per-frame compositing. Eight static PNGs, `overlay` with `enable`, `-loop 1` for the cards, concat.

## Files

- `captions.html` — the sheet: bar anatomy, the eight overlays composited on real frames at 1:1, a 49 % collision strip, the tables above, this note. Self-contained (frames as data URIs, mark inline, Google Fonts only).
- `overlays/` — the eight PNGs exported from the sheet via `?alone` (proof of the export path; the pipeline should regenerate them).
- `frames/` — ffmpeg stills used on the sheet; `check/` — headless-Chromium verification screenshots at 1400 px.

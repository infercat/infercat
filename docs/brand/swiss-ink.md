# Direction: **Swiss ink**

Infercat brand direction 01. Deliverable: `swiss.html` (open at ~1100 px; both schemes render stacked, no interaction required).

---

## Rationale (112 words)

Swiss ink puts the product's argument in the typography: black ink on white paper, a strict grid, hairline rules, and one printer's cobalt for the single thing you can act on. Nothing is soft — no rounded corners, no tint washes, no warm neutrals. Where the beige-and-terracotta chat apps signal *friendly assistant, trust us*, Infercat signals infrastructure you own: a receipt, a spec sheet, a Unix tool with a face. The cat is drawn from a circle, two triangles and one arc — geometry, not a mascot. Dark mode is true black with the same cobalt, so a terminal-shaped audience sees the tool it expected rather than a chat product wearing a dark theme.

### How the mark works

One compound path, `fill="currentColor"`, nonzero winding: a triangle body, two triangle ears, a circle head, and two slit pupils wound backwards so they knock out as holes. The tail is the single stroked arc. It has no second colour and no outline, so it drops unchanged into a favicon, a one-colour sticker, a terminal README, or a cobalt field. The pupils are vertical slits — the cat-specific tell, and the one that carries the "independent, nocturnal, yours" story without a face.

---

## Palette

### Light

| Role | Hex | Notes |
|---|---|---|
| page bg | `#FFFFFF` | paper; never tinted |
| surface | `#F1F2F4` | cool neutral, not warm grey |
| surface 2 | `#E7E9ED` | the reader's own message |
| text | `#0A0A0A` | 20.4:1 — AAA |
| muted text | `#5C6068` | 6.3:1 on white, 5.7:1 on surface — AA |
| border | `#D8DBE0` | hairlines; frames use ink `#0A0A0A` |
| accent | `#1F3BFF` | printer's cobalt; 6.7:1 on white — AA as text and as fill |
| accent text | `#FFFFFF` | on the cobalt fill; 6.7:1 |
| success | `#0B7A4B` | 5.4:1 — the `direct` path dot |
| warning | `#E5E80B` | acid yellow; **fill only**, always with `#0A0A0A` on it |
| danger | `#D5182F` | 5.3:1 |
| code bg | `#EDEFF2` | mono blocks, invite fields |

### Dark

| Role | Hex | Notes |
|---|---|---|
| page bg | `#000000` | true black, not slate |
| surface | `#101114` | panels |
| surface 2 | `#1A1C21` | the reader's own message |
| text | `#FFFFFF` | 21:1 |
| muted text | `#9AA0AA` | 8.0:1 on black, 7.2:1 on surface — AA |
| border | `#2A2D33` | hairlines; frames use `#FFFFFF` |
| accent | `#1F3BFF` | same cobalt, fills only |
| accent text | `#FFFFFF` | on the cobalt fill; 6.7:1 |
| accent as text | `#7E93FF` | cobalt tint for links on black; 7.5:1 (the flat cobalt is only 3.1:1 on black and must never carry text) |
| success | `#35D48D` | 11:1 |
| warning | `#F2F52E` | fill only, ink on it |
| danger | `#FF5266` | 6.7:1 |
| code bg | `#16181D` | |

Blue and yellow against black-and-white is the Müller-Brockmann pairing, and it is the furthest legal move from terracotta-on-cream. Yellow is load-bearing but never a text colour.

---

## Type stack

Google Fonts, two families, no third.

| Face | Weight | Used for |
|---|---|---|
| **Archivo** | 700 | wordmark, OG display, README lockup |
| **Archivo** | 600 | section headings, connect headline, host name, buttons |
| **Archivo** | 500 | emphasis inside UI chrome, meter values |
| **Archivo** | 400 | body, chat messages, descriptions, privacy line |
| **IBM Plex Mono** | 500 | invite codes, path pill, field labels, badges, hex values |
| **IBM Plex Mono** | 400 | measurements, timing lines, footers |

Rules: Archivo is tracked −0.02 em at heading sizes and −0.035 to −0.045 em at display sizes; mono is never tracked. Nothing is set in a serif anywhere. Nothing below 10.5 px carries meaning. Mono is reserved for things that are *measured or machine-issued* (codes, latencies, token counts) — prose never borrows it, which is what keeps the metrics readable as facts rather than decoration.

---

## Three risks (honest)

**1. The austerity fights the pitch.** Infercat's actual proposition is a warm social act — lending your GPU to your friends, "self-sufficient small AI clouds for you and your circle." Pure black-on-white with zero radii, hairline rules and mono metrics reads as a status page, a bank statement, or a compliance report. It nails "infrastructure you own" and may entirely lose "for you and your circle." A founder who wants the friend-to-friend warmth to survive will have to reintroduce it in copy and photography alone, because this system gives it no visual channel.

**2. It is adjacent to two of the things the anti-brief bans.** Black/white/blue with a grotesk is Tailscale's neighbourhood, and cobalt `#1F3BFF` is one hue-step from the indigo button this brief calls the other cliché. The separation is real but it lives in details — Archivo not Inter, IBM Plex Mono as a co-equal voice, zero border radius, true black rather than slate, acid yellow as the secondary — and details are exactly what get sanded off by the third contributor or the first component library. Monochrome-plus-one-accent is also now the default look of every developer tool launched since 2021; this may land as a competent template rather than as a brand anyone remembers.

**3. The mark and the density have small-size problems.** The slit pupils are ~1.4 px wide at 16 px and the tail stroke ~1.45 px, so on a non-retina tab favicon both blur toward mush and the mark falls back to pure silhouette. Worse, the tail is what makes it read *cat* rather than *fox* or *bear* — and it is the part that gets clipped by any square or circular avatar crop (GitHub org, Discord, app icon), which is precisely where the mark will be seen most. Separately, high-contrast pure `#000`/`#FFF` body text plus visible hairlines makes a long chat busier than a soft-contrast scheme, and true-black dark mode causes halation for some readers on OLED — a message-heavy screen is the one surface where this direction's discipline costs the most.

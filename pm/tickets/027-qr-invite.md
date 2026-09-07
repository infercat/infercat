---
id: 027
title: QR invites that a phone camera actually reads — verified, and exportable as an image
kind: normal
size: 1
status: done
updated: 2026-09-07
release: demo-1
---

# 027 — QR invites for phones

**Why.** Founder: many friends will open the chat on a phone; the easiest path is a photo of a QR code.
The CLI already prints a terminal QR (009) and encodes the full link when `--web-url` is set, but nobody
has verified a phone camera reads a ~200-character link from a terminal render, and a host cannot get
the QR as an image to text to someone.

**Promises.**
1. **Verified scan.** With `--web-url` set, the terminal QR encodes `<web-url>#<invite>`; a phone camera
   (real iPhone and Android, not an emulator) reads it from the laptop screen at normal size in one try,
   opens the app, and auto-connects. Record the QR version and error-correction level used; if the
   terminal render is unreadable at that density, switch to a higher error-correction level or a larger
   block render and re-verify.
2. **Image export.** `keys add --qr-png PATH` (and `keys rotate`) writes a PNG of the same QR (with the
   host name and "scan to chat" under it) so the host can text or AirDrop it; `--json` includes the
   path. Nothing new is stored on disk beyond the PNG the host asked for.
3. **Web app shows its own QR** for the invite it is connected with? — NO (declined: the friend already
   has it; a QR on screen would leak the secret to anyone looking). Instead, the connect screen's
   "Invite from your link" path is the phone target and must survive the camera app's URL handling
   (fragment preserved — verify on iOS Safari and Android Chrome).
4. Evidence: photos/screenshots of the phone scan; the PNG; `go test` for the encoder settings.

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `cmd/bunny-network/qr.go`, `keys.go`, tests;
`web/src/ui/Connect.tsx` only if the fragment handling needs a fix. Depends on F3 (web URL) for the
real-phone verification; the PNG export can land before.

## Log

## Report
---
id: 027
title: QR invites that a phone camera actually reads — verified, and exportable as an image
kind: normal
size: 1
status: done
updated: 2026-09-07
release: demo-1
---

# 027 — QR invites for phones

**Why.** Founder: many friends will open the chat on a phone; the easiest path is a photo of a QR code.
The CLI already prints a terminal QR (009) and encodes the full link when `--web-url` is set, but nobody
has verified a phone camera reads a ~200-character link from a terminal render, and a host cannot get
the QR as an image to text to someone.

**Promises.**
1. **Verified scan.** With `--web-url` set, the terminal QR encodes `<web-url>#<invite>`; a phone camera
   (real iPhone and Android, not an emulator) reads it from the laptop screen at normal size in one try,
   opens the app, and auto-connects. Record the QR version and error-correction level used; if the
   terminal render is unreadable at that density, switch to a higher error-correction level or a larger
   block render and re-verify.
2. **Image export.** `keys add --qr-png PATH` (and `keys rotate`) writes a PNG of the same QR (with the
   host name and "scan to chat" under it) so the host can text or AirDrop it; `--json` includes the
   path. Nothing new is stored on disk beyond the PNG the host asked for.
3. **Web app shows its own QR** for the invite it is connected with? — NO (declined: the friend already
   has it; a QR on screen would leak the secret to anyone looking). Instead, the connect screen's
   "Invite from your link" path is the phone target and must survive the camera app's URL handling
   (fragment preserved — verify on iOS Safari and Android Chrome).
4. Evidence: photos/screenshots of the phone scan; the PNG; `go test` for the encoder settings.

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `cmd/bunny-network/qr.go`, `keys.go`, tests;
`web/src/ui/Connect.tsx` only if the fragment handling needs a fix. Depends on F3 (web URL) for the
real-phone verification; the PNG export can land before.

## Log

## Report

- 2026-09-07 — Founder: opened a `https://infercat.ai#ic1…` invite on a phone over cellular and chatted through the relay. Done.

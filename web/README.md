English · [简体中文](README.zh-CN.md)

# Browser app

Vite + React + TypeScript; the Go tunnel client compiles to WebAssembly under [wasm/](wasm/). For using the app, see the [root quickstart](../README.md#quickstart-friend).

## Layout

- [src/api.ts](src/api.ts): gateway requests, usage and errors; [src/stream.ts](src/stream.ts): streamed responses.
- [src/session.ts](src/session.ts): connection/session state; [src/storage.ts](src/storage.ts): browser persistence and the key registry.
- [src/invite.ts](src/invite.ts): invite parsing; [src/transport/](src/transport/): native fetch and HTTP over the wasm tunnel.
- [src/i18n/](src/i18n/): English/Chinese tables and named substitutions; [src/ui/](src/ui/): connect, chat and landing components.

## Commands

From the repository root, `make wasm` builds the browser bridge; `make web` builds wasm and the app into `web/dist`. See [Contributing](../CONTRIBUTING.md#building) for the complete build/release commands.
Run these from `web/` after `pnpm install --frozen-lockfile` (browser checks also need `pnpm exec playwright install chromium`):

| Command | Purpose |
|---|---|
| `pnpm dev` | Vite development server. |
| `pnpm build` | Production bundle in `dist/`; build wasm first. |
| `pnpm test`, `pnpm lint`, `pnpm typecheck` | Unit tests, lint and TypeScript checks. |
| `pnpm screenshots` | Fake-host browser scenarios and screenshots. |
| `pnpm launch-check` | Check the built app in a local preview; `INVITE=… APP=…` also exercises a real host. |
| `pnpm tunnel-check` | Real wasm/tunnel test against the local tunnel demo; needs network access. |
| `pnpm brand` | Regenerate icons, flat marks and the two social cards from the shared mark/fonts. |

[dev/](dev/) contains the fake gateway/backend and fake tunnel, plus the launch, screenshot, tunnel and brand harnesses. The screenshot harness starts its own fake services; a real-host check needs your own host and invite.

## Copy and fonts

Change both [en.ts](src/i18n/en.ts) and [zh.ts](src/i18n/zh.ts); preserve named placeholders. Keep storage keys in the [registry](src/storage.ts).
After copy changes, rebuild the shared CJK subset with `python3 hack/subset-font.py /path/to/NotoSansSC[wght].ttf` from the repository root. It uses the union of both tables; web tests check every CJK glyph against the shipped font. The source/fontTools instructions are in [Hosting](../hosting/README.md#rebuilding-the-chinese-font-subset). Keep the OFL and run `make notices-check`; never add a runtime font CDN.

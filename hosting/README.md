# Hosting the web app

The web app is a static bundle (`web/dist` after `make web`, or `web-<version>.zip` from a release).
Any static host serves it. The repo is host-agnostic: everything a particular host needs lives under
`hosting/<host>/` and never in the app.

## Cloudflare Pages (infercat.ai)

`make deploy-web` builds the app, assembles `web/deploy/` from `web/dist` plus `hosting/cloudflare/`,
and publishes it with wrangler (OAuth login; run from a directory without a `.env`).

- Only the gzip twin of the wasm ships (`infercat.wasm.gz`); Pages refuses files over 25 MB and the
  app fetches the gzip copy first anyway. The raw module stays in the release zip for self-hosters.
- `_headers`: HSTS, nosniff, no referrer, long cache on hashed assets.
- `_routes.json` + `functions/_middleware.js`: the first-party counter. One Analytics Engine data
  point per page view (`/`, with the referring site and a `?from=` tag) and per app load
  (`/infercat.wasm.gz`), by country and browser family. No script on the page, no cookie, no third
  party; the invite in the fragment never reaches a server. Query in the dashboard (Analytics Engine,
  dataset `infercat_loads`): `SELECT index1 AS kind, blob1 AS country, blob4 AS from, SUM(_sample_interval) AS n FROM infercat_loads WHERE timestamp > NOW() - INTERVAL '7' DAY GROUP BY kind, country, from`.
- infercat.dev is a separate Pages project that only redirects to infercat.ai (`hosting/cloudflare/redirect/`).

## Roadmap signup list

`POST /signup` accepts `{email, lang, from, ts}`. The Function stores a canonical address, language,
fixed `landing-roadmap` source and server receipt time in `SIGNUPS`; duplicates keep their first
record and succeed. It sends no email. The hashed-IP cooldown expires after 60 seconds; KV is
eventually consistent, so this is a simple per-network brake, not an exact concurrent quota.
The namespace id in `wrangler.toml` is a placeholder: the PM creates it and fills the id at landing.

Export the signup metadata (the `email:` prefix excludes cooldown records):

```sh
wrangler kv key list --config hosting/cloudflare/wrangler.toml --binding SIGNUPS --prefix 'email:' --remote > signup-keys.json
jq '[.[].metadata]' signup-keys.json > signups.json
```

These are private email addresses: keep exports outside the public repository. `kv key list`
follows pagination; records carry metadata so the export needs no per-address reads. See the
[Wrangler KV commands](https://developers.cloudflare.com/workers/wrangler/commands/kv/).
The Function is tested with fake KV by `cd web && pnpm test`; no cloud resources are needed.

### Rebuilding the Chinese font subset

Source: Google Fonts `ofl/notosanssc/NotoSansSC[wght].ttf`, with its adjacent `OFL.txt`.
Using Python fontTools 4.60.2 with its WOFF extra (`pip install 'fonttools[woff]==4.60.2'`),
concatenate `web/src/i18n/en.ts` and `zh.ts` to a temporary glyph text file, then run:

```sh
python3 -m fontTools.subset 'NotoSansSC[wght].ttf' --text-file=glyphs.txt --flavor=woff2 --output-file=web/public/fonts/noto-sans-sc-landing.woff2
make notices
```

The font and OFL are served from `web/public/fonts/`; no runtime font CDN is used.

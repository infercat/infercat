English · [简体中文](README.zh-CN.md)

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
- `derpmap.json`: the relay map for Infercat's own DERP relay (`derp.infercat.ai`, region 900). Hosts
  pin it with `infercat serve --derpmap-url https://infercat.ai/derpmap.json`; the region is then
  embedded in every invite that host mints, so the hostname must not change once keys are out.
- infercat.dev is a separate Pages project that only redirects to infercat.ai (`hosting/cloudflare/redirect/`).

## The demo invite: `/try`

`https://infercat.ai/try` is the one address the site, the README and posts use for the public demo
host. `functions/try.js` reads the current invite link from KV (`SIGNUPS`, key `link:try`) and
answers a 302 to it with `?from=try`, so the counter attributes the visit and the app opens with the
code in the box. No invite is ever committed. To remint (the old key paused or revoked on the host):

```
infercat keys add reddit-public-2 --max-concurrent 30 --max-output-tokens 8192 --rpm 100000 --tpm 100000000 --daily-tokens 10000000000 --json --no-qr
wrangler kv key put --config hosting/cloudflare/wrangler.toml --binding SIGNUPS --remote link:try 'https://infercat.ai#ic2.…'
```

The change is live within a minute; nothing is deployed. Without a stored link, `/try` opens the
landing page.

## Roadmap signup list

`POST /signup` accepts `{email, lang, from, ts}`. The Function stores a canonical address, language,
fixed `landing-roadmap` source and server receipt time in `SIGNUPS`; duplicates keep their first
record and succeed. It sends no email. The hashed-IP cooldown expires after 60 seconds; KV is
eventually consistent, so this is a simple per-network brake, not an exact concurrent quota.
The deployed `SIGNUPS` namespace is bound in `hosting/cloudflare/wrangler.toml`. A separate deployment needs its own namespace and binding.

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
Using the existing Python fontTools 4.60.2 WOFF extra (`pip install 'fonttools[woff]==4.60.2'`):

```sh
python3 hack/subset-font.py 'NotoSansSC[wght].ttf'
python3 hack/subset-font.py --check
make notices
```

The script subsets the union of both tables to `web/public/fonts/noto-sans-sc.woff2` and verifies
CJK coverage. The normal web tests independently read the shipped font's cmap to catch copy/font
drift without requiring Python. The same font and OFL are self-hosted; no runtime font CDN is used.

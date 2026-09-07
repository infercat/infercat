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

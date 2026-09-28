# Fixtures

Two folders, two jobs.

## `contract/` — the app's contract fixture

Vendored, byte for byte, from `cmd/infercat/testdata/contract/` on main by
`apps/macos/vendor-fixtures.sh`. CI re-runs that script and fails on a diff, so a
payload that moves under the app is caught in the product's own CI.

`ContractTests` decodes every one of these into the Swift type the app uses, in both
forms: `empty` is the Go zero value (a `null` slice, an absent `omitempty` field) and
`populated` carries every optional field. A field the host makes optional therefore
fails here rather than at a person's desk. Unknown fields are ignored on purpose —
the contract allows additions without a schema bump — so these files are a *subset*
check, not an equality check.

Only the operations cut A consumes are vendored: `status`, `keys.list`,
`service.status|install|start|stop|restart`, `settings.set`, `usage`, `console.open`,
`version`, the five `watch` line shapes, and the common `error` envelope. Cut B adds
the `keys.*` mutations and `stored.*`; cut C adds `engine` and `runs.list`.

## `demo/` — what the screenshots show

Hand-written payloads in the contract's shapes, with the mockups' example data (Lin,
Bao, Wei; `qwen3.8-flash-next`; up 3d 4h; 214 calls). They exist so `--fixtures` and
`--capture` have something realistic to draw, and so a reviewer can see the same
numbers the design document uses. They are not captured from a host and contain no
usable invite, token or address.

## Where the design's field names differ from the contract

The design spec (`desktop-app-ui.md`) was written before 195 landed. Per seat answer
§10.4, the payload wins. The differences cut A hit:

| Design says | The payload has | What the app does |
|---|---|---|
| `upstream.model` | no such field | `models_pinned[0]`, and the model is left out of the sentence when the host pins none |
| `upstream.slots`, `model_context` | present, as named | used as named |
| `tokens_per_s_1m` on the root | `engine.tokens_per_s_1m`, with `engine.metrics` saying whether it is measured at all | shown only when `engine.metrics` is true; otherwise "not reported" |
| `keys[].via` | `tunnel.sessions[].via`, keyed by `key` | a friend reads "through the tunnel" when an active tunnel session carries their key id, "through the public URL" when the bridge is connected, and nothing when the host did not say |
| "usage today: calls, tokens, errors" on `status` | a separate `usage --window today` route | read on the first status and at most once a minute while visible; the cell reads "not reported" until it arrives |
| `keys[].daily_tokens` limit on `status` | limits live on `keys.list` only | "Needs attention" needs a limit, so it produces nothing until `keys.list` has been read; it never guesses one |
| `service.status.since` | a string the CLI formats, not a timestamp | printed as given |

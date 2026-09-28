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

Vendored so far: `status`, `keys.list`, `keys.get`, `keys.add`, `keys.rotate`,
`keys.limits`, `keys.pause`, `keys.resume`, `keys.revoke`,
`service.status|install|start|stop|restart`, `settings.set`, `usage`, `console.open`,
`version`, the five `watch` line shapes, and the common `error` envelope. Cut C adds
`engine`, `runs.list` and `stored.*`.

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


## What "no limit" actually sends, and why it is not zero

This is the one place in the app where a plausible-looking value would ship a real
bug, so it is written down. Evidence is in the host's own source: `internal/keys/
keys.go` (`DefaultLimits`, `AudioDefaults`, `ImageDefaults`), `internal/keys/store.go`
(`WithDefaults`, `SetLimitsAndAgent`), and the enforcement sites in
`internal/gateway/`, which uniformly ask `limit > 0`.

Three layers coerce a limit before the gateway sees it, and they do not agree:

| Fields | `0` on `keys add` | `0` on `keys limits` | Unlimited is |
|---|---|---|---|
| `rpm`, `tpm`, `max_concurrent`, `max_output_tokens`, `daily_tokens` | the host default | literally 0, which *is* unlimited | `-1` (works on both) |
| `search_per_day`, `daily_images`, `max_queued_images` | the default | re-coerced to the default on the next read | `-1` |
| `daily_audio_seconds`, `daily_speech_chars` | the default | re-coerced to the default on every read | any negative |
| `max_context` | the engine's own window | the engine's own window | `0` — this one *is* the sentinel |

So the app sends **`-1`** for a cleared field, and `0` only for `--max-context`.
Sending `0` for the others would silently reinstate the host's default and the person
would have been told "no limit".

Two more rules the app follows for the same reason:

- **An untouched field is not sent at all.** Both routes are sparse — "only the flags
  you pass change" — and an unpassed flag on `keys add` takes the host's default. The
  contract exposes no way to *read* those defaults (no operation carries them; the
  `--help` text hard-codes them), so the app cannot prefill a new invite's form. It
  therefore leaves untouched fields empty, labels them "the host's default", and sends
  nothing for them. Prefilling only happens when editing an existing key, where the
  host has told us its real values.
- **`--models` is sent only when edited**, because an explicitly empty `--models`
  sends `null` and clears an allowlist, while omitting it keeps one.

## Where the design and the contract still disagree

Added in cut B, on top of the cut A table above:

| Design says | The payload has | What the app does |
|---|---|---|
| searches/day appears only if the matching `destinations[]` entry exists | there is no search destination — search is a host tool, and no machine payload reports whether it is configured | searches/day is always offered; the limit is always enforced, so the field is never a lie, but the app cannot hide it on a host without search |
| `destinations[]` entries are distinguished by kind | every destination carries `kind: "engine"`; the discriminator is `id` ∈ `text, transcribe, speech, embed, images` | image, audio and speech limits are gated on `id` |
| a duplicate name is its own refusal | a namesake is a plain 400 → `invalid_request`, with a multi-line message written for a terminal | the app checks `keys list` itself and shows the spec's sentence with the Rotate offer; the host's own first line is the fallback |
| `last_seen` is absent when a friend never connected | it is always present, as Go's zero time | `0001-01-01T00:00:00Z` reads as "never connected" |
| `keys add` applies agent access | `--agent` exists on `keys add` in machine mode, but the capability is the host's to grant | the app passes `--agent` and then reads it back with `keys show`; if the host did not grant it, the sheet says so |

English · [简体中文](CLI-JSON.zh-CN.md)

# CLI JSON contract (schema 1)

Use the bundled binary by its absolute path and pass an argv array, without a shell. `--json` means `--json=1` permanently. Every ordinary machine command writes one JSON object to stdout; errors also use stdout and a nonzero exit. No colour, QR code, confirmation prompt or terminal read occurs. `watch` is the streaming exception described below.

```sh
infercat version --json
infercat status --json
infercat keys add --json -- "Alice Chen"
infercat keys limits k_example --rpm 40 --agent --json
infercat settings set --json -- "name=My host" slots=2
infercat keys revoke k_example --yes --json
```

Free text (new names and settings assignments) must follow `--`; put flags, including `--data-dir`, before it. Names beginning with `-` are refused. Machine key references are IDs, not display names. Human commands still resolve unique names.

## Envelope and compatibility

```json
{"schema":1,"command":"status","host":{"version":"0.1.6","name":"My host"},"data":{}}
```

```json
{"schema":1,"command":"keys.pause","error":{"code":"key_not_found","message":"no key k_example"}}
```

`data` is the admin route's response byte for byte, apart from whitespace outside its JSON value. The CLI does not normalize arrays, nulls, zero values or omitted fields. The [generated field inventory](../cmd/infercat/testdata/cli-schema-1.txt) records those distinctions. New CLI-owned payloads (`api`, `version`, envelopes) use present zero values and `[]` for empty lists. The host metadata describes the host at the start of the operation; offline commands use the CLI build and remembered local name (empty for `version` and `api`).

Adding fields or operations is compatible. Error codes and string enums describing engine kinds, key status and run state/kind are open: preserve or display unfamiliar values rather than rejecting the response. Removing, renaming or retyping fields, making a required field optional, changing nullability, changing an exit code's meaning or changing an existing operation's semantics requires a new schema. Schema 1 and bare `--json` must continue to work; a new version does not silently replace them. Consumers ignore unknown fields.

**Script migration:** the former unwrapped `keys add --json`, `keys rotate --json` and `remote … --json` results now live inside schema 1's `data` envelope. Move readers to `.data`; move new key names after `--`. `remote.status` and `expose.status` return the complete `/status` body, not a projection.

| Exit | Meaning |
|---|---|
| 0 | Success |
| 1 | Operation failed; inspect `error.code` and `error.message` |
| 2 | Invalid arguments, unsupported schema, or missing `--yes` (`confirmation_required`) |
| 69 | No host is running for this data directory (`host_stopped`) |
| 75 | The host did not answer in time (`host_not_responding`) |

The local client sends each mutation once (preparatory reads may precede it), never redirects and never retries a mutation. Calls have a five-second timeout and a 16 MiB response bound. A timeout, broken reply or malformed result may follow an action that already committed: inspect state before deciding what to do; do not automatically repeat a mint, rotation or clear. HTTP errors preserve the route's sentence; codes derive from status and operation (`key_not_found`, `not_found`, `conflict`, `invalid_request`, `unauthorized`, `forbidden`, otherwise `admin_error`). Other local failures use `command_failed`; incomplete/invalid responses use `invalid_response`.

## Host and offline behavior

With a host running, key mutations and usage use the authenticated admin routes. A failed online mutation never falls back to a file write. Human output and its stopped-host file path remain available. Human `usage --since` retains rolling-duration semantics; machine `usage --window today|week` uses UTC calendar days and accepts `--key ID`.

`keys list`, `keys add`, `version` and `api` work offline. Listing helps inspect a stopped host; adding lets an operator prepare an invite using an already-created v2 identity. Offline add cannot create a host identity and retains the legacy-identity refusal. Other admin operations require a running host. The separately implemented `service` operations talk to the local supervisor and also work while the host is stopped. The admin token remains inside the CLI and is never included in an envelope.

`force` and `agent` key changes are local-only; the remote console refuses either field, including an explicit false or null, before mutation. The human remote console API and its query allow-list are unchanged. Settings use only the route's whitelist: `name`, `web_url`, `slots`, `console`, `log_requests`.

`stored clear ID --expect CURSOR --yes` fetches the current record, checks that its cursor matches, then sends that same snapshot's counts to DELETE. A conflict refuses without retry. `expose --on|--off` retains the local bridge configuration write and returns the reload's `{ "ok": true }`; registration remains a human operation.

## Watch

```sh
infercat watch --json --interval 2s
```

One object per line: `hello` (schema, host, interval_ms, events), `status` (schema, at, data), `event` (schema, data), `dropped` (schema, count), then `gone` (schema, reason) when the connection ends. `hello` follows the event subscription opening, immediately followed by the already-fetched first `status`; later status lines are polled on the interval. Response bodies are compacted to one line. Prompt and completion text never appear.

The CLI buffers at most 256 events. A slow stdout reader does not block the event reader; `dropped` reports only losses observed in this forwarding queue. The host's existing event fan-out is also lossy and does not report its losses, so this is not a durable audit stream. Read aggregate status/usage for current totals.

Machine-mode watch writes nothing to stderr during normal operation, including handled failures; only fatal process diagnostics may appear there. A host stop or restart ends the current connection with `gone` and exit 69; watch never reconnects itself. The consumer starts a new watch.

With no host at startup, watch prints one `gone` line and exits 69, without `hello`. A timeout exits 75. Interrupting watch cancels its connections and joins its reader; it does not stop the host. Remote-host mode is separate work. Service lifecycle commands are implemented by the service layer (196), and are included in the operation and field inventories below.

## Operations

Generated from `infercat api --json`; `{id}` is the admin path parameter. `--data-dir DIR` is supported on every command.

<!-- cli-operations:start -->
| Operation | argv after `infercat` | Admin route(s) | Schema |
|---|---|---|---|
| `status` | `status --json` | GET /status | 1 |
| `watch` | `watch --json [--interval 2s]` | GET /status; GET /events | 1 |
| `keys.list` | `keys list --json` | GET /keys | 1 |
| `keys.get` | `keys show ID --json` | GET /keys/{id} | 1 |
| `keys.add` | `keys add --json [limits] -- NAME` | POST /keys | 1 |
| `keys.limits` | `keys limits ID --json [limits]` | PATCH /keys/{id} | 1 |
| `keys.pause` | `keys pause ID --json` | POST /keys/{id}/pause | 1 |
| `keys.resume` | `keys resume ID --json` | POST /keys/{id}/resume | 1 |
| `keys.revoke` | `keys revoke ID --yes --json` | POST /keys/{id}/revoke | 1 |
| `keys.rotate` | `keys rotate ID --json` | POST /keys/{id}/rotate | 1 |
| `usage` | `usage --json --window today\|week [--key ID]` | GET /usage | 1 |
| `engine` | `engine --json` | GET /engine | 1 |
| `settings.get` | `settings --json` | GET /settings | 1 |
| `settings.set` | `settings set --json -- k=v…` | PATCH /settings | 1 |
| `runs.list` | `runs list --json [--key ID]` | GET /runs | 1 |
| `stored.get` | `stored ID --json` | GET /stored | 1 |
| `stored.clear` | `stored clear ID --expect CURSOR --yes --json` | GET /stored; DELETE /stored | 1 |
| `remote.status` | `remote status --json` | GET /status | 1 |
| `remote.on` | `remote on --json` | POST /remote/enable | 1 |
| `remote.off` | `remote off --json` | POST /remote/off | 1 |
| `remote.rotate` | `remote rotate --json` | POST /remote/rotate | 1 |
| `expose.status` | `expose --json` | GET /status | 1 |
| `expose.on` | `expose --on --json` | POST /reload | 1 |
| `expose.off` | `expose --off --json` | POST /reload | 1 |
| `service.status` | `service status --json` | — | 1 |
| `service.install` | `service install --json [--start-at-login=false]` | — | 1 |
| `service.uninstall` | `service uninstall --json` | — | 1 |
| `service.start` | `service start --json` | — | 1 |
| `service.stop` | `service stop --json` | — | 1 |
| `service.restart` | `service restart --json` | — | 1 |
| `service.login` | `service login on\|off --json` | — | 1 |
| `version` | `version --json` | — | 1 |
| `api` | `api --json` | — | 1 |
<!-- cli-operations:end -->

## Maintaining the contract

`make cli-contract` regenerates the field inventory and this operation table, then requires `git diff --exit-code`. The regular Go suite checks the inventory, subprocess goldens in a non-TTY, and a walk of the actual admin registrations. The parity walk compares the CLI's raw `data` with the real handler's body. The inventory checker rejects removed/renamed/retyped/presence-changed fields, including attempts to regenerate a breaking schema-1 edit over the previous commit.

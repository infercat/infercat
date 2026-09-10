English · [简体中文](DATA-DIRECTORY.zh-CN.md)

# Data directory

`host.lock` is a permanent lock-file inode, not a PID file. `serve` holds an exclusive OS lock for its lifetime; a second `serve` or identity upgrade on the same directory refuses. The OS releases the lock on process exit, including a crash; do not delete the file while a host is running. Stop older binaries too: they predate this lock; the upgrade also checks their admin listener. `infercat identity upgrade` requires a stopped host, saves `host.key.json.pre-ic2` once without overwriting it, and atomically publishes the version-2 identity. Do not run an older binary on an upgraded identity: it ignores the marker and serves the legacy form of the same key, so `ic2` invites fail silently at the handshake.

`~/Library/Application Support/infercat` (macOS), `~/.config/infercat` (Linux),
`%AppData%\infercat` (Windows), or `--data-dir`:

| File | What it is |
|---|---|
| `host.key.json` | Your host identity. Back it up; do not sync it; deleting it invalidates every invite you sent. |
| `keys.json` | Friends' keys as hashes (never the secret), their status and limits. |
| `usage.jsonl` | One line per request: key, endpoint, status, token counts, timings. No prompt content unless you run `serve --log-prompts`. |
| `admin.json` | Optional remote-console switch: SHA-256 admin-code hash and enabled-since time, mode 0600; never plaintext or a friend key. Removed when turned off. |
| `bridge.json` | Public bridge endpoint, host id, and secret bridge token (0600). Created by `expose --register`; read by `serve` at startup/reload; removed by `expose --off`. Keep private. |
| `config.json` | Remembered `serve` flags. |
| `tunnel.log` | The tunnel engine's log (`serve --verbose` prints it instead). |

`infercat serve -h` also lists these files. See the [host quickstart](../README.md#quickstart-host).

The console reuses `admin.token` on every platform: a fresh random bearer per run, mode 0600,
removed when that admin server shuts down. The console address is remembered in the existing
`config.json` (`console`); the actual listening address is in authenticated `/status`, so port 0
also works with `infercat console --print`. No additional state file is introduced.

Remote access is off until enabled locally. `admin.json` preserves that choice across restarts;
`admin.token` remains an independent per-run loopback credential. Rotation atomically replaces
the persisted hash. The in-use marker is memory-only and expires after ten minutes. An unreadable
or malformed admin record starts the host with remote access off. The banner and Settings name
the file and the repair: enable remote access again to mint a new code. The damaged file stays
intact until that explicit action replaces it atomically; a failed repair is reported.

For a running headless host, use `infercat remote on --data-dir DIR` to enable access and receive
its one-time admin link/code and terminal QR. Paste it into the web app or open its link to reach
`/console`. `remote rotate` replaces the code, `remote off` disables access, and `remote status`
reports state without returning a code. All commands require the loopback console listener on
and use the authenticated local admin API. `--json` returns the result for scripts; `--no-qr`
omits the QR. Protect on/rotate output as a secret. Commands never replay an uncertain action;
if its result is lost, check status and rotate to obtain a fresh code.

New `usage.jsonl` rows include per-class `meters` with unit, measured amount and charged amount.
`ts` remains request start; `settled_at` locates charges in their UTC settlement day.
The host restores daily budgets from recorded charges, including zero-charge refusals and the
reservation charged for a cut non-streaming request. Older rows without `settled_at` use `ts` for
accounting; rows without meters keep the historical
measured-equals-charged interpretation; existing token/audio telemetry remains readable. No file
migration occurs: `keys.json` keeps its existing limit fields, interpreted as resource budgets in
memory, and reading it does not rewrite it.

### Run state

`runs/<key-id>/state.json` is an atomic per-key snapshot containing inputs, outputs,
attempt trajectory, lifecycle metadata and the last 256 state events. Directories
are private (0700), files 0600. Only key IDs are stored as identity; bearer secrets
are never saved in run metadata. Run contents are user data, independent of the
optional prompt logging in `usage.jsonl`. Corrupt snapshots fail closed for that
key; they are never silently replaced with an empty store.

The fixed initial bounds are 1 MiB input and 1 MiB output per step, 64 MiB of stored
snapshot data per key, 100 retained runs per key, 16 live runs per key and 64 live
runs per host. Additions refuse at a bound rather than trim existing work. Terminal
content expires seven days after completion or cancellation. A run's maximum live
age is 24 hours; the sweep cancels abandoned waits. Startup recovery interrupts
unfinished runs without replaying engine work; terminal snapshots remain readable
until expiry. The host runs expiry sweeps at startup and once per minute.
No CLI flags change these constants in this slice.

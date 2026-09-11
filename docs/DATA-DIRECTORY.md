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
| `runs/<key-id>/images/<run-id>` | Generated PNG/JPEG: up to 8 MiB each, 7-day expiry, separate 256 MiB per-key budget (oldest evicted first). See [image hosting](IMAGES.md). |
| `config.json` | Remembered `serve` flags and the active `profile_install` reference. |
| `profiles/downloads/` | SHA-256-addressed verified downloads and resumable `.part` files; no system installation. |
| `profiles/trees/` | Private extracted artifact/model trees; successful setup retires trees the new manifest does not reference. Failed checks retain verified downloads. |
| `profiles/install-<sha256>.json` | The versioned installation record: profile digest, canonical paths, file hashes, symlink targets, external ownership, unavailable reasons, and materialized commands. Config points to the active record. |
| `profiles/members/<id>/` | Generated engine config, isolated HOME, and `engine.log` (wraps at 1 MiB). |
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
attempt trajectory, captured file bytes, approval question text and answers,
lifecycle metadata and the last 256 lifecycle events. Directories
are private (0700), files 0600. Only key IDs are stored as identity; bearer secrets
are never saved in run metadata. Run contents are user data, independent of the
optional prompt logging in `usage.jsonl`. Corrupt snapshots fail closed for that
key; they are never silently replaced with an empty store.

The fixed initial bounds are 1 MiB input and 1 MiB output per step, 64 MiB of stored
snapshot data per key, 100 retained runs per key, 16 live runs per key and 64 live
runs per host. Additions refuse at a bound rather than trim existing work. Terminal
content is kept for at most seven days after completion or cancellation, or until the host owner clears it. A run's maximum live
age is 24 hours; the sweep cancels abandoned waits. Startup recovery interrupts
unfinished runs without replaying engine work; terminal snapshots remain readable
until expiry. The host runs expiry sweeps at startup and once per minute.
No CLI flags change these constants in this slice.

Ordinary writes leave 64 KiB of terminal headroom. If a legacy full snapshot cannot
retain a new terminal output, it receives an explicit `output not retained: budget`
marker and a minimal terminal/usage update. That update may exceed the ordinary
budget by at most 4 KiB per run, with a 400 KiB maximum exception per key.
A private per-run byte tally in the same snapshot survives restart and is never
renewed by later lifecycle writes; 512 bytes of that allowance are reserved for
terminal settlement. If the requested lifecycle update cannot fit, the run ends
Failed with `storage exhausted`, the refusal is logged, and dispatch/approval does
not continue. At the absolute cap this terminal write may discard the oldest
replay-ring entries (old cursors receive Reset), never accumulated attempts, usage,
captured outputs or image metadata. Newly proposed retention is refused. Expiry
removes the tally with its run; a rejected new-run admission does not use this path. Disk or
directory failures still refuse that key and are logged; they do not refuse host
startup or stop expiry for healthy keys. Snapshot bodies load on first use and are
released from memory after five idle minutes with no live run or subscriber.

Image runs keep PNG/JPEG bytes in `runs/<key-id>/images/<run-id>`, with private
permissions, and only output metadata in the run snapshot. Each output is at most
8 MiB. Images have a separate 256 MiB per-key budget: oldest outputs are evicted
first, independently of the 64 MiB state ceiling. Outputs are kept for at most seven days, or until the host owner clears stored data for the key;
Discard removes an output immediately. The run metadata remains with an output-gone
marker until the run record expires or the owner clears the run. Cleared runs return not-found. Startup sweeps drain proven cleanup paths;
interrupted work is never automatically replayed.

The image budget counts retained, servable outputs. Stale files that cannot be unlinked are logged and retried on the next sweep; they may temporarily add disk usage outside that budget. `infercat status` reports proven paths awaiting cleanup and observed orphans separately as for review. Report-only observations never authorize automatic
deletion; proven retry paths retain their existing unlink permission.

Shrinking run-snapshot commits are exempt from the retained-byte budget, including
expiry above the ordinary ceiling. Bounded lifecycle records cover Waiting as well
as terminal states; image artifact metadata and metering identities survive that
fallback. Budget refusal never marks a key corrupt. Expiry and run-removing clears commit all removed run IDs as cleanup intents before unlinking. Already-proven paths may also be retried directly without a commit. An empty snapshot cannot authorize a directory scan to delete
unknown image files; those are left intact and logged.

The console’s Stored section reports cached state-file bytes and servable image bytes per key. Clear removes terminal runs and their retained content/images, preserves live runs and outstanding workers, and reports files still awaiting cleanup; it does not remove the key or its workspace.

Warm Stored reads scan at most MaxRuns (100) records plus the host-wide tracked cleanup paths, without a filesystem scan. Rows and sizes are derived on read from the cached snapshot, including bounded retained payloads; only the encoded state-file byte count is cached at commit. A cold key also loads its bounded state file and scans its key directory for state-write temporary files. The ordinary ceiling excludes 64 KiB of terminal headroom; live reservations and any exceptional excess are shown separately. Clear persists validated cleanup run IDs atomically with removal and the epoch reset, drains only proven paths, and durably retires successful intents. With an empty snapshot, observed unknown image files are report-only entries, separate from proven retry paths: both count in status, but observing a file never authorizes deletion. The drawer names the report-only count; clear retries only proven paths. Only run removal resets the epoch; cleanup-only retries preserve the cursor. Missing directories are benign.

On the engineer’s Mac (three iterations), 100 runs with 25 MiB input measured 8.89 ms warm and 32.06 ms cold; a full commit measured 22.94 ms. With 100 small runs, reads measured 83 µs with no tracked cleanup paths, 173 µs with 1,000, and 1.081 ms with 10,000. Read cost includes serializing retained payloads for per-row sizes; commit adds only assignment of the already-encoded length.

At the ordinary ceiling (100 runs, about 63 MB encoded; three iterations), Stored reads measured 21.06 ms warm and 62.37 ms cold. These reads serialize bounded retained payloads for row sizes; they preserve the prior access time so polling does not pin a key in memory. Expiry persists proven cleanup intents with its shrink; a failed image metadata commit best-effort unlinks its own artifact and tracks failures as proven retries. Unnamed image files are collected when records remain; an empty snapshot leaves them for manual review.

The admin field `image_cleanup_pending` now counts proven retry paths only; older hosts included observations in that field. `image_orphans_for_review` carries the separate report-only count. The store aggregate accessor still sums both. Unnamed image files are collected when the snapshot has runs; only empty snapshots leave unknown files for review. Owned `.run-*` write-remnant files in both the state and image directories are collected on load or sweep; failed deletions are logged and retried on later scans.

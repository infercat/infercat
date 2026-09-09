English · [简体中文](DATA-DIRECTORY.zh-CN.md)

# Data directory

`~/Library/Application Support/infercat` (macOS), `~/.config/infercat` (Linux),
`%AppData%\infercat` (Windows), or `--data-dir`:

| File | What it is |
|---|---|
| `host.key.json` | Your host identity. Back it up; do not sync it; deleting it invalidates every invite you sent. |
| `keys.json` | Friends' keys as hashes (never the secret), their status and limits. |
| `usage.jsonl` | One line per request: key, endpoint, status, token counts, timings. No prompt content unless you run `serve --log-prompts`. |
| `admin.json` | Optional remote-console switch: SHA-256 admin-code hash and enabled-since time, mode 0600; never plaintext or a friend key. Removed when turned off. |
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
or malformed admin record refuses startup rather than silently replacing access state.

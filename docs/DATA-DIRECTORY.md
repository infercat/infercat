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

# Captured host responses

Captured 2026-09-09 from actual `infercat serve` binaries built from these refs:

- `0.1.0.json`: v0.1.0, `78fc87f03b7fca2575473daf35150df50a7d9707`.
- `0.1.1.json`: v0.1.1, `7f1539c6aef14246d86f6fe0a93346dcb7772087`.
- `current.json`: 078 v2 source based on main `e2f9b10` (audio routes with no audio engine configured).

Each binary used a new temporary `--data-dir`, `--dev-listen 127.0.0.1:19082`,
`--name 'Compatibility host'`, and a loopback test engine. The engine served
`/v1/models` with `compat-model`, `/props` with 8192 context, one slot and
`modalities.vision: true`, and healthy `/health`. We minted `compat-fixture`
with `keys add --json --no-qr`, then captured authenticated `GET /me`.
Only the random key id was replaced with `fixture-key`; no other response
values or fields were edited. No production host or user data was used.

The released shapes are identical. Current adds vision, nullable audio model ids, and daily audio limits. There is no `host.models_pinned` in the Go wire type: pins filter
`host.models`. Audio is optional in the client; hostAudio returns the configured model id or
null. The current fixture captures the unconfigured case. Live ASR/Kokoro proof
separately verifies configured model ids and requests without a model field.

`make host-compat` renders Connect → Chat with each response. Files are independent
of host vision, so the + stays present on old hosts but its accept list excludes
images. The capability accessor tests enumerate optional fields and prohibit
capability property reads outside api.ts. Refresh fixtures and expectations when
extending /me; retain every shipped-version fixture.

Post-deploy: `cd web && pnpm exec node dev/live-assert.mjs [URL]` (default /try).
Set INVITE to use a specific invite without printing it. The script observes the
empty chat's controls, so `listen: false` means no Listen button was rendered;
it does not infer speech capability from an empty history. It never sends a chat.

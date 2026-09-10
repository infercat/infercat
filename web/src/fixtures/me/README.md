# Captured host responses

The released fixtures were captured 2026-09-09 from actual `infercat serve` binaries:

- `0.1.0.json`: v0.1.0, `78fc87f03b7fca2575473daf35150df50a7d9707`.
- `0.1.1.json`: v0.1.1, `7f1539c6aef14246d86f6fe0a93346dcb7772087`.
- `current.json`: re-captured 2026-09-10 from 156 source based on main `7f2ccd8`, with a real sd.cpp image engine and two charged images.

The released binaries used a new temporary `--data-dir`, `--dev-listen 127.0.0.1:19082`,
`--name 'Compatibility host'`, and a loopback test engine. The engine served
`/v1/models` with `compat-model`, `/props` with 8192 context, one slot and
`modalities.vision: true`, and healthy `/health`. We minted `compat-fixture`
with `keys add --json --no-qr`, then captured authenticated `GET /me`.
In each fixture only the random key id was replaced with `fixture-key`; no other response
values or fields were edited. No production host or user data was used.

The released shapes are identical. Current adds vision, nullable audio model ids, and daily audio limits. There is no `host.models_pinned` in the Go wire type: pins filter
`host.models`. Audio is optional in the client; hostAudio returns the configured model id or
null. The current fixture has no audio engine configured. Live ASR/Kokoro proof
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

## Current capture (156)

Built the actual host from the 156 candidate on `7f2ccd8`. Used a new temporary
`--data-dir`, `--dev-listen 127.0.0.1:19156`, `--name 'Compatibility host'`,
`--models compat-model` and `--console off`. The loopback chat probe fixture used
port 18561 and the same `/v1/models`, `/props` and `/health` payloads above.
`--upstream-images http://127.0.0.1:18156` and
`--upstream-images-model FLUX.2-klein-4B-Q8_0` connected the native engine from the
[pinned image recipe](../../../../docs/IMAGES.md). This was the real engine, not a
fabricated image capability. No audio engine was configured.

Minted `compat-fixture` with 2 daily images / 8 queued; verified atomic over-budget
refusals, queued and running cancellation, and a successful synchronous generation.
Then `keys limits compat-fixture --daily-images 20 --max-queued-images 8` restored
the defaults, and authenticated `GET /me` was captured. `today_images` is 2 because
two real images were charged; a zero would be omitted by the host. Only `key.id`
was changed to `fixture-key`, as for released captures. Property order/whitespace
are JSON formatting, not substituted response values.

The proof and unmodified response are retained locally as
`/tmp/infercat-156-proof.json` and `/tmp/infercat-156-me-raw.json`. The re-capture
check compared parsed objects after replacing only that key id. Reproduce on an
isolated host; never copy an invite into a fixture.

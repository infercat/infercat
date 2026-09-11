# Captured host responses

The released fixtures were captured 2026-09-09 from actual `infercat serve` binaries:

- `0.1.0.json`: v0.1.0, `78fc87f03b7fca2575473daf35150df50a7d9707`.
- `0.1.1.json`: v0.1.1, `7f1539c6aef14246d86f6fe0a93346dcb7772087`.
- `current.json`: re-captured for 146 from `TestChatImageToolOffer` on base `ba895bd`: the real gateway HTTP handler with isolated loopback fixture engines, registered chat/agent kinds, an embedding destination, and one charged image. `INFERCAT_146_ME_CAPTURE=/tmp/me.json go test ./internal/gateway -run ^TestChatImageToolOffer$ -count=1` reproduces it. This is a dev capture, not a native-model proof.

The released binaries used a new temporary `--data-dir`, `--dev-listen 127.0.0.1:19082`,
`--name 'Compatibility host'`, and a loopback test engine. The engine served
`/v1/models` with `compat-model`, `/props` with 8192 context, one slot and
`modalities.vision: true`, and healthy `/health`. We minted `compat-fixture`
with `keys add --json --no-qr`, then captured authenticated `GET /me`.
In each fixture only the random key id was replaced with `fixture-key`; no other response
values or fields were edited. No production host or user data was used.

The released shapes are identical. Current adds vision, nullable audio model ids, and daily audio limits. There is no `host.models_pinned` in the Go wire type: pins filter
`host.models`. Audio is optional in the client; hostAudio returns the configured model id or
null. The current fixture has no audio engine configured and includes the separate embedding offer. Live ASR/Kokoro proof
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

## Current capture (151b-B)

Built the actual host from 151b-B on `d884df9` with the unmodified `apple-64g`
profile. A clean owned `--data-dir` ran real `setup` (published engine archives
fetched and verified; model overrides hash matched), then `serve --name proof-151b-b
--console off --dev-listen 127.0.0.1:49755`. The profile's 8080–8084 ports were
verified free first. The managed anchor was resident; BGE-M3 and sd.cpp started
on the first admitted request. ASR and speech were unavailable pending helper
publication. No shared host or user data was used.

A newly minted proof key used 60 rpm / 100000 tpm; its own `connect` served
127.0.0.1:49756. Chat and embeddings returned 200, and a real image job completed
with a stored 1024×1024 PNG. This authenticated `/me` was captured afterward;
`today_images` is 1, not a fabricated nonzero fixture value. Only `key.id` was
changed to `fixture-key`. The rebase onto 116c v3 retains its `agent: false`
fixture field; that field was not part of the managed-engine capture. All other
values are the actual response; formatting is not a substitution. The optional-field inventory includes `embeddings`, and
all shipped-version fixtures remain unchanged.

Raw capture, requests, setup/status logs and cleanup proof are retained locally
under `/private/tmp/infercat-proof-151b-b/`. The proof key was revoked, the owned
host and connect stopped, and all seven proof ports closed. No invite is stored
in this fixture or repository.

## Search fields (164)

The three additive search fields in `current.json` are a projection from the actual
164 candidate's authenticated `/me` after the owned E4B → Exa → sd.cpp turn:
`host_tools`, `limits.search_per_day`, and `usage.today_searches`. The remaining
fields retain the landed 146 gateway fixture; this fixture is therefore composite, not
claimed as a fresh whole-response capture. The full unedited 164 response is at
`/private/tmp/infercat-proof-164/me-after.json`. It reports one charged search and
both tools. The proof key was revoked and all owned processes stopped. Released
fixtures remain unchanged.

# Console action proof

`pnpm test` checks all six writes against success, API refusal and network failure, plus pending-close/once-card lifecycle, authoritative refreshes, draft/focus preservation, optional audio limits, clipboard failures and QR decoding. The QR encoder is pinned `qrcode-generator@2.0.4` (MIT, no runtime dependencies); its original license is shipped in `public/LICENSE-qrcode-generator.txt` and the release notices. It encodes UTF-8 locally at Medium correction, with four quiet modules. `jsqr@1.4.0` independently decodes the actual rendered SVG pixels in tests.

From `console/`, run `pnpm screenshots` to produce 40 EN/ZH states at 1280 and 390 px in ignored `test/evidence/`. Compare against `infercat-pm/docs/brand/console/`:

| Current evidence | Frozen reference |
| --- | --- |
| `mint-{width}-{lang}.png` | `mint-{width}-{lang}.png` |
| `confirm-{width}-{lang}.png` | `drawer-{width}-{lang}.png` |
| `revoked-1280-en.png` | `revoked-open-1280-en.png` |

The full sheet is captured at its content height. The additional `drawer-*` captures show the ordinary state before the explicit Revoke click; `mint-form-*` captures show the prefilled form. Expected differences: the QR carries the complete returned link and a quiet zone; limits come from the authoritative key GET, not static mock defaults; unknown event dates remain omitted; explanatory zero/model-selection hints and gender-neutral copy are intentional. Settings remain read-only.

Real-host proof (explicitly opt-in, launches only a fresh host and fresh friend data directories):

```sh
go build -o /tmp/infercat-075 ./cmd/infercat
cd console
INFERCAT_PROOF_BINARY=/tmp/infercat-075 node test/actions-live.mjs
```

It mints through the UI, connects through the real tunnel, and asserts answer → pause/403 → resume/200 → rotate/old-401/new-200 → revoke/403. It writes `test/evidence/actions-live.txt` and two live screenshots, then stops its host, friends and local engine. The screenshots contain disposable proof invites, never a real user's invite. No existing installation or data directory is used.

The two optional limits are `daily_audio_seconds` and `daily_speech_chars`, verified against the 078 key type. Each existing-key field appears only when returned by the API, including a returned zero; older hosts receive no unknown fields.

## 085 host-side proof

`pnpm test` also covers settings success/refusal/ambiguity, dirty drafts across polls, and local
admin-code enable/rotate/off with inline confirmation and pending-close secret disposal.
`node test/remote-screenshots.mjs` writes 28 local states at 1280/390 in EN/ZH. Compare `remote-settings-*`
with the frozen `settings-*`, `remote-admin-*` with `remote-on-*`, and `remote-off-*`/`remote-on-*` with
the corresponding Settings state/never-list mocks in `infercat-pm/docs/brand/console-remote/`.
The header and CLOSED remote-page states belong to 086 under the PM's split, not this fixture.
Intentional differences: bearer use is “in use”/“not in use,” no invented device count; the admin
card says browser support needs a newer web app until 086; the never-list describes the counts-only
API without claiming prompts cannot exist on disk when the host uses --log-prompts.

```sh
go build -o /tmp/infercat-085 ./cmd/infercat
go build -o /tmp/infercat-085-tunnel ./console/test/tunnel.go
cd console
INFERCAT_PROOF_BINARY=/tmp/infercat-085 INFERCAT_PROOF_TUNNEL=/tmp/infercat-085-tunnel node test/remote-live.mjs
```

The opt-in fixture launches only its own new host, engine and tunnel client. Curl traverses the
real relay/session, with a loopback proof adapter restricted to console routes plus read probes
of /me and /v1/models. It checks the remote limit change, live host name, admin-not-chat refusal,
remote console-address refusal, rotation, in-use state and off=404, then stops every process.
`remote-live.txt`, `remote-live-code.png` and `remote-live-state.png` are in ignored `test/evidence/`.
The temporary curl header belongs only to the disposable proof code, which is rotated and disabled.

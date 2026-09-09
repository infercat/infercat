# 072 preview evidence and v3 verification — 2026-09-09

The PM deployed `bridge/` on the infercat Cloudflare account as
`https://infercat-bridge-preview.yuanping-song.workers.dev`, Worker version `72f339a5`.
The v1 proof used host id `preview-072`, an independently started llama-server on loopback port
63272, and an isolated Infercat data directory. The launch host and other lanes' engines were
not used. The v1 final binary was built after rebasing onto `ea6793d`; it resumed the saved bridge
identity successfully without another registration. The Worker source did not change on rebase.

## Core evidence

The commands ran from the private proof directory. Header files contained only the disposable
friend bearer key and are deliberately not committed. `chat.json` selected the model returned
by `/v1/models`, `stream: true`, `max_tokens: 512`, and
`chat_template_kwargs: {"enable_thinking": false}`. The prompt was
“Reply with one short sentence confirming the bridge works.”

```sh
curl -sS --max-time 30 --header @final-friend-header.txt -o final-models.json -w '%{http_code}' https://infercat-bridge-preview.yuanping-song.workers.dev/h/preview-072/v1/models
# 200; one model: gemma-4-E2B-it-Q4_K_M.gguf

curl -sS -N --max-time 120 --header @final-friend-header.txt -H 'Content-Type: application/json' --data-binary @chat.json -D final-chat.headers -o final-chat.sse -w 'chat HTTP %{http_code}; first byte %{time_starttransfer}s; total %{time_total}s\n' https://infercat-bridge-preview.yuanping-song.workers.dev/h/preview-072/v1/chat/completions
# chat HTTP 200; first byte 0.109723s; total 0.218830s
```

Concatenating the SSE `choices[].delta.content` fields produced:

```text
The bridge is confirmed to be working.
```

The stream ended with `data: [DONE]`. The host recorded 19 prompt tokens and 9 completion
tokens, `via: "bridge"`, and a 200 status. No prompt logging was enabled.

```sh
/tmp/infercat-072-preview-final --data-dir /tmp/infercat-072-live.y8swd4/host keys revoke --yes k_50813c
# k_50813c (bridge-final-proof) is now revoked

curl -sS --max-time 30 --header @final-friend-header.txt -H 'Content-Type: application/json' --data-binary @chat.json -o final-revoked.json -w '%{http_code}' https://infercat-bridge-preview.yuanping-song.workers.dev/h/preview-072/v1/chat/completions
# 403
# {"error":{"message":"this key has been revoked by the host","type":"permission_error","code":"key_revoked"}}
```

Actual host request lines:

```text
02:12:29  bridge-final-proof  models  via bridge  0ms  ok
02:12:30  bridge-final-proof  chat  via bridge  0ms  403 key_revoked
```

`expose --off` printed `public endpoint disabled`; a subsequent public models request returned
503 `host_offline`. Both proof keys were revoked, the local bridge credential was removed by
`--off`, and the isolated host and engine were stopped. PM-owned preview resources remain for
review; the engineer did not provision or deploy Cloudflare resources.

## V1 verification and limits

- Final rebased Go race suite (`go test -race -json ./...`): 304 passed / 0 failed / 2 skipped.
  The skips are the existing opt-in `TestSavedAddrLive` and `TestLiveLlamaCPP`.
- Worker (`npm run typecheck`; `npm test`): 11 passed / 0 failed / 0 skipped; dry-run bundle succeeds.
- Clean `npm ci --legacy-peer-deps --ignore-scripts`: succeeds; audit 0 vulnerabilities.
- `go vet ./...`: passes. The pre-console-rebase `make check` passed (installer 18/0/0).
  The final `make check` failed in unchanged base test
  `internal/admin/console_test.go:115`, `TestOldCloseCannotRemoveSuccessorToken`, with
  “no running host found for this data dir”. A focused 30-run race check reproduced it twice
  (28 passed / 2 failed / 0 skipped). `git diff ea6793d -- internal/admin` is empty.
  This base defect is reported to the PM; no adjacent fix is included in 072.
- Local fixtures separately prove bridge/direct requests share gateway RPM admission and
  preserve friend revocation and trusted usage provenance.

Bounded framing, queueing, coalescing/backpressure, reconnect settlement, disable checks, and
one-time registration are covered locally. KV disable visibility is eventually consistent;
already-started responses are not promised immediate termination. Interrupted requests are
never replayed automatically. The PM's adversarial review is still required before landing.

## V2 — isolated job failures and upload admission

The v2 patch addresses the two review findings on base `e95a73b`. A response consumer cancelling
or stalling now ends only its job: the response stream and writer are errored, the Worker sends
that id's `cancel`, the host cancels the gateway request context, and the queue advances only
after the host replies `ready`. A host ack timeout uses the same handshake. New fixtures confirm
that cancellation reaches a real gateway upstream, releases its key admission, and allows the
next request on the same socket. An incomplete body is capped/read before admission, so another
completed request can use the host slot while that upload waits; upload timeout returns 408.

Actual v2 check results:

```text
go test -race -json ./...
307 passed / 0 failed / 2 skipped

npm run typecheck
passes

npm test
Test Files  2 passed (2)
Tests       17 passed / 0 failed / 0 skipped

make check
install tests: 18 passed / 0 failed / 0 skipped of 18 total
CHECK OK
```

The two Go skips remain the existing opt-in live tests named above. The v1 console-base flake
and its PM-approved exception remain recorded; no admin fix is included, and the v2 check passed.
The new Worker fixtures use controlled local streams for disconnect/backpressure and fake time
for upload deadlines, plus Miniflare for the cancellation handshake and next-job response.
The Go fixtures use local TLS WebSockets and the actual gateway handler. These v2 regression
scenarios in that initial freeze ran locally; the v1 preview proof above remains labeled v1.
The subsequently requested live v2 checks are recorded below.

**Accepted slice-1 limitation (ticket 072 ruling):** keyless requests still reach the per-host
60/minute window and four-deep queue, because friend-key validation is at the host. Someone who
knows the URL can exhaust those admission caps and starve friends. The later edge key-hash check
is outside this slice. This limitation is also stated in `bridge/README.md` and the PM's ticket.

## Follow-up live v2 checks

The PM deployed v2 commit `ae09083` to the same preview origin as Worker version
`2d429c00-32f1-4c97-93fa-acea19534904`. The matching host used a fresh isolated data directory
and its own llama-server on port 63272. Both checks used a disposable authenticated friend key.

**Drop a streaming client while another request is pending.** An HTTP 200 stream had emitted
content (`1`). A second authenticated request was sent and remained pending before the first
client connection was closed. It subsequently returned HTTP 200, `QUEUE_OK`, and `data: [DONE]`.
The host recorded `client_closed` for the first request and `ok` for the second, both `via bridge`.
The configured acknowledgment timeout handled the disconnect: the pending request finished
30.355 seconds after the drop. Host-session reconnect count during the check: **0**.
This proof establishes isolation across the configured timeout; it does not claim instant
client-disconnect notification from Cloudflare.

**Complete another request while an upload is incomplete, then enforce its cap.** The client
sent only the first 16 bytes of a body and held the remainder while another request completed.
That other request returned HTTP 200 and `QUEUE_OK` after 0.834 seconds. The upload was then
completed at 4,194,524 bytes and received HTTP 413 in 1.436 seconds total:

```json
{"error":{"message":"body_too_large","type":"bridge_error","code":"body_too_large"}}
```

Only the ordinary request created a gateway usage event; the oversized upload never entered
the gateway. Host-session reconnect count: **0**. A separate unfinished Content-Length probe
ended with an edge TCP reset, so HTTP 408 visibility was not established in that live probe;
the deterministic local deadline fixture verifies 408. The requested live cap proof is the
413 result above.

The disposable proof key was revoked, `expose --off` removed the bridge credential, and the
isolated host and engine were stopped. The engineer consumed the PM-issued preview code and
sent these bounded preview requests; the PM owned infrastructure deployment. No other lane's
engine or the launch host was used.

## V3 upload admission bound

The object reserves one reader and its full 4 MiB buffer before `bounded()` runs: **4 readers,
16 MiB aggregate reserved upload capacity**. It refuses excess admission immediately with
429 `host_busy` and releases the reservation in `finally` after success, timeout, oversize, or
read error. Bodyless requests need no upload reservation. Fixed buffers and one cancellation
deadline per reader replace the growing chunk list, repeated timeout races, and final body copy.
Completed bodies transfer to the separately bounded active/queued job storage.

The controlled-stream fixture holds four uploads open, checks 16 MiB reserved capacity, and
gets 429 on the fifth upload without advancing time. A bodyless request still completes.
Error and timeout paths free their reservations; oversize and successful-read fixtures do too.
These concurrency-budget checks run locally. No concurrent memory-pressure scenario was run
on the public preview. The accepted unauthenticated-admission limitation still applies to these
new upload reservations; the later edge key-hash check remains out of scope.

Final v3 checks on base `9f4d2e1`:

- Go race suite: **379 passed / 0 failed / 2 skipped** (existing opt-in live tests).
- Worker typecheck and dry-run bundle: pass; Worker suites: **19 passed / 0 failed / 0 skipped**.
- `make check`: **CHECK OK**. Included console **68/0/0**, client **27/0/1** (existing opt-in
  integration fixture), browser compatibility **9/0/0**, and installer **18/0/0**, expressed as
  passed/failed/skipped; vet, lint, shipped-shape and launch-asset checks passed too.
- The current base includes ticket 077's admin lifecycle fix. The old v1 base-gate exception is
  historical; `internal/admin` remains unchanged by 072.

The v3 rebase preserved the newer model pinning, audio refresh, remote-console settings, session
interface, and audio usage fields while retaining the bridge handler and recorder hooks.

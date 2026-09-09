# Public bridge — ticket 072

a public URL decrypts TLS at the edge and in our object; the tunnel mode's "nobody in the middle" does not carry over.

The peer-to-peer path stays free and unmetered. This bridge has no billing. The host's running
`serve` process owns the outbound WebSocket and passes requests through its existing
`Gateway.Handler()`; friend authentication, revocation, admission, queue, and usage are shared
with tunnel requests. `via: "bridge"` is set from trusted local request context, never a client header.

## Host

Start your host with its own data directory. With a PM-issued registration code:

```sh
infercat --data-dir /path/to/host expose --register CODE
infercat --data-dir /path/to/host expose
infercat --data-dir /path/to/host expose --off
```

Registration requires a running host. The command stores the bridge credential in `bridge.json`
with mode 0600, then POSTs the existing local admin `/reload`. Startup and reload read the same
file; unchanged configuration preserves the socket, a changed configuration cancels and joins
the old client before starting another, and a missing file stops the client. An unreadable or
invalid file refuses reload without replacing the previous bridge state. `--off` removes the
file and reloads; if reload fails the command reports the failure and must not be treated as
confirmation that the running connection stopped. Enabling again after `--off` needs a fresh code.

The printed base URL is `https://gateway.infercat.ai/h/HOST/v1`. Use the existing friend key as
its bearer token. The bridge credential is a separate secret and never authorizes inference.
The launch host is not part of this slice's testing.

## Protocol and bounds

The Worker routes only `GET|POST /h/HOST/v1/*` and the authenticated host socket `/h/HOST/socket`.
One Durable Object, selected by `idFromName(HOST)`, owns each host. Idle sockets use the hibernation
API; automatic `ping`/`pong` JSON responses keep idle hosts hibernatable. The host sends a ping
at 25 seconds and reconnects after 60 seconds without receiving any frame. Backoff grows from
1 to at most 32 seconds and resets after a connection lasts a minute.

All messages are JSON text with `type` and a per-request `id`. The sequence is:

1. Worker → host: `request` (method, relative path/query, headers), zero or more `body` chunks,
   then `end`. Request headers are only `authorization`, `content-type`, and `accept`.
2. Host → Worker: `response` (status, headers), zero or more `data` chunks, then `end`.
   Response headers are only `content-type`, `cache-control`, `retry-after`, and `x-request-id`;
   the Worker forces `cache-control: no-store`.
3. Worker → host: `ack` after each response chunk reaches the response stream; `ready` after
   the response ends. The next request follows `ready`. An unfinished request is never replayed.
4. On a client abort, response-writer error, or request deadline the Worker sends `cancel` for
   that request id. The host cancels that handler's context, joins it, and answers `ready` before
   another request starts. A host-side ack timeout cancels its request context and sends `cancel`;
   the Worker echoes cancellation and waits for the same `ready` handshake. In-flight frames for
   the cancelled id are drained. The response stream is errored as well as its writer aborted,
   releasing a write blocked by backpressure without closing the host socket.

Chunk data is base64, at most 512 KiB decoded, keeping each entire encoded message below 1 MiB.
Requests are fully read before queue admission, with a 4 MiB aggregate bridge cap
and a 30-second upload deadline; oversize and timed-out uploads receive 413 and 408 respectively.
An incomplete upload never holds the host's execution slot. Before reading, the object reserves
one of four reader slots and the full 4 MiB buffer capacity, with a 16 MiB aggregate upload-buffer
budget. Excess readers/bytes receive 429 `host_busy` immediately; Content-Length is not trusted
to size the reservation. Every completion, timeout, oversize refusal, or read error releases the
reservation in `finally`. Bodyless requests need no upload reservation. Each reader uses one fixed
buffer and a single cancellation deadline, with no growing chunk list, per-chunk timeout race,
or final full-body copy. Completed bodies transfer to the separately bounded queue. The
bridge permits one active request and four waiting requests per host; overflow is 429 `host_busy`.
The Durable Object's persistent fixed-minute counter permits 60 public requests per host/minute,
also returning 429 `host_busy`. It counts admission attempts, including offline requests, and
survives reconnect/hibernation. This is the ticket's per-host edge rate limit; no separate WAF rule.

A queued or active request has a 180-second bridge deadline. A public client's cancellation,
writer error, or stalled-reader ack timeout settles only that job and preserves queued requests
and the socket. The host cancels its gateway/upstream request context, so generation stops and
key admission is released. A host that fails to acknowledge cancellation within 30 seconds has
violated the protocol and is disconnected. Socket errors, invalid host frames, disable, and
credential replacement still close the session and settle outstanding requests; none are replayed.
The host coalesces token flushes at approximately 100 ms or a full chunk, with one unacknowledged
chunk at a time and a 30-second acknowledgment deadline. An interrupted public client must retry
deliberately.

**Accepted slice-1 admission limitation:** friend keys are validated only at the host. Unauthenticated
requests reach the per-host rate window, upload-reader budget, and four-deep queue; someone with the public URL can
consume that capacity and starve friends. Per-job cancellation and pre-queue body reads do not
solve that separate limitation. An edge key-hash check is deferred to a later slice, as recorded
in ticket 072's review ruling.

## PM deployment and registration

Only the PM deploys previews and production under the infercat Cloudflare account.
Deploy matching Worker and host builds for the cancellation handshake. There is no
account id or production resource credential in this package. `wrangler.toml` has a deliberately
nonfunctional KV placeholder. The PM supplies the infercat namespace binding and a distinct
preview Worker name. Do not deploy this unchanged or create resources under another account.

The KV namespace contains `reg:CODE` → host id and `disabled:HOST` → any nonempty value. Host ids
are 1–64 ASCII letters, digits, underscores or hyphens. Codes must be unique, unpredictable,
16–128 URL-safe characters, and never remapped to another host. A code is consumed transactionally
in the host's Durable Object, even if its KV mapping is cached; token hashes and consumed-code
hashes survive hibernation. A fresh code for an existing host rotates the credential and disconnects
the previous socket. Keep used-code records; deleting them could make retained KV codes reusable.
Registration replies carry `cache-control: no-store`. An uncertain registration response or local
save failure needs PM recovery with a fresh code, never automatic replay.

KV disable changes have Cloudflare's eventual-consistency delay. Disable is checked on every
public request, host socket admission, and registration; a public request seeing disable closes
the current socket too. It does not promise instant termination of an already-running response.
See [KV consistency](https://developers.cloudflare.com/kv/concepts/how-kv-works/) and
[WebSocket hibernation](https://developers.cloudflare.com/durable-objects/best-practices/websockets/).

For a preview, build the host with the preview origin fixed into the binary:

```sh
go build -ldflags '-X main.bridgeEndpoint=https://PREVIEW.workers.dev' -o /tmp/infercat-072 ./cmd/infercat
```

This is a build-time value, not a new user-facing option. Use an isolated data directory and
register only after the PM has deployed the branch and supplied the preview code. The PM owns
promotion to `gateway.infercat.ai` and the demo-host operation.

## Local verification

Node 22+ and the repo's pinned Go version:

```sh
cd bridge
npm ci --legacy-peer-deps
npm run typecheck
npm test
cd ..
make check
```

`npm test` bundles with `wrangler deploy --dry-run`, then runs real Miniflare integration tests.
It does not create Cloudflare resources. Miniflare uses Wrangler's matching published version;
the `sharp` patch override addresses a development dependency advisory. The Worker bundle has
no third-party runtime dependencies. The host tests cover shared gateway limits/revocation,
usage provenance, framing at the cap, coalescing/backpressure, reload/reconnect/off, credential
persistence and CLI control. V2 fixtures additionally cover client disconnect mid-stream, a stalled
response consumer, queued abort, host ack-timeout cancellation, real gateway upstream cancellation,
and upload completion/timeout before admission. The v1 real-preview evidence is retained in
[the verification record](../docs/BRIDGE-072-VERIFICATION.md); v2's focused adversarial review remains
a gate. The verification record also labels the later, bounded v2 preview checks separately from
v3's local concurrency-budget fixtures. The v3 fixtures check four simultaneous readers,
16 MiB reserved capacity, immediate excess refusal, and release on every exit path.

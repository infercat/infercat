# @infercat/client v0

**Not yet published.** A private workspace package; build it with
`cd web && pnpm install --frozen-lockfile && pnpm --filter @infercat/client build`.
ESM and TypeScript declarations are emitted in `packages/client/dist/`.

In a browser app, with the invite your friend gave you:

```ts
import { connect } from '@infercat/client';
const session = await connect(invite);
const models = await (await session.fetch('models')).json();
```

Close the session when you are done: `session.close()` cancels outstanding requests and
streams and releases the tunnel. Calling it again is harmless. Nothing is stored by the library.
`session.privateKeyJSON` can be kept by the caller to reuse an identity through
`connect(invite, { privateKey })`; do not run concurrent sessions under the same private key.

## Official OpenAI SDK

Install `openai` in your app (v0 is tested against 6.27.0), then:

```ts
import OpenAI from 'openai';
const client = new OpenAI(session.openai());
try {
  const models = await client.models.list();
  const stream = await client.chat.completions.create({
    model: models.data[0]!.id,
    messages: [{ role: 'user', content: 'Hello!' }],
    stream: true,
  });
  for await (const event of stream) {
    console.log(event.choices[0]?.delta.content ?? '');
  }
} finally {
  session.close();
}
```

`openai()` returns `{ baseURL, apiKey, fetch, dangerouslyAllowBrowser: true, maxRetries: 0 }`.
The credential is your friend's scoped Infercat invite secret, **not an OpenAI API key**.
The helper enables browser use explicitly and disables SDK retries to avoid replaying an
ambiguous generation. Keep the invite private; only give it to browser code you trust.
This connects the SDK to your friend's gateway; it does not make an OpenAI API request.

## Fetch, status, and errors

`baseURL` is `http://host/v1`, a logical address routed through the encrypted tunnel; there is
no DNS or plaintext network request to `host`. `fetch` has the global fetch signature and accepts
strings, URLs, or Requests with RequestInit overrides. `models` resolves to `/v1/models`;
`/me` resolves to `/me`. Absolute URLs must have origin `http://host`; other origins and URL
credentials are refused before dialling. The invite's Authorization header always wins.

The adapter buffers uploads through the standard Request body encoder (including FormData and
Blob); response bodies stream. It provides gateway HTTP/1.1 semantics: no cookie jar, HTTP cache,
redirect following, or connection pooling. Redirects are returned as responses. Aborting a
request or cancelling its response stream closes that request's connection. `close()` aborts all
outstanding requests, and subsequent requests reject with AbortError.

`fetch` preserves native HTTP error semantics: a 4xx/5xx is a Response, not a rejected Promise.
The official SDK maps those responses to its own APIError, preserving the gateway's `code`.
`me()` returns a typed `Me`, with a ten-second deadline, or throws `GatewayError` with `status`,
`code`, `type`, `message`, and optional `retryAfterS`, `limit`, and `inFlight`.
`GatewayErrorCode` lists known wire codes; unknown future codes remain unchanged.
The exported `gatewayError(response)` consumes an error response body when manual mapping is
wanted. Transport failures and invalid invites remain ordinary Error/TypeError, not invented
gateway codes. `connect()` authenticates with `/me` and closes the session if authentication fails.

`status` is `{ kind: 'relayed' | 'direct', rttMs, via }`, measured at connection time, matching
the app's path information. It is not a live health monitor. `me().host.log_prompts` reports whether
the host enabled prompt logging.

## Runtime and artifact coupling

**Browser-only v0.** Use a modern browser with WebAssembly streaming compilation, fetch,
WebSocket, ReadableStream, DecompressionStream, AbortSignal.any and AbortSignal.timeout.
This bridge uses DERP over WebSocket; it does not use WebRTC and needs no RTCPeerConnection.

Node 22 has fetch and WebSocket, but the current Go 1.27 js/wasm runtime explicitly disables
net/http's fetch path when `process.argv0` starts with `node` (`src/net/http/roundtrip_js.go:50–68`).
Tailcat needs that path to fetch its DERP map. The app loader also needs a document to load the
Go runtime script. v0 refuses Node before starting the bridge; it installs no polyfills and
never disguises the runtime. Supporting Node requires a separately tested bridge/runtime change.

By default, `connect` loads `https://infercat.ai/v/<package-version>/infercat.wasm.gz`
and its sibling `wasm_exec.js`. The version comes from this package's own metadata
and is bundled into the client; it does not follow the mutable root URL. The site's
`/v/` directory mirrors the current version only; it is not a release archive.
Pass `{ wasmURL: 'https://your-assets.example/v0/infercat.wasm.gz' }` (or a raw `.wasm` URL) to
choose an exact artifact; put its matching Go `wasm_exec.js` beside it. Serve wasm with CORS access
for your app, and allow the runtime script and wasm compilation in your CSP. A response already
HTTP-decoded from gzip is not decompressed again. Explicit artifact URLs never fall back to a
different version. One bridge boots per page; later sessions reuse it, so choose the artifact
before the first connection (including any existing app connection).

The library depends on the `ic1` invite format and `InfercatTunnel` contract from the same Infercat
checkout. `make deploy-web` stages the matching pair at `/v/<product-version>/`
and keeps the root paths and the app's `/runtime/<product-version>/` paths.
`make check` and deployment refuse mismatched client/product versions. Development
versions such as `0.1.2-dev` can be rebuilt; a version-addressed development URL is
not an immutable content pin. The GitHub release is the immutable home for a tagged
release's `infercat.wasm.gz` and matching `wasm_exec.js`. Production clients should
set `wasmURL` to that release's asset URL, for example
`https://github.com/infercat/infercat/releases/download/v<VERSION>/infercat.wasm.gz`,
or download and self-host the pair with CORS enabled for their app.
For an archived checkout, build with `make wasm` and
host `infercat.wasm[.gz]` and `wasm_exec.js` together, then pass its exact `wasmURL`.
`derpMapURL`, `onLog`, and `onWasmProgress` are optional connection settings; progress is a percent
or null when the download total is unknown.

## Trust

Encrypted end-to-end from your device to your host’s computer — the relay in between can’t read
it. Infercat records counts, never text. The model runs on their machine.

If `me().host.log_prompts` is true, this host has prompt logging on, so everything you send and
everything the model answers is written to a log on their machine. The host runs the model and
can read its inputs and outputs; end-to-end encryption protects the path to that host.

## Verification

`make check` builds, typechecks, lints, and tests this package along with the host checks.
Unit tests use a fake bridge and the official SDK. The integration test uses Chromium, the built
package, and the real wasm bridge to list models and stream a chat reply through the supplied host.
It is explicitly skipped when `INFERCAT_TEST_INVITE` is unset.

```sh
make wasm
cd web
pnpm --filter @infercat/client build
# Install Chromium if this machine has not already installed it:
pnpm --filter @infercat/client exec playwright install chromium
INFERCAT_TEST_INVITE='<invite from your isolated test host>' pnpm --filter @infercat/client test
```

Use your own `infercat serve --data-dir <temporary-directory>` and a key minted in that same
directory. The test never discovers a default host or reads its keys. Keep the invite out of
committed files and shell history. A deterministic fixture engine behind that real host verifies
the transport; it does not claim to validate model inference quality.

## Gateway contract

`src/contract.ts` is the shared, type-only wire contract consumed by this package
and the web app. The public exports include `Me`, `Limits`, `GatewayErrorBody`,
`GatewayErrorPayload`, and `GatewayErrorCode`. Audio budgets and `host.audio` are
included. `host.audio` and `host.vision` remain optional for older hosts; a vision
map value of `null` means unknown, and an audio model of `null` means unavailable.
The web fake backend checks its actual `/me` constructor against the same type.
The app keeps localized error text and raw-body diagnostics; this package keeps
native HTTP status behavior in `fetch` and typed gateway errors in `me()`.

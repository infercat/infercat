# Console compatibility captures

`current-085-dev.json` was captured from an isolated `0.1.2-dev` host using the 085 API, by
`web/dev/console-live.mjs` (CAPTURE_CONSOLE_FIXTURE=1) during 086's real WASM/relay proof. It is not labelled as a shipped
release. It contains only the public status/keys/engine/settings/today/week responses; no bearer,
local token, invite secret, or prompt text. The data directory, tunnel address and ports belong
to the disposable proof host.

The compatibility runner also removes optional metadata, uses a null model list and null per-key
usage to exercise graceful fallback. It tests 401, 404 and read-budget backoff, and explicitly
asserts that admin entry never uses remembered chat credentials. Add a pinned 0.1.2 capture when
that version ships. Keep this dev capture rather than rewriting it to pretend it was released.

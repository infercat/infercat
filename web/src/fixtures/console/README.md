# Console compatibility captures

`current-085-dev.json` was recaptured during 094 from an isolated `0.1.2-dev` host on the
090 base, using `console/test/connected-live.mjs` with CAPTURE_CONSOLE_FIXTURE=1. The original
filename denotes the API family, not a released host version. Two fresh tunnel peers authenticate
with one friend key: its status reports `connected: true, sessions: 2`. Local and remote console
views are checked against that fact. Per-peer tunnel identity rows are omitted from the capture.
It contains only public status/keys/engine/settings/today/week responses; no bearer, local token,
invite secret or prompt text. The data directory, host address and ports belong to the disposable
proof host. The original no-connected-fields shape remains in `console/test/fixture.json` and
its local/remote rendering is explicitly tested.

The compatibility runner also removes optional metadata, uses a null model list and null per-key
usage to exercise graceful fallback. It tests 401, 404 and read-budget backoff, and explicitly
asserts that admin entry never uses remembered chat credentials. Add a pinned 0.1.2 capture when
that version ships. Keep this dev capture rather than rewriting it to pretend it was released.

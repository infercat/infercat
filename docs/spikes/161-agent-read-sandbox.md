# 161: per-run read sandbox verification

Local review freeze; Linux runtime verification and public publication remain pending.

The same per-run launcher owns preflight, the child, its private workspace/tmp,
and final cleanup. The inherited native SandboxProvider is materialized only
inside that path. It substitutes the nested launcher, preserving the native
policy, approval and filesystem services. Unsupported policy/root combinations
are refused rather than described as enforced.

## Mac proof

Actual product tree, no build overlay. Pinned harness 0.1.5-alpha.1, Node 22.23.2.

```
INFERCAT_AGENT_TEST_INSTALL=/path/to/install go test -race -v ./internal/agent \
  -run '^TestPinned161|^TestPinnedNativeApproval|^TestPinnedAdapter|^TestPinnedForceStop|^Test161|^TestPinnedCompositionDisables' -count=1
```

The lifecycle/canary command passed 8 top-level tests (plus the two Allow/Deny
subtests), 0 failed, 0 skipped. The separately run pinned provider policy test
passed 1/0/0: exact workspace accepted; missing confinement marker, read-only,
danger-full-access and another root refused. Native read/bash/Python denials are
kernel denials against synthetic host data, while commands and writes inside the
workspace succeed. The Exa sentinel is absent from the subprocess environment.
The native Allow/Deny fixture uses real approval messages; Allow does not expand
the outer boundary. Captures remain readable after child join and workspace removal.

The E4B route proof uses an isolated host, fresh local ports and its own
llama-server process (Gemma-4-E4B Q4_K_XL). Five probes passed: native read denial,
bash denial, Python denial, credential absence, and write capture. All 16 model
attempts match usage rows on key/time/model/token counts/status; all 47 SSE step
replacements reduce to the GET step lists. Every run ended Done, including the
expected failed read step, and no workspace remained after settlement. Both owned
processes were stopped. No production host or another lane's engine was used.

See [summary](161-sandbox/mac-proof.json), [usage identities](161-sandbox/mac-usage.json),
[pinned output](161-sandbox/mac-pinned.log) and [route output](161-sandbox/mac-e4b.log).

## Linux proof boundary

The Linux launcher cross-builds. CI is configured to require the Landlock canaries
and run the real pinned suite on Linux, including the parent-environment sentinel;
ABI below 3 fails, with no unconfined fallback or CI skip. That job has not run:
public branch publication was rejected by automatic approval review and awaits
payload-specific approval. Mac success is not evidence of Linux kernel enforcement.

The allow-list and honest limitations are in [AGENT-RUNTIME](../AGENT-RUNTIME.md).

## Local gate

`make check` completed with `CHECK OK`: console 105 passed; adapter JavaScript
9 passed / 0 failed / 0 skipped; Worker 59 passed; host compatibility 57 passed;
installer 18 passed / 0 failed / 0 skipped. The client package reported 30 passed
and one pre-existing opt-in skip. `go test -race ./...` passed (run package
250.455 seconds). `go vet ./internal/agent ./internal/supervise ./cmd/infercat`
and `GOOS=linux GOARCH=amd64 go build ./cmd/infercat` passed.

The full local command log is retained beside this report. It is local evidence,
not a claim that the Linux runner has executed the confinement tests.

## Real search descriptor and 129 task

The separate real E4B search/write probe passed through the routes: live Exa
search via the inherited descriptor, then a two-line `notes.md` captured before
the workspace was removed. The run ended Done with five completed steps; all
four model attempts match usage identities, and SSE replacements match GET.
The host and engine were stopped afterward. The public evidence contains no
credential. See [result and metering](161-sandbox/mac-search-write.json) and
[command output](161-sandbox/mac-search-write.log).

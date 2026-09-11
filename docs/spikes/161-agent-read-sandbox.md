# 161: per-run read sandbox verification


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
ABI below 3 fails, with no unconfined fallback or CI skip. Publication is now
authorized and the branch is being verified in CI. The replacement must pass
Linux execution before landing; Mac success is not evidence of Linux enforcement.

The allow-list and honest limitations are in [AGENT-RUNTIME](../AGENT-RUNTIME.md).

## Local gate

On the initial reviewed checkpoint `3777a14`, `make check` completed with `CHECK OK`: console 105 passed; adapter JavaScript
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

## Review delta and file-backed key

The replacement is rebased onto 164's landed `470666f`. Sandbox preflight retains
stderr and the wrapped execution error, uses platform-specific advice, and names
a timeout. Public unconfined harness entry points are removed; the old IPC-only
fixture has an explicitly test-only helper. Linux handles only filesystem rights
in the pinned headers and grants `/proc` reads for descendant tools; a required
Node-to-Python grandchild canary reads `/proc/self/status` on Linux. Linux execution
is still pending CI; the prior PID-only rule's inability to cover that descendant
was inferred from its scope, not claimed as a measured Linux run.

The Exa input now shares 164's startup-loaded `search.key_file` snapshot. No agent
code reads `EXA_API_KEY`. With no configured file, no native Exa provider is
registered. The file-backed key goes only into memory and the inherited pipe,
not config or child environment. Operators must keep other secrets out of the
host environment because Linux `/proc` exposes same-uid environments subject to
ordinary process permissions.

The replacement's pinned Mac suite passed 11 top-level tests plus two approval
subtests, zero failures/skips. It includes the real confined lifecycle, read/bash/
Python denials, file-key composition, exact-root provider checks, and diagnostic
fixtures. Search snapshot regressions passed under race. The live E4B proof
returned an explicit search failure with no configured file, then completed the
real search/write task after startup with a configured key and the file deleted.
The key was absent from the host environment; all model attempts match usage
identities; captures outlived the removed workspaces and both owned processes
stopped. See [file-key evidence](161-sandbox/mac-file-key.json),
[route output](161-sandbox/mac-file-key.log), and
[pinned delta output](161-sandbox/mac-review-delta.log).
The final gate result is supplied in the freeze handoff; no Linux success is
claimed by these Mac records.

## Resolver delta after the initial Linux measurement

The initial published checkpoint's Linux CI 34569253847 failed the external DNS
canary: `tool-ok`, `bash-ok`, `python-ok`, `node-ok`, and owned-TLS `https-ok`
preceded `curl exit status 6`. The pinned harness step was not reached. This is
DNS failure evidence, not confirmation of a particular resolver symlink target.
The [initial output](161-sandbox/linux-initial-canary.log) is retained verbatim
apart from trailing whitespace.

The ruled replacement checks `/etc/resolv.conf`, `/etc/hosts`, and
`/etc/nsswitch.conf` on each launch. Only symlinks resolving outside the existing
allowed trees to readable regular files gain READ_FILE on the resolved leaf.
Missing/unreadable targets are omitted. The O_PATH helper checks the opened inode
is still regular, so no directory substitution can grant a subtree. `/run` itself
is never granted. The Linux fixture logs both the resolved target and chosen leaf;
`curl --show-error` retains DNS diagnostics instead of only the exit code.

The resolver selection regression covers regular/missing/in-tree/directory targets,
unreadable files where ordinary permissions deny the read, and a changed symlink
between launches. The updated Mac native/canary command passed 11 top-level tests
plus two approval subtests, zero failures/skips; see
[delta output](161-sandbox/mac-resolver-delta.log). Linux execution of the final
replacement is still required; these local checks do not claim that DNS or the
pinned harness has passed on Linux.

# Agent runtime

The runtime foundation is installed and supervised by Infercat. Agent run routes,
key opt-in and native event/model integration belong to 116c; the 116b shared run
mechanism does not expose an agent route to friends.

```sh
infercat agent install --data-dir /path/to/host
infercat serve --data-dir /path/to/host --agent
```

Installation is one command on Darwin and Linux (arm64 or x64). It installs a
private Node 22.23.2 and DeepSeek Harness 0.1.5-alpha.1, source revision
`5dda764ed3aa172535a7967b06ff95d9cbfe536a`, with the matching Exa plugin.
Windows reports an unsupported agent runtime; ordinary serving is unaffected.
No system Node installation or system service is changed.

The installer verifies the platform archive against pinned official Node SHA-256
values and every npm package against the retained lockfile's SHA-512 integrity.
The lockfile SHA-256 is
`9b053e130bd71c2950eb106f6ca7ad9043368d969532ae233df1c0579b75b90d`.
It uses `npm ci --ignore-scripts`, then invokes the pinned harness's helper script
that sets its bundled subprocess helper executable. The complete tree is published
by rename; a second install of that complete tree does nothing. Package licenses
remain in the installed tree. The pinned harness is a developer preview, with its
upstream safety notice unchanged; it is not a verified per-friend isolation boundary.

Files live below the host's `agent/` directory:

- `runtime-0.1.5-alpha.1-node22.23.2/`: pinned Node and integrity-locked packages.
- `host/`: our adapter plugin and native composition patch, harness configuration, and
  `runtime.log` (at most 1 MiB, replaced on restart).
- `npm-cache/`: installer cache, outside retained run data.

`--agent` is off by default and is not remembered. `/status.agent` is absent when
off; when enabled it contains `state`, optional `pid` and `last_error`, and
`restarts`. States are `starting`, `healthy`, `backoff`, `failed`, and `stopped`.
Healthy means the native plugin is ready on its private health listener. Missing
installation, an unsupported OS, or a runtime failure never stops normal serving.

One guardian owns the runtime process group. Only Infercat owns the write end of
the guardian's lifetime pipe. Host death closes that pipe: the guardian sends TERM,
waits up to two seconds, then kills the group. The supervisor allows a further
one second to join cleanup. This covers host death, including SIGKILL; it does not
cover simultaneous guardian death or descendants that escape the process group.
The loopback health listener is passed as an open descriptor, so there is no
release-to-bind port race. An occupied configured fixture port is refused without
touching its listener. Readiness allows ten seconds; three failed health probes
after readiness trigger restart. Restart delays are 0.5, 1, 2, then at most 4 seconds.
Closed or malformed child protocol output ends that runtime generation.

The product-owned plugin uses the native agent registry, session events, model
adapter, and cancellation services. It is separate from the vendor packages.
The private newline-JSON protocol carries start/cancel, session and streaming
events, model requests/results, and a settled notification. Frames are bounded to
2 MiB; overload ends the generation rather than silently truncating an event.
At most 64 sessions are live in the process. Late model results after cancellation
are ignored; settlement is emitted only after native session flush and disposal.
A restarted process does not reconstruct or replay prior runs.

All model calls, including helpers, use the IPC adapter. No external model
credentials or gateway bearer are inherited. `EXA_API_KEY`, when configured by the
host, is passed only for the native search provider. The harness's workspace-write
and ask policy remains intact: ordinary workspace writes do not ask; genuine
approval requests are not automatically allowed. The three description strings
proven in 129 are applied verbatim, with a check that schema semantics are unchanged.
The intended scope is “confined to your workspace and temporary files,” not
isolation from other friends' data. The retained-data bound is not a hard
quota on a live workspace or temporary files.

## Verification

`make check` includes the Go lifecycle fixtures and the plugin framing/tool-text
tests. The real pinned-runtime fixture is explicit, so ordinary checks do not
download a runtime:

```sh
INFERCAT_AGENT_TEST_INSTALL=/path/to/installed/host \
  go test -race -v ./internal/agent -run TestPinnedHarnessIPC -count=1
```

That fixture creates a separate temporary host, checks native readiness and model
IPC, cancels the native run, sends a late model reply, and completes a successor
on the same process with a fixture model response. It does not call an inference
engine. The E4B baseline proof through real routes belongs to 116c; E2B remains
promising pending the separate profile proof.

## Shared run mechanism (116b)

Host code registers a pull `Kind` or `Manager.Consumer` through the same registry.
The same durable attempt executor records each model call before dispatch and its
settlement/accounting before returning. A consumer owns its child work and must
join it before returning. Its registered `Policy.JoinCancel` keeps cancellation
requested but nonterminal through cleanup, including approval waiting. The shared
`Policy.DeferredCancel` supports work such as an already-generating image that
finishes successfully as Done. Ordinary pull kinds retain their default behavior.
After a deferred cancelled attempt settles, its run becomes Done if it produced
output, otherwise Cancelled; a kind cannot dispatch another step after that cancel.
Registration is rejected after startup/first submission, and the manager copies
the caller's initial kind map. Failed attempt starts end the run, so serial queues
cannot repeatedly select the same failed start.

Registration refuses JoinCancel without both Serial scheduling and a ForceStop
hook: joined consumers have at most one active run per kind on the host. Joined cancellation has
a 30-second deadline. The host registers
`Policy.ForceStop(runID)` to stop that run's owned runtime generation. At the
deadline, the manager waits up to ten more seconds for the hook, recovering a
panic. A successful stop permits the next queued run; timeout or panic quarantines
the kind from new starts. Accepted queued and waiting runs fail as `runtime quarantined`,
release their reservations and publish terminal events. New submissions receive
503 `upstream_down` with Retry-After: 10, rather than a malformed-request error.
While the stop is still in progress the message is `runtime stopping; retry
shortly`, distinct from quarantine. Entry
logs name timeout or panic; a successful stop (including a late return) logs recovery
and clears quarantine. Host restart also clears it. The bounded Cancelled outcome remains authoritative,
and late worker returns cannot replace it. Close returns within join + stop bounds.
At the stop deadline the manager abandons its waiter and closes the joined
notification. Go cannot kill a non-cooperative hook goroutine: that one goroutine
remains until the hook returns; a late successful return performs recovery
directly. There is no extra manager goroutine waiting indefinitely for it.
Go cannot kill an arbitrary goroutine. The supervised adapter identifies a runtime
generation: other sessions on that generation fail without replay, while a delayed
stop for an older generation cannot kill its replacement. This is not per-session
process isolation.

`Work.Approval` persists a pending question and waits without reconstructing the
consumer. `Manager.Answer` commits the identified answer once before waking it;
stale, duplicate, cross-key and cancelled answers refuse. The consumer forwards
that committed answer once; an ambiguous IPC send fails the run, never resends.
Restart recovery fails interrupted runs and keeps accumulated evidence. If a
requested Cancel/Answer instead commits an emergency terminal outcome, the typed
result carries that committed run and cancels its owned worker through the joined
stop path. The control response is 200 with the terminal snapshot: state and reason
are authoritative, not a promise that the requested answer was applied. A write
that did not commit remains a refusal (429 for storage limits), never a speculative
failed-run snapshot.

The Go run store owns retained state, exact raw native trajectory events, and
immutable captured output bytes in `runs/<key>/state.json`. The existing 64 MiB
per-key limit includes their encoded JSON/base64 bytes, the run ledger and its
event ring. `Admit` reserves encoded capacity before a model/tool step; concurrent
admissions and all ledger commits share that budget. Payload ingestion is capped
at 1 MiB per call and requires an active reservation, with control-record headroom.
Unused reservation capacity is released after the step; expiry removes the run,
its retained payload and any reservation together. Reads return detached data;
failed writes publish no event. A corrupt or ambiguous store fails closed.
Pure storage writes do not advance the lifecycle cursor or publish run-state events.
The ordinary budget reserves lifecycle headroom. At the ceiling, a bounded minimal
lifecycle/usage record (including Waiting) can replace newly returned output with
`output not retained: budget`; stored image metadata is preserved and the marker
goes in the existing bounded Reason. Usage identity fields are never rewritten.
A commit that shrinks the encoded snapshot is budget-exempt, so expiry can reclaim
space even while the remaining snapshot is still above the ordinary ceiling.
The 4 KiB exception is tallied per run in the snapshot across restart; its final
512 bytes are reserved for settlement. Exhaustion ends the existing run Failed as
`storage exhausted` and refuses the requested dispatch/approval. The emergency
record may trim oldest replay entries with normal cursor Reset, preserving
accumulated attempts, usage, captured outputs and image metadata. New-run admission
never consumes this lifecycle exception.
Budget refusals do not mark the key broken.
`SettlementError` distinguishes a successful model call whose accounting could not
be recorded from a failed call. Neither condition replays the model request.
One bounded cancellation note (4 KiB, no new captured outputs) may be retained from
the existing lease; filenames and MIME types are validated before capture.

Per-key recovery and Sweep failures are logged and isolated. Unfinished recovered
runs fail without replay, while the host serves healthy keys. Snapshots load lazily;
inactive terminal-only keys without subscribers leave memory after five minutes.
Whole-snapshot commits still cost more as retained data grows: the 116c probe on
this Mac measured 12–13 ms at 1.4 MiB encoded, 36–37 ms at 11.2 MiB and 175–183 ms
at 65.7 MiB. 116c must coalesce thinking updates to at most one durable update per
second and flush admission/approval/output/terminal boundaries; this slice does
not claim incremental storage or eliminate that measured cost.

The native JSONL backend and its backend-dependent checkpoint row are disabled
through supported composition. The harness retains execution and its in-memory
trajectory, but **native checkpointing is not active**. The pre-dispatch durability
duty moves to the Go owner's admission acknowledgement in 116c, and no real route
enables before that acknowledgement exists. 116b tests the owner and native
in-memory composition separately; it does not claim the IPC acknowledgement is
already connected to model/tool dispatch. Existing 116a JSONL artifacts are not
imported or deleted by this extraction.
An offline test in `make check` checks both disabling rows against the vendored
pinned base composition. Startup refuses a missing or changed installed manifest;
the live test requires the expected sessions directory to exist and remain empty.

Round-two storage behavior: single-key List refreshes access time; all-key
enumeration does not. Idle terminal keys with no subscriber leave memory after
five minutes even while other keys are active. Resident snapshots cost up to
64 MiB plus terminal headroom per key; all-key enumeration can temporarily load
all known snapshots before releasing idle ones. Live or directly polled keys stay
resident. All-key enumeration re-reads and re-parses each nonresident idle key
on every call, then releases it again. It is not amortized across calls. On this
Mac, 8 idle keys with 1 MiB each took 6.37–6.59 ms per repeated enumeration with
filesystem pages warm; the first already-resident call took 7.83 µs. Measurement:
/tmp/infercat-157-v3-outcomes.log. Scheduling and position reads currently pay this
tradeoff; the separately dispatched position-cache work is not included here. Every run write advances Updated. Retain is explicitly silent on the
lifecycle stream, while artifact creation, discard and each eviction publish their
own notifications. A valid cursor from an unknown epoch returns Reset.

Sweep reloads under the lock after any unlock. Expiry records exact artifact paths
from the loaded pre-deletion snapshot, commits the shrink, then unlinks those proven
paths through the retry tracker. Scanning an empty snapshot never authorizes
orphan deletion: unknown files stay intact. A nonempty orphan directory is logged
once per observed directory change, not once per sweep tick. Report-only orphan
entries and proven retry paths are both counted in pending-cleanup status; only
the proven paths may be automatically unlinked. Observation never authorizes
deletion, even if the key later has live rows. A vanished directory is benign.
Failed unlinks remain
visible and retryable; they do not break a key. The opt-in pinned tarball test
requires network access and INFERCAT_AGENT_TEST_INSTALL; ordinary make check skips
it. Its recorded live run compared the vendored manifest byte-for-byte with the
SHA-512-verified registry tarball (/tmp/infercat-157-tarball.log). The verified
manifest SHA-256 is
`885d9766775a2585f8c3a608cd2d2c97391e158b3ac1c53365cb2d1ca82040db`.

Generation-bound sends reject generation zero as unestablished; callers capture
the generation only after the child starts. Model identifiers are limited to
256 bytes before gateway dispatch, preserving charged identities without truncation.

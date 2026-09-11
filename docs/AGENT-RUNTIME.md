# Agent runtime

Infercat runs the pinned native harness as a supervised child. A host enables it
with `serve --agent`, then opts each friend in with `keys limits ID --agent=true`.
Model calls, including helper calls, use the run key through the gateway request
owner; their tokens count against the same limits as chat.

```sh
infercat agent install --data-dir /path/to/host
infercat serve --data-dir /path/to/host --agent
infercat keys limits alice --agent=true --data-dir /path/to/host
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
upstream safety notice unchanged. Infercat applies the read sandbox around the
whole child, including its in-process tools.

Files live below the host's `agent/` directory:

- `runtime-0.1.5-alpha.1-node22.23.2/`: pinned Node and integrity-locked packages,
  readable and executable by the child, never writable through the sandbox.
- `workspaces/run-*/`: a fresh workspace for each run, including private `tmp/`
  and `.runtime/` plugin/configuration/native state and bounded runtime log.
- `npm-cache/`: installer cache, outside the child's allowed trees.

After final output capture and child join, the workspace is removed. Captured
copies remain in the existing bounded run store; files not captured do not carry
into the next run. Earlier per-key workspaces and legacy `host/` files are not
imported or deleted automatically. An interrupted host or a cleanup failure can
leave a workspace for the operator to reclaim; it is never reused or granted to a
subsequent run. Cleanup failures are logged. The stored-data bound is not a live
workspace disk, CPU or memory quota.

`--agent` is off by default and is not remembered. `/status.agent` is absent when
off. When enabled, its existing state/PID/error fields describe the adapter:
`healthy` while idle means the installation and sandbox availability preflight
passed, with no child PID; while running it means the private plugin health check
passed. A fresh child starts for each Serial run. There is no automatic replay or
restart into an old workspace. Failed creation/readiness becomes a failed run and
status error; the next run may start a fresh child. A failed preflight at POST
returns `agent_unavailable`, while normal model serving remains available.

The existing guardian owns each child's process group. Host death closes the
lifetime pipe; the guardian sends TERM, waits up to two seconds, then kills the
group. The supervisor allows a further second to join. This does not contain
processes that escape that group or simultaneous guardian death. The child's
health listener is inherited as fd 3, rather than reserved and rebound. Protocol
and health failure end that run's child; they never run it without confinement.

The read sandbox is an allow-list on both shipped platforms:

- macOS uses deny-default Seatbelt through `sandbox-exec`: read/execute the approved
  system and tool trees (`/usr`, `/bin`, `/sbin`, `/System`, Homebrew, `/usr/local`,
  `/Library/Developer`), read the listed Frameworks/Apple/etc/timezone/device paths,
  and read/write the current workspace and its private temporary directory. Root
  directory access and global file metadata are allowed; metadata/names may be
  visible outside those trees even though file contents are denied. Paths are
  canonicalized so `/var` aliases do not invalidate the policy. PATH selects a
  working Python before the `/usr/bin` xcrun shim; `/Applications` and broad temp
  directories are not granted.
- Linux re-execs `_confine`, locks its OS thread, applies `no_new_privs` and a
  Landlock ruleset, then execs Node on that thread. ABI 3 or newer is required to
  restrict truncation; older/disabled kernels refuse agent availability. All known
  supported filesystem rights are handled. Reads/exec are limited to `/usr`,
  `/lib*`, `/bin`, `/sbin`, `/etc`, `/opt`, the pinned runtime, and the current
  workspace. Only that workspace/tmp is writable, plus `/dev/null` and optional
  `/dev/tty`; zero/random/urandom are readable. Device-node creation is denied.
  Only the initial child's `/proc/<pid>` is readable, not the host's or unrelated
  processes' proc directories. Ordinary Unix file permissions still apply.

Data/runtime layouts under a broadly readable tool tree are refused, as is a
runtime that contains the data directory: an allow-list cannot promise to hide a
subtree it also grants. Agent reads outside its workspace and the approved runtime
and system trees are denied; this includes the host data directory, other run
workspaces and personal dotfiles. The earlier single-friend recommendation is
retired for supported, successfully confined hosts. Windows agents remain
unsupported. Allowed system/tool trees may themselves contain readable
configuration or identifying information. This is kernel policy, not a separate
OS user or a guarantee against kernel/sandbox vulnerabilities.

Full network egress is allowed, including local services; there is no destination
filter or protection for data exposed by a reachable service. The native
workspace-write/ask policy still governs tool requests inside the outer sandbox.
An Allow answer cannot expand the outer read/write boundary. Ordinary workspace
writes do not ask; 129's three tool-description strings remain unchanged.

All model calls use the existing IPC adapter and the run's key at the Go gateway;
no gateway bearer or external model credentials are inherited. When configured,
the Exa key travels on an inherited pipe: the plugin reads and closes its descriptor
before readiness and supplies it directly to the search provider. It is absent
from the child's initial environment and is not written to plugin/config files.
The key still exists in the harness's process memory; this is not a secret boundary
against a compromised harness. A host-side search proxy is outside this slice.

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
engine. E4B is the baseline model; E2B remains promising pending the separate profile
proof. The live E4B route evidence is in `docs/spikes/116c-agent-route.md`.

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
panic. Only a nil hook result permits the next queued run; a returned error,
timeout or panic quarantines the kind from new starts. Accepted queued and waiting runs fail as `runtime quarantined`,
release their reservations and publish terminal events. New submissions receive
503 `upstream_down` with Retry-After: 10, rather than a malformed-request error.
While the stop is still in progress the message is `runtime stopping; retry
shortly`, distinct from quarantine. Entry
logs distinguish returned errors, timeouts and panics; a successful stop (including a late return) logs recovery
and clears quarantine. Host restart also clears it. Shutdown does not create a new quarantine or fail
other queued/waiting rows through that mechanism; existing interrupted-run recovery
still fails native work without replay. The bounded Cancelled outcome remains authoritative,
and late worker returns cannot replace it. Close returns within join + stop bounds.
At the stop deadline the manager abandons its waiter and closes the joined
notification. Go cannot kill a non-cooperative hook goroutine: that one goroutine
remains until the hook returns; a late successful return performs recovery
directly. There is no extra manager goroutine waiting indefinitely for it.
Go cannot kill an arbitrary goroutine. The adapter identifies the runtime instance
and generation owned by the run; a delayed stop cannot kill a later run's child.
Each native run has its own process, while host scheduling remains Serial.

`Work.Approval` persists a pending question and waits without reconstructing the
consumer. `Manager.Answer` commits the identified answer once before waking it;
stale, duplicate and cross-key answers refuse. A terminal snapshot takes precedence
over an answer; pending approvals on terminal or cancel-requested runs are omitted
from detail responses, with the original question preserved in retained history. The consumer forwards
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
One bounded cancellation note (4 KiB, no new captured outputs) may be retained even
in a lease-free window before terminal settlement. A private marker in the same
retained record enforces one successful note across lease changes and restart;
a refused commit does not spend it. This is not fresh model/tool admission or a
byte-budget exception: ordinary capacity and other reservations still apply.
Filenames and MIME types are validated before capture.

Per-key recovery and Sweep failures are logged and isolated. Unfinished recovered
runs fail without replay, while the host serves healthy keys. Snapshots load lazily;
inactive terminal-only keys without subscribers leave memory after five minutes.
Whole-snapshot commits still cost more as retained data grows: the 116c probe on
this Mac initially measured 12–13 ms at 1.4 MB encoded, 36–37 ms at 11.2 MB and 175–183 ms
at 65.7 MB (decimal bytes). Thinking updates coalesce to at most one durable
update per second per run; admission/approval/output/terminal boundaries force a
flush. The corrected-base rerun measured up to 186 ms near the cap (see the route
proof). This does not eliminate the measured whole-snapshot cost.

The native JSONL backend, its backend-dependent checkpoint row, and the persisted
session projection-cache row are disabled
through supported composition. The harness retains execution and its in-memory
trajectory, but **native checkpointing is not active**. The pre-dispatch durability
duty belongs to the Go owner's admission acknowledgement. Native model/root-tool
dispatch waits for the raw prefix to be committed; a tool also reserves capacity
through result capture. A failed acknowledgement refuses dispatch. Cancellation
retains only a bounded final turn note (also in a lease-free window), then joins native
disposal. Existing 116a JSONL artifacts are not imported or deleted.
An offline test in `make check` checks all three disabling rows against the vendored
pinned base composition. Startup refuses a missing or changed installed manifest;
the live test requires the expected sessions directory to exist and remain empty.

## Agent route contract

`/me.agent` is an additive boolean. The host must have the agent kind registered
and the key must have `agent: true`. A missing/unhealthy runtime refuses run
creation using the existing typed availability error; other host routes keep serving.

`POST /v1/runs` accepts `kind: "agent"`, optional `priority`, optional
`client_request_id`, and an `input` containing `model`, `messages`, optional
`temperature`, and optional `chat_template_kwargs.enable_thinking`. Message roles
are system/user/assistant; content is text or text/data-image parts. Other input
settings and unsupported parts refuse before creation. The encoded input limit is
1 MiB. Original history and supported settings are passed through to model calls.
`client_request_id` is 1–128 ASCII letters/digits/underscore/hyphen, scoped to the
key and echoed on Run/Summary. It is metadata: identical values create separate
runs. Never automatically resubmit an ambiguous creation or approval answer.

`GET /v1/runs` returns at most MaxRuns (100) summaries for the key, newest-created
first, with additive `created` and correlation metadata. List, detail, output and
approval routes take detached admission and spend RPM; they do not consume the
key's model-concurrency slot. Failed control mutations are never replayed.

`GET /v1/runs/{id}` returns ordered `steps`, output descriptors, optional pending
`approval`, and final `text` alongside the existing run. The existing `event: run`
SSE envelope gains `type: "step"` and nested `step`. `StepEvent` lives in
`internal/run/steps.go`: immutable `id` and original `at`, `type: "step"`, `kind`,
`status`, optional `name`, `tool`, one-line `result`, bounded `text`, and `output_id`.
An update replaces the whole step by ID; GET uses the same objects. Raw storage
writes remain silent. Cursor replay reconstructs updates; reset recovery uses
summaries plus GET. Queue position may be absent; elapsed means run age including
waits, never an estimate.

`GET /v1/runs/{id}/outputs/{output_id}` returns immutable captured bytes, scoped
to the requesting key. `POST /v1/runs/{id}/approval` accepts `{id, allow}` and
returns the authoritative snapshot after a commit-once answer. Cancellation stays
pending until the native consumer joins, subject to the shared 30-second bound.
Restart fails live runs; an old waiting approval cannot resume a lost generation.
`Step.Observe` exposes borrowed, callback-scoped model bytes to host consumers;
errors settle through the existing request owner, and no callback follows its
return. These live deltas are not authoritative state or meters.

Round-two storage behavior: single-key List refreshes access time; all-key
enumeration does not. Idle terminal keys with no subscriber leave memory after
five minutes even while other keys are active. Resident snapshots cost up to
64 MiB plus terminal headroom per key; all-key enumeration can temporarily load
all known snapshots before releasing idle ones. Live or directly polled keys stay
resident. All-key enumeration re-reads and re-parses each nonresident idle key
on every call, then releases it again. It is not amortized across calls. On this
Mac, 8 idle keys with 1 MiB each took 6.37–6.59 ms per repeated enumeration with
filesystem pages warm; the first already-resident call took 7.83 µs. Measurement:
/tmp/infercat-157-v3-outcomes.log. Scheduling enumeration pays this tradeoff; the landed platform position cache
serves repeated position reads. Every run write advances Updated. Retain is explicitly silent on the
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

A live row whose remaining per-run exception allowance cannot fit a minimal
terminal record after dropping replay entries fails its key closed at load.
The operator-reclamation mark is derived from snapshot bytes, including at the
ordinary ceiling; it is not gated on the snapshot being near the absolute cap.
Their bytes remain untouched; other keys continue serving. No extra allowance
is added beyond the absolute cap. Valid v3-produced live states retain terminal
headroom, and their expired join settles terminally.

Generation-bound sends reject generation zero as unestablished. A stop for zero,
a superseded generation or an already stopped child succeeds without touching
the current child. Only a real stop failure quarantines; shutdown logs failures
without changing other accepted rows. Callers capture
the generation only after the child starts. Model identifiers are limited to
256 bytes before gateway dispatch, preserving charged identities without truncation.

A single nonresident ceiling key (67,043,328 encoded bytes) required 46.8–51.7 ms
per all-key enumeration on this Mac with filesystem pages warm; each call parsed
the snapshot again. Measurement: /tmp/infercat-157-v4-focused.log.
The CLI separates proven retries from report-only observations as
`N awaiting cleanup, M for review`; the admin response carries
`image_cleanup_pending` (proven retries) and additive `image_orphans_for_review`
(report-only). The aggregate Store.ImageCleanupPending accessor remains their sum.

The allowance-exhausted 67,043,328-byte fixture enters the new load check and took
73.9 ms cold (filesystem pages warm); the 67,518,464-byte absolute-cap case took
83.2 ms. Both preserve bytes and fail only that key. Measurements, without timing
assertions: /tmp/infercat-157-v5-focused.log. Failed recovery is marked complete
only after success and is retried on the next load. Needs-attention responses use
503 upstream_down with Retry-After: 60 and direct the friend to the host.
The agent route uses the landed Answer and generation-stop contracts. Runtime
loss ends the active native session without replay; a later run gets a fresh child.

Detail and approval responses use a detached projection: run metadata, steps,
approval, output descriptors, and attempt identity/settlement/meters. Bulk attempt
outputs and prompt/completion text are excluded. Output reads copy only one
selected payload. There is one snapshot and one retained-data owner, with no new
file or secondary ledger. Agent attempt output is a bounded final
assistant message with tool calls (up to 1 MiB encoded), rather than the raw SSE
transcript. Native trajectory and thinking records remain separately accounted
under the same per-key cap; helpers also produce metered attempt records. A raw
SSE response remains transiently capped at 1 MiB during conversion. If compaction
cannot interpret it, the paid call stays successful and records a flagged raw
fallback containing only the last 64 KiB, with a diagnostic log. CRLF result lines
are normalised; unsupported step formatting produces a bounded failed note, not
a run abort. Adapter failures carry bounded causes such as `runtime_lost`,
`retention_refused`, `step_invalid`, `key_revoked`, `approval_invalid`, or
`workspace_unavailable`; friend cancellation is `cancelled`. Typed model-call
causes survive the attempt seam, and a settlement error exposes both the call and
store errors without retrying either request.

Helpers are classified by the pinned `GenerateOptions.purpose` (`compaction` or
`session-title`, dsh-llm/lib/types/types.d.ts). They use native settings/budgets,
with thinking off, rather than the friend's temperature/thinking settings. Their
purpose is recorded on each attempt. Tool-call id/type/name use the first nonempty
delta; only arguments concatenate.

The pinned approval counterpart is `dsh-user-approval/src/types.ts`:
`ApprovalRequestEvent` supplies agent, toolName, optional reason and optional
AbortSignal. `sandbox/src/escalation.ts` supplies the exact question and grants
only `allowed-once`; `rejected` throws a tool refusal. Invalid shapes fail visibly,
and the waiting step preserves the verbatim question. A permitted out-of-workspace
write can succeed without capture: its step records a capture-refusal note.
`agent_unavailable` (503) identifies an unready runtime with an operator-action
hint; it is not a run-store failure. A registered kind with a key opted out emits
`/me.agent: false` explicitly.

Current 116c v3 measurements on the fat run: at 68 outputs × 700 KiB and 1 MiB of
step text (66,436,469 encoded bytes), detail mean 2.074 µs, selected-output copy
13.393 µs, response encoding 460.570 µs outside the store lock. At 64,994,268 bytes,
small native-event commits still cost 150.1–161.9 ms plus 17.3–27.2 ms for admission.
These are warm non-race measurements, not timing assertions; see the retained
116c route report and its test fixtures.

116d boundary notes: the 256-entry replay ring is shared by all run kinds on a key,
including every published step replacement. Sustained agent activity can evict an
image client's old cursor. Reconnect returns the existing Reset with current
summaries; GET detail recovers the current steps and outputs. No extra ring or
replay guarantee is introduced.

The pinned `dsh-tools/lib/index.js:1218,3032,3213` creates `run_code` nested calls
with `parent: exec.token`, preserves that parent on the execution, and invokes the
same `tools/execute` hook. The adapter's parent branch is therefore reachable,
not dead code: admission, key recheck and reservation are at the root-tool boundary.
Nested calls execute inside that root operation without an independent Go ack or
key recheck; revocation is observed at the next root/model checkpoint, not between
nested calls. The parent's result remains subject to the existing capture and
retention bounds. This exception is documented, not a new nested-admission design.

Write steps can link a captured unified diff through their existing `output_id`.
Its descriptor adds `kind: "diff"` and uses existing `mime: "text/x-diff"`; no
`content_type` alias or new route is added. The normal file and tool-result
captures remain available. Prior bytes are read through the workspace root before
the root write checkpoint is acknowledged; a missing file is empty. Read failures,
binary (NUL or invalid UTF-8), unchanged or oversized versions produce no diff.
Each text version is bounded to 64 KiB. All pending prior-file snapshots together
are bounded to 64 KiB and 64 entries per run; when full, writes retain their normal
captures without a diff. A result releases its prior snapshot.

Diffs use one hunk spanning the changed region with up to three context lines at
each end, rather than a minimal-edit matrix. A diff is at most 64 KiB; a truncated
one ends with an explicit display-only marker and is not a complete patch. Missing
final newlines are marked. Diff bytes use ordinary retained accounting and expiry.
The harness's read-before-overwrite rule remains intact: the adapter's private
snapshot does not replace the model's required read.

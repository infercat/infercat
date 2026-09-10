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

`Work.Approval` persists a pending question and waits without reconstructing the
consumer. `Manager.Answer` commits the identified answer once before waking it;
stale, duplicate, cross-key and cancelled answers refuse. The consumer forwards
that committed answer once; an ambiguous IPC send fails the run, never resends.
Restart recovery fails interrupted runs and keeps accumulated evidence.

The Go run store owns retained state, exact raw native trajectory events, and
immutable captured output bytes in `runs/<key>/state.json`. The existing 64 MiB
per-key limit includes their encoded JSON/base64 bytes, the run ledger and its
event ring. `Admit` reserves encoded capacity before a model/tool step; concurrent
admissions and all ledger commits share that budget. Payload ingestion is capped
at 1 MiB per call and requires an active reservation, with control-record headroom.
Unused reservation capacity is released after the step; expiry removes the run,
its retained payload and any reservation together. Reads return detached data;
failed writes publish no event. A corrupt or ambiguous store fails closed.

The native JSONL backend and its backend-dependent checkpoint row are disabled
through supported composition. The harness retains execution and its in-memory
trajectory, but **native checkpointing is not active**. The pre-dispatch durability
duty moves to the Go owner's admission acknowledgement in 116c, and no real route
enables before that acknowledgement exists. 116b tests the owner and native
in-memory composition separately; it does not claim the IPC acknowledgement is
already connected to model/tool dispatch. Existing 116a JSONL artifacts are not
imported or deleted by this extraction.

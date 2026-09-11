# 116c agent route: v3 proof

The pinned native harness drives agent runs through the host's durable run owner.
Native dispatch waits for the raw-prefix retention acknowledgement; model attempts
use the existing gateway executor and the run's key. There is no self-HTTP or stored
bearer. One agent run is active per host. An interrupted native session fails without
replay; queued work waits for a healthy generation before its first dispatch.

## Real E4B route proof

Six probes passed on an isolated loopback host and its own llama.cpp engine on this
Mac, 2026-09-11 UTC. Model: Gemma-4-E4B-it-qat-UD-Q4_K_XL; pinned harness
0.1.5-alpha.1 / 5dda764e; Node 22.23.2. Both owned processes stopped afterward. Source checkpoint 205c2e5 is on landed
158 (10cbc96); the evidence records the proof binary SHA-256.
[Retained evidence](116c-agent-route-v3-evidence.json) includes boundaries, usage,
approval text, output checksums and native-store inventories.

| Probe | Elapsed | Steps | SSE replacements | Model attempts |
| --- | ---: | ---: | ---: | ---: |
| allow | 5.592 s | 4 | 9 | 3 |
| deny | 4.570 s | 4 | 9 | 3 |
| read-canary | 4.571 s | 3 | 7 | 3 |
| credential-sentinel | 4.045 s | 3 | 8 | 3 |
| crlf-result | 2.546 s | 3 | 6 | 3 |
| search-write | 8.136 s | 5 | 12 | 4 |

Allow and Deny each produced one real native question, preserved verbatim on the
waiting step: `escalate sandbox to danger-full-access: Write the isolated proof file.`
Allow continued and wrote the exact requested file; Deny refused and left no file.
The pinned counterpart is `dsh-user-approval/src/types.ts` and
`sandbox/src/escalation.ts`: `allowed-once` / `rejected`, with the request's AbortSignal.
Unknown ask shapes produce a visible refusal. No mutation is automatically replayed.

The synthetic canary proves the agent can read its host's data directory. It is not
an isolation claim. The sentinel returned `CREDENTIAL_ABSENT`; the actual search
credential was absent from the retained report. Search/write produced notes.md in
exactly two physical lines. The CRLF probe kept the original bytes in its captured
output while its step result was a valid single line. SSE full replacements matched
GET.steps in all six probes.

All 19 attempts were dispatched and settled once, with identities and token counts
matching all 19 gateway usage rows. There were 13 main attempts and six labeled
`session-title` helpers; helpers produced nonempty replies in 5–8 tokens. The
largest encoded compact attempt output was 441 bytes. Helpers use native settings
with thinking off, not the friend's settings; `compaction` uses the same pinned-purpose
mapping. Unit fixtures cover both purposes. Compaction failure records a flagged
last-64-KiB raw fallback and does not change a served model call into a failed call.

Native sessions and session_projcache inventories were empty after shutdown. JSONL
persistence, its checkpoint row and session-projection-cache remain disabled by
composition, without a vendor patch. The Go owner retains raw events, captured
outputs and run state under the existing per-key budget.

## Read and retained-write cost

The requested run itself contained 1, 10, 40 or 68 outputs of 700 KiB each, tested
with zero and 1 MiB of step text. Detail reads copy metadata/descriptors and exclude
bulk attempt bodies, prompt/completion text and native trajectories. Output reads
copy one selected entry. The same snapshot, reservation and expiry owner is retained.

| Outputs | Encoded snapshot, with 1 MiB step text | Detail mean | One output mean | Response encoding outside lock |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 2,398,615 B | 0.444 µs | 34.706 µs | 427.193 µs |
| 10 | 11,000,707 B | 0.787 µs | 32.997 µs | 449.110 µs |
| 40 | 39,674,377 B | 1.553 µs | 19.687 µs | 455.585 µs |
| 68 | 66,436,469 B | 2.074 µs | 13.393 µs | 460.570 µs |

These are warm, non-race measurements on this Mac, with no timing assertion.
`Test116CDetailFatRunCostAndDetachment` also proves returned buffers cannot mutate
the owner. List/detail/output/approval spend RPM through detached admission and do
not consume model concurrency. Empty attempts remain `[]`; image positions and
existing image admission are preserved.

Writes still serialize the per-key snapshot. Thinking updates coalesce to at most
one durable update per second, with forced checkpoint flushes. At 64,994,268 encoded bytes, admission took 17.3–27.2 ms and a small native-event
commit took 150.1–161.9 ms (three warm non-race samples). The
[cost fixture](https://github.com/infercat/infercat/blob/ad688b8beda55fd416a9c6982ea3da59b691473e/internal/run/testdata/116c_cost_test.go) ran through a temporary
Go overlay adding it as internal/run/cost_experiment_test.go; log:
`/tmp/infercat-116c-v3-write-cost.log`. These writes still pay full-snapshot cost;
the cheap detail measurements do not apply to commits.

## Boundaries and verification

Workspaces are `<dataDir>/agent/workspaces/<key>`, checked before run creation.
A home-less host works; an unavailable workspace returns `agent_unavailable`.
The shipped sandbox confines writes to workspace/temp unless genuinely approved,
but does not isolate reads from other host data. Use --agent on single-friend hosts
until a complete read boundary exists. Workspace/temp bytes are not a hard live quota.

The pinned tests verify actual Allow/Deny, checkpoint-before-model, cancellation,
serial replacement-generation recovery and the three disabled persistence rows.
Additional fixtures cover detached RPM, CRLF through native IPC, malformed step
notes, bounded model fallback, stable tool identities and the empty-attempt contract.
Final `GOFLAGS=-v make check`: CHECK OK; Go 981 passed / 0 failed / 8 opt-in
skips. Pinned native checks under race: 10 passed / 0 failed / 0 skipped. Logs:
`/tmp/infercat-116c-v3-final-check.log` and `/tmp/infercat-116c-v3-final-pinned.log`.
Exact accounting is in the handoff. Ticket 161 was not started.

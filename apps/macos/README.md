# Infercat for Mac

A native macOS 14+ menu-bar app and one window, Swift 6 and SwiftUI with an AppKit
`NSStatusItem`. Architecture and the CLI machine contract: `docs/design/desktop-app.md`
in the PM repo; the design it is built from: `docs/design/desktop-app-ui.md`.

**This is v0 of ticket 197, cuts A, B and C.** The menu-bar item with its six states
and the popover; first run; Overview in all seven of its states; Friends — the list,
the inspector, pause, resume, rotate, revoke, edit limits, and the New invite flow
with its once-card and QR; Activity; and the v0 Settings. English and Simplified
Chinese, light and dark, throughout. Engine and Usage are v1: their sidebar entries
say "comes in the next build" and offer the web console, which already does the job.

## Build and test, unsigned

Xcode with Swift 6, and the Go toolchain from the repository's `go.mod`.

```sh
make -C apps/macos build     # or: make -C apps/macos test
```

or directly:

```sh
xcodebuild -project apps/macos/InfercatMac.xcodeproj -scheme Infercat \
  -configuration Debug -destination 'platform=macOS' \
  -derivedDataPath /tmp/infercat-mac CODE_SIGNING_ALLOWED=NO test
```

A build phase compiles `./cmd/infercat` from this repository into
`Infercat.app/Contents/Helpers/infercat`. Set `INFERCAT_CLI_BINARY` to an absolute
path to bundle a specific build instead. No signing, notarization, Sparkle or `.dmg`:
those wait on ticket 034.

## Screenshots

```sh
make -C apps/macos screens OUT=/tmp/infercat-mac-screens
```

Renders every state this build has — 19 scenes × 2 appearances × 2 languages — from
the bundled fixtures, offscreen, without touching a host or a data directory, and
exits. It is also how the founder's copy was made.

`--fixtures` runs the app against the same fixtures interactively. It is explicit:
the default always uses the real CLI, and an unreadable answer is an error the person
sees, never a quiet fall back to demo data.

## The boundary

The app's only interface to the host is the bundled `infercat` in machine mode,
addressed by absolute path, invoked with an argv array. No PATH, no shell, no HTTP,
no socket, no reading the host's files, and the admin token never enters the app.

- **`CLIClient`** is the seam. `ProcessCLI` is the only production implementation;
  `FixtureCLI` backs the tests, the previews and the capture harness.
- **`--json` goes immediately after the verb path** and free text after `--`, because
  a value-taking flag would otherwise swallow the next argument (195-b's review).
- **Exit codes map before the envelope is insisted on**: 0 ok, 1 refused (the host's
  `code` and `message` are kept), 2 a command we built wrong, 69 no host, 75 no answer
  in time. A host that dies without printing still reports 69 or 75 correctly.
- **One `watch --json` subprocess** for live data, at 2 s while a window or the popover
  is visible and 10 s otherwise, restarted on a visibility change with a one-second
  debounce. Stopping it closes stdout first, then sends SIGTERM, then SIGKILL after a
  second: `watch` cannot see a signal while it is blocked writing to a pipe nobody reads.
- **One ladder, and one rule.** A `watch` attempt that ends *without having delivered
  a `status` frame* is answered the same way every time: wait out the current rung —
  2 s, doubling to a 30 s ceiling — and try again. A `status` frame is the only thing
  that resets it. `hello`, `gone`, an exit code and `service status` are not evidence.
  **`service.running` least of all**: it means launchd holds a pid for the LaunchAgent,
  not that a host answers for this data directory, so a host that is booting,
  crash-looping under `KeepAlive`, or serving a different `--data-dir` will keep
  exiting 69 while launchd reports it running. The service is read here only to word
  what the person sees, and only while the ladder is still 8 s or shorter, because
  after that only `watch` can tell us anything new.

  Every edge from "a subprocess ended" to "spawn another subprocess", with its
  minimum delay:

  | Edge | Minimum delay before the next spawn |
  |---|---|
  | app launch, Start, Restart, ⌘R → first attempt | none — these are the person asking, once each, and `working` admits one at a time |
  | attempt ended without a `status` frame → next attempt | the current rung: 2, 4, 8, 16, 30, 30… |
  | attempt ended having delivered a `status` frame → next attempt | 1 s, and the ladder is back at its foot |
  | the wording read (`service status`) → the attempt in the same cycle | none, but it happens at most three times per waiting period and never while the ladder is above 8 s |
  | `checkNow()` (Start pressed, a surface appeared) → next attempt | cuts the current nap short, honoured at most once per 30 s of waiting, and never lowers the ladder |
  | visibility change → restart for a new interval | 1 s debounce, and only while a stream is actually delivering |
  | damaged bundle, or a schema we cannot read | the loop stops; it will not fix itself |
  | **Activity** — any number of rows, filters, pause, resume | **none: it adds no edge at all.** Every row is an `event` line of the stream that is already running, so the screen is a view of it and never a source |
  | Activity's list publishes | at most five a second. A busy host settles twenty requests a second, and republishing five hundred rows for each cost about a quarter of a core; the first publish after a quiet moment is immediate, the rest wait out a 200 ms cooldown, and pause and resume always publish at once |
  | Settings: Save name, Start at login, Open web console | one subprocess per press, and `working` means a second press while the first is out does nothing |
  | selecting a friend → `keys show` | 150 ms debounce, latest-wins, one read in flight. Holding an arrow key across fifty friends costs at most three reads, not fifty |

  Ten minutes with no host installed costs **26 subprocesses**; ten minutes with a pid
  but nothing answering costs the same 26; someone holding Start down through those
  ten minutes costs at most 80. `CadenceTests` pins all of them with a fake clock.

- **The other things that can spawn are rate-limited too.** `service status` outside
  the loop is one read per user action. Limits and today's usage are read once on the
  first status and then at most once a minute while a surface is visible — never at
  the status cadence — and a read that fails still counts, so a failure cannot spin.
  `keys show` is one read per friend the person stops on: a new selection cancels the
  pending one, so arrow-key navigation costs a read for where they landed, not for
  every row they passed. Every mutation is one subprocess, guarded so a second cannot
  start while the first is in flight, and saving limits sends only the fields that
  moved — one command, plus a second only when the agent switch itself was moved.

- **A command subprocess only when someone acts.** Its stdout is capped at 1 MiB,
  its stderr is drained and never logged, and it is never replayed. An action that
  goes unanswered says so; the app re-reads instead of resending.
- **`gone.reason` and `error.code` are open sets**, and so is a watch line's `type`.
  The app reports them and never switches on them.
- **launchd having a pid is not the same as a host answering, and the app says which
  it means.** For 30 s after the app asked for a start — including its own launch —
  a host that has not answered yet reads "Starting…". After that it reads "The host is
  not answering", with Restart host and Open web console, because at that point a
  fault is the honest reading.
- **No optimistic updates.** A pressed control shows "Waiting for the host…", and the
  screen changes when the next status confirms it.
- **Lifecycle only through `infercat service`.** The host outlives the app: Quit says
  so and leaves it running.
- **Activity can never hold what was said.** `usage.Event` on the host carries
  `prompt` and `completion` when it runs with `--log-prompts`. The stream is
  documented to strip them, and the app's decoder does not declare them either, so
  there is no property for request text to land in. A test feeds an event line
  carrying both and asserts no trace survives into the row or the list. App polls are
  not listed — an idle browser tab polls every 30 s per friend, and the design's own
  count is the host's `model_calls` — and a `dropped` line becomes a visible gap row,
  never a silent one. Pausing counts arrivals as they arrive rather than inferring
  them from the buffer's length, which stops meaning anything once the ring is full;
  when more arrive than the ring holds, the button says so. The rows survive the host
  going away, because that is exactly when someone wants to look at them.
- **The invite secret lives in memory and nowhere else.** `InviteSecret` is not
  `Codable`, is never written, never logged, never interpolated into an error, and
  reaches the pasteboard only through a button the person pressed — marked transient
  so clipboard managers keep no history. The sheet and the window that presents it are
  marked non-restorable while it is on screen, so no saved-state snapshot can contain
  it. After Done, a masked prefix and the date are all that remain.
  `SecretHygieneTests` fails if a minted secret reaches UserDefaults, the app's
  directories, the saved-state directory, the pasteboard before a copy, or any error
  string.
- **"No limit" is `-1`, not `0`.** The host coerces `0` to its own default on almost
  every limit field. The only exception is `--max-context`, where `0` is the
  documented sentinel for the engine's window. The fixtures README has the table and
  the evidence.

## Layout

| Path | What |
|---|---|
| `Sources/Contract.swift` | schema-1 payload types, exit codes, argv templates, the `CLIClient` protocol |
| `Sources/ProcessCLI.swift` | subprocesses, the stop sequence, envelope validation |
| `Sources/WatchStream.swift` | the one long-lived stream, cadence and backoff |
| `Sources/HostModel.swift` | the six menu-bar states, the seven Overview states, attention, actions |
| `Sources/PopoverView.swift`, `FirstRunView.swift`, `MainWindow.swift`, `OverviewScreen.swift` | the surfaces |
| `Sources/FriendsScreen.swift`, `FriendInspector.swift`, `FriendsModel.swift` | Friends: the list, one friend in full, and the sorting and status lines behind them |
| `Sources/ActivityScreen.swift`, `Activity.swift` | Activity: the live tail of settled requests, and the types that keep request text out of it |
| `Sources/SettingsScreen.swift` | Settings: name, start at login, language, the console |
| `Sources/InviteSheet.swift`, `InviteSecret.swift`, `Limits.swift` | the invite flow, the secret that exists only while the once-card is open, and the limit form |
| `Sources/Brand.swift`, `Copy.swift` | cobalt, the loaf, status squares, IBM Plex Mono, EN/ZH copy |
| `Sources/Fixtures.swift`, `Capture.swift` | the fixture client and the screenshot harness |
| `Resources/Fixtures/` | the vendored contract fixtures and the demo payloads ([README](Resources/Fixtures/README.md)) |

IBM Plex Mono is bundled under the SIL Open Font License 1.1
(`Resources/LICENSE-IBMPlexMono.txt`); the notice is in the About panel.

## Two deliberate deviations from the design

1. **The sidebar uses the system list's `.inset` style, not `.sidebar`.** The sidebar
   style draws its selection as a vibrancy layer, which the offscreen capture harness
   renders as a solid black bar — so every screenshot of every screen would have hidden
   the selected row. Same system list, drawn opaquely.
2. **The Overview screen carries a 60 pt top inset.** macOS 26 draws a glass band under
   the toolbar; without the inset the one sentence of state is read through it and looks
   disabled. The inset is what keeps the headline at full contrast.

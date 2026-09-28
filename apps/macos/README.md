# Infercat for Mac

A native macOS 14+ menu-bar app and one window, Swift 6 and SwiftUI with an AppKit
`NSStatusItem`. Architecture and the CLI machine contract: `docs/design/desktop-app.md`
in the PM repo; the design it is built from: `docs/design/desktop-app-ui.md`.

**This is cut A of ticket 197.** It has the menu-bar item with its six states and the
popover, first run, and the main window with the Overview screen in all seven of its
states, in English and Simplified Chinese, light and dark. Friends, Activity, Engine,
Usage and Settings are in the sidebar but say "comes in the next build" and offer the
web console, which already does the job. Nothing in this build fakes an invite.

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
- **A stream that ends is two different things, handled differently.** With no host,
  `watch` prints one `gone` line and exits 69 after a fraction of a second, so
  respawning it on a timer would be the polling-by-spawning this architecture exists
  to avoid. That case does not respawn `watch` at all: the stream enters *waiting for
  the host* and asks `service status --json` on its own ladder — 2 s doubling to a
  30 s ceiling — starting `watch` again only once the service reports running.
  Pressing Start, or bringing the window or popover on screen, cuts the current wait
  short. Ten minutes with no host installed costs **24 subprocesses**, a number
  `CadenceTests` pins with a fake clock. Any other ending (a crash, exit 75, output we
  cannot read) does restart `watch`, on the same ladder from 1 s, and the delay resets
  only after a `status` frame has actually arrived — a `hello` or a `gone` is not
  evidence that anything works. A `gone` followed by the stream ending is one service
  read, not one per callback.
- **A command subprocess only when someone acts.** Its stdout is capped at 1 MiB,
  its stderr is drained and never logged, and it is never replayed. An action that
  goes unanswered says so; the app re-reads instead of resending.
- **`gone.reason` and `error.code` are open sets**, and so is a watch line's `type`.
  The app reports them and never switches on them.
- **No optimistic updates.** A pressed control shows "Waiting for the host…", and the
  screen changes when the next status confirms it.
- **Lifecycle only through `infercat service`.** The host outlives the app: Quit says
  so and leaves it running.

## Layout

| Path | What |
|---|---|
| `Sources/Contract.swift` | schema-1 payload types, exit codes, argv templates, the `CLIClient` protocol |
| `Sources/ProcessCLI.swift` | subprocesses, the stop sequence, envelope validation |
| `Sources/WatchStream.swift` | the one long-lived stream, cadence and backoff |
| `Sources/HostModel.swift` | the six menu-bar states, the seven Overview states, attention, actions |
| `Sources/PopoverView.swift`, `FirstRunView.swift`, `MainWindow.swift`, `OverviewScreen.swift` | the surfaces |
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

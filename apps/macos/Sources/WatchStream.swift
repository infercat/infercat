import Foundation

/// Sleeping, behind a seam, so the cadence can be tested without waiting for it.
protocol Napper: Sendable {
    /// Returns when the nap is over, or at once if the task was cancelled.
    func nap(seconds: Int) async
    /// The short waits — a debounce rather than a ladder rung.
    func nap(milliseconds: Int) async
}

extension Napper {
    func nap(milliseconds: Int) async { try? await Task.sleep(for: .milliseconds(milliseconds)) }
}

struct RealNapper: Napper {
    func nap(seconds: Int) async { try? await Task.sleep(for: .seconds(seconds)) }
}

/// Owns exactly one `infercat watch --json` subprocess for the life of the app.
///
/// One stream, not a poll loop: spawning a process twice a second costs an exec event
/// and about 20 ms each time (docs/design/desktop-app.md, decision 3). The interval is
/// 2 s while a window or the popover is visible and 10 s otherwise.
///
/// # One ladder, and one rule
///
/// A `watch` attempt that ends **without having delivered a `status` frame** is the
/// only thing that matters, and it is always answered the same way: wait out the
/// current rung of the ladder — 2 s, doubling to a 30 s ceiling — and try again.
/// Nothing else shortens it and nothing else resets it.
///
/// In particular **`service status` is not evidence.** Its `running` only means
/// launchd holds a pid for the LaunchAgent; a host can be booting, crash-looping
/// under `KeepAlive`, or serving a different `--data-dir`, and `watch` will still
/// exit 69. Treating `running` as permission to respawn produced about 140
/// subprocesses a second. So the service is read here for one reason only — to word
/// what the person sees — and only while the ladder is still short, because after
/// that only `watch` can tell us anything new.
///
/// Every edge from "a subprocess ended" to "spawn another subprocess" therefore
/// passes through `pause(_:)`, and the README's lifecycle paragraph lists them all
/// with their minimum delay.
@MainActor
final class WatchStream {
    enum Phase: Equatable, Sendable { case idle, streaming, waitingForHost }

    private let client: any CLIClient
    private let napper: any Napper
    private var task: Task<Void, Never>?
    private var debounce: Task<Void, Never>?
    private var wake: CheckedContinuation<Void, Never>?
    private(set) var intervalSeconds = 10
    private(set) var phase: Phase = .idle
    private var generation = 0
    /// Ladder seconds waited since the last early wake, so a person hammering Start
    /// cannot turn the ladder into a spin.
    private var nappedSinceWake = Int.max

    /// The ladder: where it starts when nothing has answered, where it restarts after
    /// a stream that did answer, and its ceiling.
    static let firstStep = 2
    static let afterGoodStream = 1
    static let ceiling = 30
    /// While the ladder is this short, one `service status` read per attempt words the
    /// screen. Above it, the wording is left alone and only `watch` is tried.
    static let wordingWindow = 8
    /// An early wake is honoured at most once per this much waiting.
    static let earlyWakeFloor = 30

    /// Every frame the stream produced, in order.
    var onFrame: ((WatchFrame) -> Void)?
    /// The stream ended. `error` is nil for a clean end.
    var onEnd: ((Error?) -> Void)?
    /// A `service status` read taken to word the screen. Never used to decide whether
    /// to spawn anything.
    var onService: ((ServiceStatus) -> Void)?
    /// A delay about to be waited out, in seconds.
    var onBackoff: ((Int) -> Void)?
    /// The phase changed.
    var onPhase: ((Phase) -> Void)?

    init(client: any CLIClient, napper: any Napper = RealNapper()) {
        self.client = client
        self.napper = napper
    }

    // MARK: - Running

    /// Starts, or restarts, from the bottom of the ladder. This is a user-driven edge
    /// — launch, Start, Restart, ⌘R — never something the loop does to itself.
    func start() {
        generation += 1
        let mine = generation
        task?.cancel()
        resumeWake()
        nappedSinceWake = Int.max
        task = Task { [weak self] in await self?.run(mine) }
    }

    func stop() {
        generation += 1
        debounce?.cancel()
        task?.cancel()
        task = nil
        resumeWake()
        set(.idle)
    }

    private func run(_ mine: Int) async {
        var ladder = Self.firstStep
        var everAnswered = false
        var firstAttempt = true

        while !Task.isCancelled, mine == generation {
            // Edge 1 — between any two attempts, the current rung. The only edge that
            // does not wait is the very first attempt after `start()`.
            if !firstAttempt {
                onBackoff?(ladder)
                await pause(ladder)
                guard !Task.isCancelled, mine == generation else { return }
                ladder = min(Self.ceiling, ladder * 2)
            }
            firstAttempt = false

            // Wording only, and only while the situation is fresh. Never a gate.
            if !everAnswered, ladder <= Self.wordingWindow,
               let service = try? await client.read(.serviceStatus, as: ServiceStatus.self) {
                guard !Task.isCancelled, mine == generation else { return }
                onService?(service)
            }

            var sawStatus = false
            do {
                for try await frame in client.watch(intervalSeconds: intervalSeconds) {
                    guard !Task.isCancelled, mine == generation else { return }
                    onFrame?(frame)
                    if case .status = frame.body {
                        sawStatus = true
                        everAnswered = true
                        set(.streaming)
                    }
                }
                guard !Task.isCancelled, mine == generation else { return }
                onEnd?(nil)
            } catch let error as CLIError {
                guard !Task.isCancelled, mine == generation else { return }
                onEnd?(error)
                // A damaged bundle or a schema we cannot read will not fix itself, and
                // retrying it forever is the same mistake in a different shape.
                if error == .missingBinary || Self.isSchemaFailure(error) {
                    set(.idle)
                    return
                }
            } catch {
                guard !Task.isCancelled, mine == generation else { return }
                onEnd?(error)
            }

            // A `status` frame is the one piece of evidence that resets the ladder.
            // `gone`, `hello`, an exit code and `service.running` are all not.
            if sawStatus {
                ladder = Self.afterGoodStream
            } else {
                set(.waitingForHost)
            }
        }
    }

    // MARK: - Cadence

    /// Switches the cadence. The restart is debounced: a click that opens the popover
    /// and immediately closes it must not cost two subprocesses.
    func setVisible(_ visible: Bool) {
        if visible { checkNow() }
        let next = visible ? 2 : 10
        debounce?.cancel()
        guard next != intervalSeconds else { return }
        debounce = Task { [weak self] in
            guard let self else { return }
            await self.napper.nap(seconds: 1)
            guard !Task.isCancelled, next != self.intervalSeconds else { return }
            self.intervalSeconds = next
            // Only a stream that is actually delivering needs restarting for a new
            // interval. While we are waiting, the ladder owns the timing and a
            // visibility change must not jump it.
            if self.phase == .streaming { self.start() }
        }
    }

    /// Cuts the current wait short — the person pressed Start, or brought a surface on
    /// screen. It never lowers the ladder, and it is honoured at most once per
    /// `earlyWakeFloor` seconds of waiting, so holding the button down cannot spin.
    func checkNow() {
        guard nappedSinceWake >= Self.earlyWakeFloor else { return }
        nappedSinceWake = 0
        resumeWake()
    }

    /// Sleeps, unless `checkNow()` cuts it short first.
    private func pause(_ seconds: Int) async {
        let sleeper = Task { [napper] in await napper.nap(seconds: seconds) }
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            wake = continuation
            Task { [weak self] in
                await sleeper.value
                guard let self else { return }
                // A nap that ran its course counts towards the next early wake.
                if self.wake != nil { self.nappedSinceWake = self.saturatingAdd(self.nappedSinceWake, seconds) }
                self.resumeWake()
            }
        }
        sleeper.cancel()
    }

    private func saturatingAdd(_ value: Int, _ delta: Int) -> Int {
        value > Int.max - delta ? Int.max : value + delta
    }

    private func resumeWake() {
        guard let continuation = wake else { return }
        wake = nil
        continuation.resume()
    }

    private func set(_ next: Phase) {
        guard phase != next else { return }
        phase = next
        onPhase?(next)
    }

    private static func isSchemaFailure(_ error: CLIError) -> Bool {
        if case .unsupportedSchema = error { return true }
        return false
    }
}

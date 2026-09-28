import Foundation

/// Sleeping, behind a seam, so the cadence can be tested without waiting for it.
protocol Napper: Sendable {
    /// Returns when the nap is over, or at once if the task was cancelled.
    func nap(seconds: Int) async
}

struct RealNapper: Napper {
    func nap(seconds: Int) async { try? await Task.sleep(for: .seconds(seconds)) }
}

/// Owns exactly one `infercat watch --json` subprocess for the life of the app, and
/// decides what to do when it ends.
///
/// One stream, not a poll loop: spawning a process twice a second costs an exec event
/// and about 20 ms each time (docs/design/desktop-app.md, decision 3). The interval is
/// 2 s while a window or the popover is visible and 10 s otherwise.
///
/// **Ending is two different things.** When there is no host, `watch` prints one
/// `gone` line and exits 69 after a fraction of a second. Respawning it on a timer
/// would be exactly the polling-by-spawning this design exists to avoid, so that case
/// does not respawn `watch` at all: the stream enters `waitingForHost` and asks
/// `service status --json` on its own slow ladder — 2 s doubling to a 30 s ceiling —
/// starting `watch` again only once the service reports running. Any other ending
/// (a crash, exit 75, output we cannot read) does restart `watch`, with the same
/// ladder, and the delay resets only after a `status` frame has actually arrived:
/// a `hello` or a `gone` is not evidence that anything is working.
@MainActor
final class WatchStream {
    enum Phase: Equatable, Sendable { case idle, streaming, retrying, waitingForHost }

    private let client: any CLIClient
    private let napper: any Napper
    private var task: Task<Void, Never>?
    private var debounce: Task<Void, Never>?
    private var wake: CheckedContinuation<Void, Never>?
    private(set) var intervalSeconds = 10
    private(set) var phase: Phase = .idle
    private var generation = 0

    /// The ceiling both ladders climb to, and where each one starts.
    static let ceiling = 30
    static let firstRetry = 1
    static let firstHostCheck = 2

    /// Every frame the stream produced, in order.
    var onFrame: ((WatchFrame) -> Void)?
    /// The stream ended. `error` is nil for a clean end.
    var onEnd: ((Error?) -> Void)?
    /// A `service status` read taken while waiting for the host. This is the only
    /// place the service is polled, so a `gone` frame followed by the stream ending
    /// is one check and not two.
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

    func start() {
        generation += 1
        let mine = generation
        task?.cancel()
        resumeWake()
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
        var retry = Self.firstRetry
        while !Task.isCancelled, mine == generation {
            var noHost = false
            set(.streaming)
            do {
                for try await frame in client.watch(intervalSeconds: intervalSeconds) {
                    guard !Task.isCancelled, mine == generation else { return }
                    onFrame?(frame)
                    switch frame.body {
                    case .status:
                        // The only evidence that the host is actually answering.
                        retry = Self.firstRetry
                    case .gone:
                        noHost = true
                    default:
                        break
                    }
                }
                guard !Task.isCancelled, mine == generation else { return }
                onEnd?(nil)
            } catch let error as CLIError {
                guard !Task.isCancelled, mine == generation else { return }
                onEnd?(error)
                switch error {
                case .missingBinary, .unsupportedSchema:
                    // A damaged bundle or a schema we cannot read will not fix itself.
                    set(.idle)
                    return
                case .hostStopped:
                    noHost = true
                default:
                    break
                }
            } catch {
                guard !Task.isCancelled, mine == generation else { return }
                onEnd?(error)
            }
            guard !Task.isCancelled, mine == generation else { return }

            if noHost {
                await waitForHost(mine)
                retry = Self.firstRetry
            } else {
                set(.retrying)
                onBackoff?(retry)
                await pause(retry)
                retry = min(Self.ceiling, retry * 2)
            }
        }
    }

    /// Asks `service status` on a slow ladder until the host is running. No `watch`
    /// subprocess exists while this runs, and each turn of the loop is one short read.
    private func waitForHost(_ mine: Int) async {
        set(.waitingForHost)
        var delay = Self.firstHostCheck
        while !Task.isCancelled, mine == generation {
            if let service = try? await client.read(.serviceStatus, as: ServiceStatus.self) {
                guard !Task.isCancelled, mine == generation else { return }
                onService?(service)
                if service.running { return }
            }
            guard !Task.isCancelled, mine == generation else { return }
            onBackoff?(delay)
            await pause(delay)
            delay = min(Self.ceiling, delay * 2)
        }
    }

    // MARK: - Cadence

    /// Switches the cadence. The restart is debounced: a click that opens the popover
    /// and immediately closes it must not cost two subprocesses.
    func setVisible(_ visible: Bool) {
        // Somebody is looking, so do not make them wait out a 30 s ladder.
        if visible { checkNow() }
        let next = visible ? 2 : 10
        debounce?.cancel()
        guard next != intervalSeconds else { return }
        debounce = Task { [weak self] in
            guard let self else { return }
            await self.napper.nap(seconds: 1)
            guard !Task.isCancelled, next != self.intervalSeconds else { return }
            self.intervalSeconds = next
            // Only a running stream needs restarting for a new interval; while we are
            // waiting for a host there is no subprocess to restart.
            if self.phase == .streaming { self.start() }
        }
    }

    /// Cuts the current wait short — the person pressed Start, or brought a surface
    /// on screen, and should not have to wait out the ladder.
    func checkNow() { resumeWake() }

    /// Sleeps, unless `checkNow()` cuts it short first.
    private func pause(_ seconds: Int) async {
        let sleeper = Task { [napper] in await napper.nap(seconds: seconds) }
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            wake = continuation
            Task { [weak self] in
                await sleeper.value
                self?.resumeWake()
            }
        }
        sleeper.cancel()
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
}

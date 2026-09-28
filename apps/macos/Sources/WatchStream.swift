import Foundation

/// Owns exactly one `infercat watch --json` subprocess for the life of the app.
///
/// One stream, not a poll loop: spawning a process twice a second costs an exec event
/// and about 20 ms each time (docs/design/desktop-app.md, decision 3). The interval is
/// 2 s while a window or the popover is visible and 10 s otherwise; a visibility change
/// restarts the stream, debounced by a second so that clicking through the menu bar does
/// not churn processes. When the stream ends the loop backs off and tries again.
@MainActor
final class WatchStream {
    private let client: any CLIClient
    private var task: Task<Void, Never>?
    private var debounce: Task<Void, Never>?
    private(set) var intervalSeconds = 10
    private var generation = 0

    /// Every frame the stream produced, in order.
    var onFrame: ((WatchFrame) -> Void)?
    /// The stream ended. `error` is nil for a clean end (the host stopped, we were asked to).
    var onEnd: ((Error?) -> Void)?
    /// A retry is about to happen in `seconds`.
    var onBackoff: ((Int) -> Void)?

    init(client: any CLIClient) { self.client = client }

    func start() {
        generation += 1
        let mine = generation
        task?.cancel()
        task = Task { [weak self] in
            var delay = 1
            while !Task.isCancelled {
                guard let self, mine == self.generation else { return }
                let interval = self.intervalSeconds
                do {
                    for try await frame in self.client.watch(intervalSeconds: interval) {
                        guard !Task.isCancelled, mine == self.generation else { return }
                        self.onFrame?(frame)
                        delay = 1
                    }
                    guard !Task.isCancelled, mine == self.generation else { return }
                    self.onEnd?(nil)
                } catch {
                    guard !Task.isCancelled, mine == self.generation else { return }
                    self.onEnd?(error)
                    // A damaged bundle or a schema we cannot read will not fix itself.
                    if let failure = error as? CLIError,
                       failure == .missingBinary || Self.isSchemaFailure(failure) { return }
                }
                guard !Task.isCancelled, mine == self.generation else { return }
                self.onBackoff?(delay)
                do { try await Task.sleep(for: .seconds(delay)) } catch { return }
                delay = min(30, delay * 2)
            }
        }
    }

    /// Switches the cadence. The restart is debounced: a click that opens the popover
    /// and immediately closes it must not cost two subprocesses.
    func setVisible(_ visible: Bool) {
        let next = visible ? 2 : 10
        debounce?.cancel()
        guard next != intervalSeconds else { return }
        debounce = Task { [weak self] in
            do { try await Task.sleep(for: .seconds(1)) } catch { return }
            guard let self, next != self.intervalSeconds else { return }
            self.intervalSeconds = next
            self.start()
        }
    }

    func stop() {
        generation += 1
        debounce?.cancel()
        task?.cancel()
        task = nil
    }

    private static func isSchemaFailure(_ error: CLIError) -> Bool {
        if case .unsupportedSchema = error { return true }
        return false
    }
}

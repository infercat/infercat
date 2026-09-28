import Foundation

/// A `CLIClient` backed by the bundled fixtures. Two uses only: the `--fixtures`
/// preview mode and the capture harness. Production never falls back to it — an
/// unreadable answer from the real CLI is an error the person gets to see.
final class FixtureCLI: CLIClient, @unchecked Sendable {
    let language: Language
    private let lock = NSLock()
    private var running: Bool
    private var installed: Bool

    init(language: Language = .en, running: Bool = true, installed: Bool = true) {
        self.language = language
        self.running = running
        self.installed = installed
    }

    func run(_ command: CLICommand) async throws -> Data {
        switch command.operation {
        case "status":
            guard lock.withLock({ running }) else { throw CLIError.hostStopped }
            return try Fixtures.demo("status.\(language.code)")
        case "keys.list":
            guard lock.withLock({ running }) else { throw CLIError.hostStopped }
            return try Fixtures.demo("keys.\(language.code)")
        case "keys.get":
            guard lock.withLock({ running }) else { throw CLIError.hostStopped }
            return try Fixtures.demo("keyshow.\(language.code)")
        case "keys.add", "keys.rotate":
            guard lock.withLock({ running }) else { throw CLIError.hostStopped }
            return try Fixtures.demo("minted.\(language.code)")
        case "keys.limits", "keys.pause", "keys.resume", "keys.revoke":
            guard lock.withLock({ running }) else { throw CLIError.hostStopped }
            return try Fixtures.contract("keys.pause.populated")
        case "usage":
            guard lock.withLock({ running }) else { throw CLIError.hostStopped }
            return try Fixtures.demo("usage")
        case "service.status":
            return try serviceEnvelope()
        case "service.install":
            lock.withLock { installed = true }
            return try serviceEnvelope()
        case "service.start", "service.restart":
            lock.withLock { installed = true; running = true }
            return try serviceEnvelope()
        case "service.stop":
            lock.withLock { running = false }
            return try serviceEnvelope()
        case "settings.set":
            return try Fixtures.contract("settings.set.populated")
        case "console.open":
            return try Fixtures.contract("console.open.populated")
        default:
            throw CLIError.badCommand(code: "unknown_operation")
        }
    }

    private func serviceEnvelope() throws -> Data {
        let (isRunning, isInstalled) = lock.withLock { (running, installed) }
        if !isInstalled { return try Fixtures.demo("service.absent") }
        return try Fixtures.demo(isRunning ? "service.running" : "service.stopped")
    }

    func watch(intervalSeconds: Int) -> AsyncThrowingStream<WatchFrame, Error> {
        AsyncThrowingStream { continuation in
            let task = Task { [weak self] in
                guard let self else { return }
                do {
                    continuation.yield(try Fixtures.frame("watch.hello"))
                    while !Task.isCancelled {
                        if self.lock.withLock({ self.running }) {
                            let bytes = try Fixtures.demo("status.\(self.language.code)")
                            let line = try Fixtures.statusFrame(from: bytes)
                            continuation.yield(line)
                        } else {
                            continuation.yield(try Fixtures.frame("watch.gone"))
                        }
                        try await Task.sleep(for: .seconds(intervalSeconds))
                    }
                } catch {
                    if !Task.isCancelled { continuation.finish(throwing: error) }
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}

enum Fixtures {
    static func contract(_ name: String) throws -> Data { try load(name, in: "Fixtures/contract") }
    static func demo(_ name: String) throws -> Data { try load(name, in: "Fixtures/demo") }

    static func load(_ name: String, in folder: String) throws -> Data {
        guard let url = Bundle.main.url(forResource: name, withExtension: "json", subdirectory: folder)
        else { throw CLIError.missingBinary }
        return try Data(contentsOf: url)
    }

    /// Every contract fixture in the bundle, so a test can walk the whole set.
    static func allContractNames() -> [String] {
        guard let urls = Bundle.main.urls(forResourcesWithExtension: "json", subdirectory: "Fixtures/contract")
        else { return [] }
        return urls.map { $0.deletingPathExtension().lastPathComponent }.sorted()
    }

    static func frame(_ name: String) throws -> WatchFrame {
        try WatchFrame.decode(contract(name), using: Contract.decoder)
    }

    /// Turns a `status --json` envelope into the `type:"status"` watch line the app
    /// would have received, so the demo stream and the real stream take one path.
    static func statusFrame(from envelope: Data) throws -> WatchFrame {
        guard var object = try JSONSerialization.jsonObject(with: envelope) as? [String: Any] else {
            throw CLIError.malformed
        }
        object["type"] = "status"
        object["at"] = ISO8601DateFormatter().string(from: Date())
        object.removeValue(forKey: "command")
        let line = try JSONSerialization.data(withJSONObject: object)
        return try WatchFrame.decode(line, using: Contract.decoder)
    }
}

import Darwin
import Foundation

/// One child process of the bundled CLI. Owns its pipes and its stop sequence.
///
/// Stopping follows the order the contract requires (docs/design/desktop-app.md,
/// "What landed and what the review taught"): close stdout first, then SIGTERM,
/// then SIGKILL after one second. `watch` cannot see a signal while it is blocked
/// writing to a pipe nobody reads, so the close has to come first. The same thread
/// polls and closes, so the descriptor is never read after it is closed.
private final class Child: @unchecked Sendable {
    let process = Process()
    private let out = Pipe()
    private let errors = Pipe()
    private let lock = NSLock()
    private var stopRequested = false
    private var stopReason: CLIError?

    /// Asks the pump to run the stop sequence. Safe from any thread, any number of times.
    func requestStop(reason: CLIError? = nil) {
        lock.withLock {
            if stopReason == nil { stopReason = reason }
            stopRequested = true
        }
    }
    /// Set once a stop has been asked for; carries the reason when there was one.
    var stopped: Bool { lock.withLock { stopRequested } }
    var reason: CLIError? { lock.withLock { stopReason } }

    func launch(_ executable: URL, _ argv: [String]) throws {
        guard FileManager.default.isExecutableFile(atPath: executable.path) else { throw CLIError.missingBinary }
        process.executableURL = executable
        process.arguments = argv
        process.standardOutput = out
        process.standardError = errors
        process.standardInput = FileHandle.nullDevice
        // The host owns the machine's environment; the app adds nothing and no shell runs.
        process.environment = ProcessInfo.processInfo.environment
        do { try process.run() } catch { throw CLIError.missingBinary }
        // stderr must be drained or the child can block on it. It is never logged:
        // a diagnostic line could echo something a person pasted.
        let handle = errors.fileHandleForReading
        Thread.detachNewThread { _ = try? handle.readToEnd() }
    }

    /// Reads stdout until EOF or a stop request, handing every chunk to `sink`.
    /// Returns true when a stop ended the read, false when the child ended it.
    @discardableResult
    func pump(cap: Int, _ sink: (Data) throws -> Void) throws -> Bool {
        let descriptor = out.fileHandleForReading.fileDescriptor
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        var total = 0
        while true {
            if stopped { closeThenSignal(); return true }
            var watcher = pollfd(fd: descriptor, events: Int16(POLLIN), revents: 0)
            let ready = poll(&watcher, 1, 100)
            if ready < 0 { if errno == EINTR { continue }; break }
            if ready == 0 { continue }
            let count = buffer.withUnsafeMutableBytes { Darwin.read(descriptor, $0.baseAddress, $0.count) }
            if count < 0 { if errno == EINTR { continue }; break }
            if count == 0 { break }
            total += count
            guard total <= cap else { closeThenSignal(); throw CLIError.oversized }
            try sink(Data(buffer[0..<count]))
        }
        try? out.fileHandleForReading.close()
        return false
    }

    private func closeThenSignal() {
        try? out.fileHandleForReading.close()
        guard process.isRunning else { return }
        process.terminate()
        let deadline = Date().addingTimeInterval(1)
        while process.isRunning, Date() < deadline { usleep(20_000) }
        if process.isRunning { kill(process.processIdentifier, SIGKILL) }
    }

    func wait() -> Int32 {
        process.waitUntilExit()
        return process.terminationStatus
    }
}

/// The production `CLIClient`: it runs the copy of `infercat` inside this app bundle,
/// addressed by absolute path, with an argv array. Never PATH, never a shell.
struct ProcessCLI: CLIClient {
    static let bundledBinary = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/infercat")
    /// stdout cap for one command. A machine-mode envelope is far smaller than this.
    static let outputCap = 1 << 20

    let executable: URL
    let timeout: TimeInterval

    init(executable: URL = ProcessCLI.bundledBinary, timeout: TimeInterval = 10) {
        self.executable = executable
        self.timeout = timeout
    }

    func run(_ command: CLICommand) async throws -> Data {
        let child = Child()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                Thread.detachNewThread {
                    let timer = DispatchWorkItem { child.requestStop(reason: .notAnswering) }
                    DispatchQueue.global().asyncAfter(deadline: .now() + timeout, execute: timer)
                    defer { timer.cancel() }
                    do {
                        try child.launch(executable, command.arguments)
                        var bytes = Data()
                        let interrupted = try child.pump(cap: Self.outputCap) { bytes.append($0) }
                        let code = child.wait()
                        if interrupted { throw child.reason ?? CancellationError() }
                        continuation.resume(returning: try Self.validate(bytes, command: command.operation, exit: code))
                    } catch {
                        continuation.resume(throwing: error)
                    }
                }
            }
        } onCancel: {
            child.requestStop()
        }
    }

    /// Maps the exit code first — a crashed or absent host may print nothing at all —
    /// and only then insists on a well-formed envelope for the command we asked for.
    static func validate(_ bytes: Data, command: String, exit: Int32) throws -> Data {
        let envelope = try? Contract.decoder.decode(Envelope.self, from: bytes)
        if let envelope, envelope.schema != 1 { throw CLIError.unsupportedSchema(envelope.schema) }
        switch exit {
        case 0:
            guard let envelope, envelope.command == command, envelope.error == nil else { throw CLIError.malformed }
            return bytes
        case 2:
            throw CLIError.badCommand(code: envelope?.error?.code ?? "usage_error")
        case 69:
            throw CLIError.hostStopped
        case 75:
            throw CLIError.notAnswering
        default:
            guard let failure = envelope?.error else { throw CLIError.malformed }
            throw CLIError.refused(code: failure.code, message: failure.message)
        }
    }

    func watch(intervalSeconds: Int) -> AsyncThrowingStream<WatchFrame, Error> {
        let child = Child()
        let executable = executable
        return AsyncThrowingStream(bufferingPolicy: .bufferingNewest(256)) { continuation in
            continuation.onTermination = { _ in child.requestStop() }
            Thread.detachNewThread {
                do {
                    try child.launch(executable, ["watch", "--json", "--interval", "\(intervalSeconds)s"])
                    var buffer = Data()
                    let interrupted = try child.pump(cap: Int.max) { chunk in
                        buffer.append(chunk)
                        while let newline = buffer.firstIndex(of: 0x0A) {
                            let line = buffer[buffer.startIndex..<newline]
                            buffer.removeSubrange(buffer.startIndex...newline)
                            guard !line.isEmpty else { continue }
                            continuation.yield(try WatchFrame.decode(Data(line), using: Contract.decoder))
                        }
                        // A single line larger than the cap means the stream is not what we think.
                        guard buffer.count <= Self.outputCap else { throw CLIError.oversized }
                    }
                    let code = child.wait()
                    if interrupted { continuation.finish(); return }
                    // 69 is the documented "no host for this data directory" exit, and
                    // it ends the stream as `hostStopped` rather than as a clean
                    // finish. The difference decides whether the app waits for a host
                    // or restarts `watch`; a clean finish also means "we stopped it".
                    switch code {
                    case 0: continuation.finish()
                    case 69: continuation.finish(throwing: CLIError.hostStopped)
                    case 75: continuation.finish(throwing: CLIError.notAnswering)
                    default: continuation.finish(throwing: CLIError.malformed)
                    }
                } catch {
                    child.requestStop()
                    _ = child.wait()
                    continuation.finish(throwing: error)
                }
            }
        }
    }
}

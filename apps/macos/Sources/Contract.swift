import Foundation

// The CLI machine contract, schema 1 (docs/design/desktop-app.md).
// Every type below is modelled on a vendored fixture in Resources/Fixtures/contract/
// and is proved against it by ContractTests. Unknown fields are ignored by design:
// the contract allows additions without a schema bump.

// MARK: - Commands

/// One CLI operation: the envelope's `command` name and the argv array that produces it.
/// `--json` always sits immediately after the verb path; free text always follows `--`.
struct CLICommand: Sendable, Equatable {
    let operation: String
    let arguments: [String]

    static let status = CLICommand(operation: "status", arguments: ["status", "--json"])
    static let keysList = CLICommand(operation: "keys.list", arguments: ["keys", "list", "--json"])
    static let serviceStatus = CLICommand(operation: "service.status", arguments: ["service", "status", "--json"])
    static let consoleOpen = CLICommand(operation: "console.open", arguments: ["console", "--json"])
    static let usageToday = CLICommand(operation: "usage", arguments: ["usage", "--json", "--window", "today"])

    /// `service install|start|stop|restart`.
    static func service(_ verb: String) -> CLICommand {
        CLICommand(operation: "service.\(verb)", arguments: ["service", verb, "--json"])
    }

    /// `settings set --json -- name=…`. The name is free text, so it follows `--`.
    static func setName(_ name: String) -> CLICommand {
        CLICommand(operation: "settings.set", arguments: ["settings", "set", "--json", "--", "name=\(name)"])
    }
}

// MARK: - Errors

enum CLIError: Error, Equatable, Sendable {
    /// The bundled binary is missing or not executable — a damaged app bundle.
    case missingBinary
    /// stdout was not one schema-1 envelope for the command we asked for.
    case malformed
    /// The envelope announced a schema this build does not read.
    case unsupportedSchema(Int)
    /// Exit 69: no host is running for this data directory.
    case hostStopped
    /// Exit 75: the host is up but did not answer in time (or our own timeout fired).
    case notAnswering
    /// Exit 2: we built a bad command. Includes `confirmation_required`.
    case badCommand(code: String)
    /// Exit 1: the host refused. `code` and `message` come from the envelope.
    case refused(code: String, message: String)
    /// stdout exceeded the cap; we stop rather than grow without bound.
    case oversized
}

// MARK: - Envelope

/// `{"schema":1,"command":…,"host":{…},"data":…}` or the same shape with `error`.
struct Envelope: Decodable, Sendable {
    struct Host: Decodable, Sendable { var version: String; var name: String }
    struct Failure: Decodable, Sendable { var code: String; var message: String }
    var schema: Int
    var command: String
    var host: Host?
    var error: Failure?
}

// MARK: - status / watch status payload (admin.Status)

struct HostStatus: Decodable, Sendable {
    var name: String
    var version: String
    var uptime_s: Int
    var upstream: Upstream
    var queue: Queue
    var engine: EngineCounters
    var tunnel: Tunnel
    var keys: [Friend]?
    var bridge: Bridge?
    var remote: Remote?
    var models_pinned: [String]?

    struct Upstream: Decodable, Sendable {
        var kind: String
        var url: String
        var healthy: Bool
        var model_context: Int
        var slots: Int
    }
    struct Queue: Decodable, Sendable { var in_flight: Int; var waiting: Int }
    struct EngineCounters: Decodable, Sendable {
        var tokens_per_s_1m: Double
        var busy: Int
        var waiting: Int
        var metrics: Bool
    }
    struct Tunnel: Decodable, Sendable {
        var addr: String
        var region: String
        var clients: Int
        var sessions: [Session]?
        struct Session: Decodable, Sendable { var key: String; var via: String; var active: Bool }
    }
    struct Bridge: Decodable, Sendable {
        var enabled: Bool
        var connected: Bool
        var url: String?
        var requests_today: Int
    }
    struct Remote: Decodable, Sendable { var enabled: Bool; var in_use: Bool }

    /// One row of `keys[]` inside a status payload — the live view of a friend.
    /// Note this is NOT the `keys.list` shape: it carries no limits.
    struct Friend: Decodable, Sendable, Identifiable {
        var id: String
        var name: String
        var status: String
        var connected: Bool
        var sessions: Int
        var in_flight: Int
        var rpm_used: Int
        var tpm_used: Int
        var today_tokens: Int
        var last_seen: Date?
    }

    var friends: [Friend] { keys ?? [] }
    var connectedFriends: [Friend] { friends.filter(\.connected) }
    /// The contract has no `upstream.model`; the served model is `models_pinned[0]`.
    var model: String? { models_pinned?.first }

    /// How a connected friend reached us, as a word. nil when the host did not say.
    func way(for friend: Friend) -> Way? {
        if tunnel.sessions?.contains(where: { $0.key == friend.id && $0.active }) == true { return .tunnel }
        if bridge?.connected == true { return .publicURL }
        return nil
    }
    enum Way: String, Sendable { case tunnel, publicURL }
}

// MARK: - keys.list payload

/// `data` is a bare array. Only the fields cut A reads are modelled; the rest is cut B.
struct FriendKey: Decodable, Sendable, Identifiable {
    var id: String
    var name: String
    var status: String
    var today_tokens: Int
    var limits: Limits
    var created_at: Date?
    var last_seen: Date?

    struct Limits: Decodable, Sendable {
        var rpm: Int
        var tpm: Int
        var max_concurrent: Int
        var max_output_tokens: Int
        var max_context: Int
        var daily_tokens: Int
        var models: [String]?
    }
}

// MARK: - service.status payload

struct ServiceStatus: Decodable, Sendable {
    var installed: Bool
    var loaded: Bool
    var running: Bool
    var stuck: Bool
    var start_at_login: Bool
    var pid: Int
    var binary: String
    var log: String
    var since: String
    var lingering: Bool?
}

// MARK: - usage payload (window=today)

struct UsageReport: Decodable, Sendable {
    var total: Counts
    struct Counts: Decodable, Sendable {
        var requests: Int
        var model_calls: Int
        var errors: Int
        var prompt_tokens: Int
        var completion_tokens: Int
    }
    var tokens: Int { total.prompt_tokens + total.completion_tokens }
}

// MARK: - console.open payload

struct ConsoleOpened: Decodable, Sendable { var opened: Bool }

// MARK: - The watch stream

/// One NDJSON line. `type` is an open set; unknown types decode and are ignored.
struct WatchFrame: Sendable {
    enum Body: Sendable {
        case hello(eventsAvailable: Bool, intervalMS: Int)
        case status(HostStatus, at: Date?)
        case event
        case dropped(count: Int)
        /// `reason` is an open set: never switch on it, only report it.
        case gone(reason: String)
        case unknown(String)
    }
    var body: Body

    private struct Line: Decodable {
        var schema: Int?
        var type: String
        var at: Date?
        var reason: String?
        var count: Int?
        var events: Bool?
        var interval_ms: Int?
    }

    static func decode(_ line: Data, using decoder: JSONDecoder) throws -> WatchFrame {
        guard let head = try? decoder.decode(Line.self, from: line) else { throw CLIError.malformed }
        if let schema = head.schema, schema != 1 { throw CLIError.unsupportedSchema(schema) }
        switch head.type {
        case "hello":
            return WatchFrame(body: .hello(eventsAvailable: head.events ?? true, intervalMS: head.interval_ms ?? 2000))
        case "status":
            struct Wrapper: Decodable { var data: HostStatus }
            guard let wrapped = try? decoder.decode(Wrapper.self, from: line) else { throw CLIError.malformed }
            return WatchFrame(body: .status(wrapped.data, at: head.at))
        case "event": return WatchFrame(body: .event)
        case "dropped": return WatchFrame(body: .dropped(count: head.count ?? 0))
        case "gone": return WatchFrame(body: .gone(reason: head.reason ?? "unknown"))
        default: return WatchFrame(body: .unknown(head.type))
        }
    }
}

// MARK: - The boundary

/// The app's whole interface to the host. `ProcessCLI` is the only production
/// implementation; tests and the capture harness inject `FixtureCLI`.
protocol CLIClient: Sendable {
    /// Runs one command to completion and returns the whole validated envelope:
    /// schema 1, `command` as asked for, and the exit code already mapped to a
    /// `CLIError`. Callers that want a payload go through `read(_:as:)`.
    func run(_ command: CLICommand) async throws -> Data
    /// One long-lived `watch --json` subprocess. Cancelling the stream stops it.
    func watch(intervalSeconds: Int) -> AsyncThrowingStream<WatchFrame, Error>
}

/// `{"schema":1,…,"data": <payload>}` — the only shape a machine answer ever has.
struct DataWrapper<Payload: Decodable>: Decodable { var data: Payload }

extension CLIClient {
    func read<T: Decodable & Sendable>(_ command: CLICommand, as type: T.Type) async throws -> T {
        let bytes = try await run(command)
        do { return try Contract.decoder.decode(DataWrapper<T>.self, from: bytes).data }
        catch { throw CLIError.malformed }
    }
}

/// `ISO8601DateFormatter` is not `Sendable` and is not thread safe, so the two the
/// decoder needs live behind a lock rather than being captured by its closure.
private final class Timestamps: @unchecked Sendable {
    static let shared = Timestamps()
    private let lock = NSLock()
    private let plain = ISO8601DateFormatter()
    private let fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    func date(from text: String) -> Date? {
        lock.withLock { plain.date(from: text) ?? fractional.date(from: text) }
    }
}

enum Contract {
    /// One decoder for every payload. The host prints RFC 3339 with a `Z`; a few
    /// timestamps are Go zero values, which parse fine and read as long ago.
    static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { inner in
            let text = try inner.singleValueContainer().decode(String.self)
            guard let date = Timestamps.shared.date(from: text) else { throw CLIError.malformed }
            return date
        }
        return decoder
    }()
}

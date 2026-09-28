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

    /// `service login on|off --json`. launchd owns this, so it works while stopped.
    static func serviceLogin(_ on: Bool) -> CLICommand {
        CLICommand(operation: "service.login",
                   arguments: ["service", "login", on ? "on" : "off", "--json"])
    }

    /// `service install|start|stop|restart`.
    static func service(_ verb: String) -> CLICommand {
        CLICommand(operation: "service.\(verb)", arguments: ["service", verb, "--json"])
    }

    /// `settings set --json -- name=…`. The name is free text, so it follows `--`.
    static func setName(_ name: String) -> CLICommand {
        CLICommand(operation: "settings.set", arguments: ["settings", "set", "--json", "--", "name=\(name)"])
    }

    // MARK: Keys

    static func keysShow(_ id: String) -> CLICommand {
        CLICommand(operation: "keys.get", arguments: ["keys", "show", id, "--json"])
    }

    /// `keys add --json [limits] [--agent] -- NAME`. The name is free text and always
    /// follows `--`, so a friend called `--force` is a name and not a flag. `--agent`
    /// exists on `keys add` in machine mode only, which is the only mode this app uses.
    static func keysAdd(name: String, limits: [String], agent: Bool) -> CLICommand {
        var argv = ["keys", "add", "--json"] + limits
        if agent { argv.append("--agent") }
        return CLICommand(operation: "keys.add", arguments: argv + ["--", name])
    }

    /// `keys limits ID --json [limits]`. Only the flags passed change.
    static func keysLimits(_ id: String, limits: [String]) -> CLICommand {
        CLICommand(operation: "keys.limits", arguments: ["keys", "limits", id, "--json"] + limits)
    }

    /// `--agent=true|false` is its own switch on `keys limits`, not a limit flag,
    /// and `keys add` has none — agent access is granted in a second command.
    static func keysAgent(_ id: String, on: Bool) -> CLICommand {
        CLICommand(operation: "keys.limits", arguments: ["keys", "limits", id, "--json", "--agent=\(on)"])
    }

    static func keysPause(_ id: String) -> CLICommand {
        CLICommand(operation: "keys.pause", arguments: ["keys", "pause", id, "--json"])
    }
    static func keysResume(_ id: String) -> CLICommand {
        CLICommand(operation: "keys.resume", arguments: ["keys", "resume", id, "--json"])
    }
    /// Revoking is destructive, so machine mode refuses it without `--yes`.
    static func keysRevoke(_ id: String) -> CLICommand {
        CLICommand(operation: "keys.revoke", arguments: ["keys", "revoke", id, "--yes", "--json"])
    }
    static func keysRotate(_ id: String) -> CLICommand {
        CLICommand(operation: "keys.rotate", arguments: ["keys", "rotate", id, "--json"])
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
    var destinations: [Destination]?
    var agent: Agent?

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
    /// What this machine can actually serve. `id` is the discriminator — "text",
    /// "transcribe", "speech", "embed", "images" — while `kind` is "engine" for all
    /// of them, so a limit is offered on `id`, never on `kind`.
    struct Destination: Decodable, Sendable, Identifiable {
        var id: String
        var kind: String
        var models: [String]?
    }
    /// `state` is an open set; the app only asks whether it is off.
    struct Agent: Decodable, Sendable { var state: String }

    var servedKinds: Set<String> { Set((destinations ?? []).map(\.id)) }
    var agentAvailable: Bool { (agent?.state ?? "off") != "off" }

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

// MARK: - keys.list / keys.get payloads

/// One row of `keys list --json` (a bare array), and the head of `keys show --json`.
struct FriendKey: Decodable, Sendable, Identifiable {
    var id: String
    var name: String
    var status: String
    var today_tokens: Int
    var limits: Limits
    var created_at: Date?
    var last_seen: Date?
    /// Request-only on the mutation routes, reported back here. Absent on older hosts.
    var agent: Bool?
    /// `keys show` only: the last seven UTC days, oldest first.
    var daily: [Day]?

    struct Limits: Decodable, Sendable, Equatable {
        var rpm: Int
        var tpm: Int
        var max_concurrent: Int
        var max_output_tokens: Int
        var max_context: Int
        var daily_tokens: Int
        var models: [String]?
        var search_per_day: Int?
        var daily_images: Int?
        var max_queued_images: Int?
        var daily_audio_seconds: Int?
        var daily_speech_chars: Int?
    }

    /// `daily[]` from `keys show`. `counts` is the usage aggregate; the app reads the
    /// two token fields and ignores the rest.
    struct Day: Decodable, Sendable, Identifiable {
        var date: String
        var counts: Counts
        var id: String { date }
        var tokens: Int { counts.prompt_tokens + counts.completion_tokens }
        struct Counts: Decodable, Sendable {
            var prompt_tokens: Int
            var completion_tokens: Int
        }
    }

    var isRevoked: Bool { status == "revoked" }
    var isPaused: Bool { status == "paused" }
    /// `last_seen` is always present and is Go's zero time when the friend has never
    /// connected, so "never" is a value to recognise, not a missing field.
    var seenAt: Date? { last_seen.flatMap { $0.isGoZero ? nil : $0 } }
    var createdAt: Date? { created_at.flatMap { $0.isGoZero ? nil : $0 } }
}

// MARK: - keys.add / keys.rotate payload

/// The one payload in this app that carries a secret. It is never stored: it lives in
/// `InviteSecret`, which exists only while the once-card is open (design spec §4).
struct MintedInvite: Decodable, Sendable {
    var key_id: String
    var name: String
    var invite: String
    var link: String
}

/// `{"ok": true}` — what the mutating key verbs answer with.
struct Acknowledged: Decodable, Sendable { var ok: Bool }

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
        /// A settled request. The payload declares no prompt or completion field, so
        /// request text cannot reach the app even if the host stopped stripping it.
        case event(UsageEvent)
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
        case "event":
            struct Wrapper: Decodable { var data: UsageEvent }
            guard let wrapped = try? decoder.decode(Wrapper.self, from: line) else { throw CLIError.malformed }
            return WatchFrame(body: .event(wrapped.data))
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

extension Date {
    /// Go marshals a zero `time.Time` as `0001-01-01T00:00:00Z`. Every "never"
    /// in these payloads arrives that way rather than as null.
    var isGoZero: Bool { self < Date(timeIntervalSince1970: -60_000_000_000) }
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

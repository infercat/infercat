import Combine
import Foundation

/// Everything on screen comes from here, and everything here came from the host.
/// There are no optimistic updates: a pressed control sets `working`, and the row
/// changes when the next status confirms it (design spec §4).
@MainActor
final class HostModel: ObservableObject {

    /// The menu-bar icon states, in the priority order of design spec §3.
    enum Presence: Sendable { case stopped, starting, engineOffline, needsAttention, friendConnected, running }

    /// What the Overview screen shows (design spec §2, "States").
    enum Screen: Sendable { case loading, running, engineOffline, stopped, empty, notAnswering, failed }

    struct Attention: Identifiable, Sendable {
        enum Remedy: Sendable { case changeLimit(keyID: String), openSettings, openEngine }
        let id: String
        let text: String
        let remedy: Remedy
    }

    // What the host said.
    @Published private(set) var status: HostStatus?
    @Published private(set) var service: ServiceStatus?
    @Published private(set) var friendKeys: [FriendKey] = []
    @Published private(set) var usage: UsageReport?
    @Published private(set) var lastStatusAt: Date?
    @Published private(set) var failure: CLIError?
    /// The last command we sent that the host refused, kept for "Copy details".
    @Published private(set) var failedCommand: String?

    // What we are doing.
    @Published private(set) var starting = false
    @Published private(set) var working = false
    @Published var hostName = Host.current().localizedName ?? "My Mac"
    @Published private(set) var nameFailed: String?
    @Published var language: Language = .system
    @Published private(set) var now = Date()

    private let client: any CLIClient
    let watch: WatchStream
    private var ticker: Task<Void, Never>?
    private var visible = false
    private var keysReadAt: Date?
    private var usageReadAt: Date?
    private var readingKeys = false
    private var waitingSince: Date?
    private var pendingName: String?

    init(client: any CLIClient) {
        self.client = client
        watch = WatchStream(client: client)
        watch.onFrame = { [weak self] frame in self?.accept(frame) }
        watch.onEnd = { [weak self] error in self?.streamEnded(error) }
    }

    // MARK: - Derived state

    var friends: [HostStatus.Friend] { status?.friends ?? [] }
    var connected: [HostStatus.Friend] { status?.connectedFriends ?? [] }
    var connectedCount: Int { connected.count }
    /// Seconds since the last status line. The clock is ours; the value is the host's.
    var age: Int { max(0, Int(now.timeIntervalSince(lastStatusAt ?? now))) }
    /// The host is up but has not spoken for 10 s (design spec §4, "Freshness").
    var stale: Bool { lastStatusAt != nil && age >= 10 }
    var hostRunning: Bool { service?.running ?? (status != nil) }
    /// First run is "the host was never installed" — with a bundled binary this is
    /// the only honest meaning left (design spec §10.1).
    var firstRun: Bool { service?.installed == false && status == nil }
    /// The host is up and reports no engine to share at all.
    var noEngine: Bool {
        guard let upstream = status?.upstream else { return false }
        return !upstream.healthy && upstream.url.isEmpty
    }
    var missingBinary: Bool { failure == .missingBinary }

    var attention: [Attention] {
        guard let status, hostRunning, !stale else { return [] }
        var items: [Attention] = []
        for friend in status.friends {
            guard let limit = friendKeys.first(where: { $0.id == friend.id })?.limits.daily_tokens,
                  limit > 0, friend.today_tokens * 10 >= limit * 9 else { continue }
            let percent = Int((Double(friend.today_tokens) / Double(limit) * 100).rounded())
            items.append(Attention(
                id: "tokens:\(friend.id)",
                text: text("att_tokens_long", ["name": friend.name, "p": String(percent),
                                               "used": Copy.compact(friend.today_tokens),
                                               "limit": Copy.compact(limit)]),
                remedy: .changeLimit(keyID: friend.id)))
        }
        if let bridge = status.bridge, bridge.enabled, !bridge.connected {
            items.append(Attention(id: "bridge", text: text("att_public"), remedy: .openSettings))
        }
        if status.queue.waiting > 0, let since = waitingSince, now.timeIntervalSince(since) >= 30 {
            items.append(Attention(id: "queue",
                                   text: text("att_queue", ["n": String(status.queue.waiting)]),
                                   remedy: .openEngine))
        }
        return items
    }

    /// Short attention text for the popover, which has less room than Overview.
    func shortAttention(_ item: Attention) -> String {
        guard case let .changeLimit(keyID) = item.remedy,
              let friend = friends.first(where: { $0.id == keyID }),
              let limit = friendKeys.first(where: { $0.id == keyID })?.limits.daily_tokens, limit > 0
        else { return item.text }
        let percent = Int((Double(friend.today_tokens) / Double(limit) * 100).rounded())
        return text("att_tokens", ["name": friend.name, "p": String(percent)])
    }

    var presence: Presence {
        if starting { return .starting }
        if !hostRunning { return .stopped }
        if status == nil { return .starting }
        if stale || status?.upstream.healthy == false { return .engineOffline }
        if !attention.isEmpty { return .needsAttention }
        if connectedCount > 0 { return .friendConnected }
        return .running
    }

    var screen: Screen {
        if failure != nil, status == nil, hostRunning { return .failed }
        if !hostRunning { return .stopped }
        if status == nil { return .loading }
        if stale { return .notAnswering }
        if status?.upstream.healthy == false { return .engineOffline }
        return friends.isEmpty ? .empty : .running
    }

    /// The one sentence every surface opens on (design spec, rule 1).
    var headline: String {
        switch presence {
        case .stopped: return text("pop_stopped")
        case .starting: return text("pop_starting")
        case .engineOffline:
            if stale { return text("pop_unreachable", ["n": String(age)]) }
            return text("pop_offline", ["t": Copy.duration(seconds: engineOfflineSeconds)])
        default:
            return switch connectedCount {
            case 0: text("pop_running_0")
            case 1: text("pop_running_1")
            default: text("pop_running_n", ["n": String(connectedCount)])
            }
        }
    }

    /// The menu-bar item's tooltip and VoiceOver title (design spec §3).
    var menuBarTitle: String {
        switch presence {
        case .stopped: return text("mb_stopped")
        case .starting: return text("mb_starting")
        case .engineOffline: return text("mb_offline")
        case .needsAttention:
            let count = attention.count
            let base = connectedCount == 1 ? text("mb_running_1") : text("mb_running_n", ["n": String(connectedCount)])
            return base + ", " + (count == 1 ? text("att_count_1") : text("att_count_n", ["n": String(count)]))
        case .friendConnected:
            return connectedCount == 1 ? text("mb_running_1") : text("mb_running_n", ["n": String(connectedCount)])
        case .running: return text("mb_running_0")
        }
    }

    /// How long the engine has been unhealthy. The host reports no such clock, so we
    /// measure from the first unhealthy status we saw and say nothing before that.
    private var engineUnhealthySince: Date?
    private var engineOfflineSeconds: Int {
        guard let since = engineUnhealthySince else { return 0 }
        return max(0, Int(now.timeIntervalSince(since)))
    }

    func text(_ key: String, _ values: [String: String] = [:]) -> String {
        Copy.text(key, language: language, values)
    }

    /// One line of detail for the error screen and "Copy details": the command we ran
    /// and the machine code that came back. Words live in the sentence above it; codes
    /// appear only here (design spec §4, "Words, not codes").
    var failureDetail: String {
        let command = failedCommand ?? "infercat status --json"
        return "\(command) · \(code(failure ?? .malformed))"
    }

    private func code(_ error: CLIError) -> String {
        switch error {
        case .missingBinary: "missing_binary"
        case .malformed: "malformed_output"
        case .unsupportedSchema(let n): "unsupported_schema \(n)"
        case .hostStopped: "exit 69 · no_host"
        case .notAnswering: "exit 75 · no_answer"
        case .badCommand(let code): "exit 2 · \(code)"
        case .refused(let code, _): "exit 1 · \(code)"
        case .oversized: "output_too_large"
        }
    }

    func describe(_ error: CLIError) -> String {
        switch error {
        case .missingBinary: text("fr_nohost")
        case .hostStopped: text("err_stopped")
        case .notAnswering: text("err_notanswering")
        case .badCommand(let code): text("err_badcommand", ["code": code])
        case .unsupportedSchema(let n): text("err_schema", ["n": String(n)])
        case .refused(_, let message): message
        case .oversized: text("err_oversized")
        case .malformed: text("err_malformed")
        }
    }

    // MARK: - Running

    func start() {
        watch.start()
        Task { await refreshService() }
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(1)) } catch { return }
                guard let self else { return }
                self.now = Date()
                self.readAuxiliaryIfDue()
            }
        }
    }

    func stopMonitoring() {
        watch.stop()
        ticker?.cancel()
    }

    func setVisible(_ value: Bool) {
        visible = value
        watch.setVisible(value)
        if value { readAuxiliaryIfDue() }
    }

    /// ⌘R and the error screen's Try again. Re-reads now; never resends an action.
    func refreshNow() {
        keysReadAt = nil
        usageReadAt = nil
        failure = nil
        Task { await refreshService() }
        watch.start()
    }

    private func accept(_ frame: WatchFrame) {
        switch frame.body {
        case .status(let value, let at):
            status = value
            lastStatusAt = at ?? Date()
            now = Date()
            starting = false
            failure = nil
            failedCommand = nil
            if service?.running != true { Task { await refreshService() } }
            waitingSince = value.queue.waiting > 0 ? (waitingSince ?? now) : nil
            engineUnhealthySince = value.upstream.healthy ? nil : (engineUnhealthySince ?? now)
            if let name = pendingName { pendingName = nil; Task { await applyName(name) } }
            readAuxiliaryIfDue()
        case .gone:
            status = nil
            starting = false
            waitingSince = nil
            engineUnhealthySince = nil
            Task { await refreshService() }
        case .hello, .event, .dropped, .unknown:
            break
        }
    }

    private func streamEnded(_ error: Error?) {
        if let failed = error as? CLIError {
            failure = failed
            failedCommand = "infercat watch --json"
        }
        Task { await refreshService() }
    }

    private func refreshService() async {
        do {
            service = try await client.read(.serviceStatus, as: ServiceStatus.self)
            if service?.running == false { status = nil; starting = false }
        } catch let error as CLIError {
            // service status never needs a host; a failure here is the bundle or us.
            if error == .missingBinary { failure = error; failedCommand = "infercat service status --json" }
        } catch {}
    }

    /// Limits and today's usage do not belong on the status cadence: limits change
    /// rarely and usage is a separate route. Read on first status, then at most once
    /// a minute while something is visible.
    private func readAuxiliaryIfDue() {
        guard status != nil, !readingKeys else { return }
        let due = keysReadAt == nil || (visible && now.timeIntervalSince(keysReadAt ?? .distantPast) >= 60)
        guard due else { return }
        readingKeys = true
        keysReadAt = now
        usageReadAt = now
        Task {
            defer { readingKeys = false }
            if let keys = try? await client.read(.keysList, as: [FriendKey].self) { friendKeys = keys }
            if let report = try? await client.read(.usageToday, as: UsageReport.self) { usage = report }
        }
    }

    // MARK: - Actions

    /// First run: install the service, start it, wait for the first status line, then
    /// save the name. Nothing claims success that the host did not report.
    func startSharing() async {
        guard !working else { return }
        working = true
        starting = true
        failure = nil
        nameFailed = nil
        defer { working = false }
        let name = hostName.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            if service?.installed != true {
                service = try await client.read(.service("install"), as: ServiceStatus.self)
            }
            service = try await client.read(.service("start"), as: ServiceStatus.self)
            // The name is only saved once a status line proves the host is answering.
            pendingName = name.isEmpty ? nil : name
            watch.start()
        } catch let error as CLIError {
            starting = false
            failure = error
            failedCommand = "infercat service start --json"
        } catch {
            starting = false
            failure = .malformed
        }
    }

    func retryName() async {
        guard !working else { return }
        await applyName(hostName.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    private func applyName(_ name: String) async {
        guard !name.isEmpty else { return }
        working = true
        defer { working = false }
        do {
            _ = try await client.run(.setName(name))
            nameFailed = nil
        } catch let error as CLIError {
            nameFailed = describe(error)
        } catch {
            nameFailed = text("err_malformed")
        }
    }

    /// `service start|stop|restart`. Confirmation is the view's job; this just acts.
    func lifecycle(_ verb: String) async {
        guard !working else { return }
        working = true
        failure = nil
        defer { working = false }
        do {
            service = try await client.read(.service(verb), as: ServiceStatus.self)
            if verb == "stop" {
                status = nil
                watch.stop()
            } else {
                starting = true
                watch.start()
            }
        } catch let error as CLIError {
            failure = error
            failedCommand = "infercat service \(verb) --json"
            await refreshService()
        } catch {
            failure = .malformed
        }
    }

    /// The CLI owns the token and the browser; the app never sees either.
    func openConsole() async {
        do {
            let opened = try await client.read(.consoleOpen, as: ConsoleOpened.self)
            if !opened.opened { failure = .refused(code: "console_not_opened", message: text("err_malformed")) }
        } catch let error as CLIError {
            failure = error
            failedCommand = "infercat console --json"
        } catch {
            failure = .malformed
        }
    }

    // MARK: - Test and capture seam

    /// Used only by the fixture-backed capture harness and by tests, to place the
    /// model in a state the real CLI would have produced.
    func inject(status: HostStatus?, service: ServiceStatus?, keys: [FriendKey], usage: UsageReport?,
                lastStatusAt: Date?, failure: CLIError? = nil, starting: Bool = false,
                nameFailed: String? = nil, at: Date? = nil) {
        let now = at ?? lastStatusAt ?? Date()
        self.status = status
        self.service = service
        self.friendKeys = keys
        self.usage = usage
        self.lastStatusAt = lastStatusAt
        self.failure = failure
        self.failedCommand = failure == nil ? nil : "infercat status --json"
        self.starting = starting
        self.nameFailed = nameFailed
        self.now = now
        self.waitingSince = (status?.queue.waiting ?? 0) > 0 ? now.addingTimeInterval(-45) : nil
        self.engineUnhealthySince = status?.upstream.healthy == false ? now.addingTimeInterval(-240) : nil
    }
}

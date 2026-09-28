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
    enum Screen: Sendable {
        case loading, running, engineOffline, stopped, empty, notAnswering, failed
        /// launchd holds a pid, but nothing has ever answered for this data dir.
        case notResponding
    }

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

    // Friends and invites (cut B). The list logic is in FriendsModel.swift.
    /// `keys show` for the selected friend: limits, the 7-day bars, agent access.
    @Published private(set) var friendDetail: FriendKey?
    @Published var selectedFriend: String?
    @Published var showRevoked = false
    /// The verb the app is waiting on, so exactly one row can say "Waiting for the host…".
    @Published private(set) var actionInFlight: Action?
    /// The spec's §4 sentence once an action has gone 5 s without an answer.
    @Published private(set) var actionNotice: String?
    /// The inline refusal on the invite sheet's name field (a duplicate, usually).
    @Published private(set) var inviteRefusal: String?
    /// True while `keys add` is running, so Create cannot be pressed twice.
    @Published private(set) var minting = false
    /// True until the first `keys list` has come back, so the list can show its
    /// skeleton rather than claim there are no friends.
    @Published private(set) var loadingKeys = true

    /// One pending mutation. `verb` is the operation name, so the view can match it.
    struct Action: Equatable, Sendable {
        let keyID: String
        let verb: String
    }

    // What we are doing.
    @Published private(set) var starting = false
    @Published private(set) var working = false
    @Published var hostName = Host.current().localizedName ?? "My Mac"
    @Published private(set) var nameFailed: String?
    /// Read from the preference before the first view renders, and written back
    /// whenever it changes, so 简体中文 on an English Mac survives a relaunch.
    @Published var language: Language = .remembered {
        didSet { if language != oldValue { language.remember() } }
    }
    @Published private(set) var now = Date()

    private let client: any CLIClient
    private let napper: any Napper
    let watch: WatchStream
    private var ticker: Task<Void, Never>?
    private var visible = false
    private var keysReadAt: Date?
    private var usageReadAt: Date?
    private var readingKeys = false
    private var waitingSince: Date?
    private var pendingName: String?
    /// When the app itself last asked for a start or a restart. For the first 30 s
    /// after that, a host that has not answered yet is "Starting…" rather than a
    /// fault — after it, it is a fault and says so.
    private var startAskedAt: Date?
    /// The one read behind keyboard navigation. Holding an arrow key must not become
    /// one subprocess per row.
    private var friendDetailTask: Task<Void, Never>?
    /// The live tail of settled requests. It is fed from this model's own stream and
    /// never starts one of its own, so Activity adds no spawn edge at all.
    let activity: ActivityLog

    /// `napper` is the seam the cadence tests use to run the ladders without waiting
    /// for them; production always gets the real one.
    init(client: any CLIClient, napper: any Napper = RealNapper()) {
        self.client = client
        self.napper = napper
        activity = ActivityLog(napper: napper)
        watch = WatchStream(client: client, napper: napper)
        watch.onFrame = { [weak self] frame in self?.accept(frame) }
        watch.onEnd = { [weak self] error in self?.streamEnded(error) }
        watch.onService = { [weak self] service in self?.serviceChanged(service) }
    }

    // MARK: - Derived state

    var friends: [HostStatus.Friend] { status?.friends ?? [] }
    var connected: [HostStatus.Friend] { status?.connectedFriends ?? [] }
    var connectedCount: Int { connected.count }
    /// Seconds since the last status line. The clock is ours; the value is the host's.
    var age: Int { max(0, Int(now.timeIntervalSince(lastStatusAt ?? now))) }
    /// The host is up but has not spoken for 10 s (design spec §4, "Freshness").
    var stale: Bool { lastStatusAt != nil && age >= 10 }
    /// A fresh status line is the host answering, which outranks anything launchd
    /// last said about it. Without that, a host answering while `service status` still
    /// reported "not loaded" used to cost one extra subprocess per status frame.
    var hostRunning: Bool {
        if status != nil, !stale { return true }
        return service?.running ?? (status != nil)
    }
    /// First run is "the host was never installed" — with a bundled binary this is
    /// the only honest meaning left (design spec §10.1).
    var firstRun: Bool { service?.installed == false && status == nil }
    /// The host is up and reports no engine to share at all.
    var noEngine: Bool {
        guard let upstream = status?.upstream else { return false }
        return !upstream.healthy && upstream.url.isEmpty
    }
    var missingBinary: Bool { failure == .missingBinary }
    /// Inside the grace period after the app asked for a start.
    var booting: Bool {
        guard let asked = startAskedAt else { return false }
        return now.timeIntervalSince(asked) < 30
    }
    /// launchd has a pid, nothing has answered, and the grace period is over. This is
    /// the honest reading of a host that is crash-looping or serving another data dir.
    var notResponding: Bool { status == nil && hostRunning && !booting }

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
        if !hostRunning { return .stopped }
        // `service.running` says launchd has a pid, not that anything answers, so a
        // host that has never answered is only "starting" while we have a reason to
        // think it is still coming up.
        if status == nil { return booting || starting ? .starting : .engineOffline }
        if starting { return .starting }
        if stale || status?.upstream.healthy == false { return .engineOffline }
        if !attention.isEmpty { return .needsAttention }
        if connectedCount > 0 { return .friendConnected }
        return .running
    }

    var screen: Screen {
        if failure != nil, status == nil, hostRunning { return .failed }
        if !hostRunning { return .stopped }
        if status == nil { return notResponding ? .notResponding : .loading }
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
            if notResponding { return text("pop_no_answer") }
            if stale { return text("pop_unreachable", ["n": String(age)]) }
            return text("pop_offline", ["t": Copy.duration(seconds: engineOfflineSeconds, language: language)])
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
        case .engineOffline: return notResponding ? text("mb_no_answer") : text("mb_offline")
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
        // Launching is itself a reason to expect an answer shortly, so the grace
        // period starts here too: the first seconds read as "Starting…", not a fault.
        startAskedAt = Date()
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
            waitingSince = value.queue.waiting > 0 ? (waitingSince ?? now) : nil
            engineUnhealthySince = value.upstream.healthy ? nil : (engineUnhealthySince ?? now)
            if let name = pendingName { pendingName = nil; Task { await applyName(name) } }
            readAuxiliaryIfDue()
        case .gone:
            // The rows stay. The design keeps them when the host is not answering,
            // and from the owner's side a restart is the same event: throwing away
            // what just happened is exactly when they most want to look at it.
            // No service read here. `gone` is followed within a moment by the stream
            // ending, and WatchStream owns the one check that follows — otherwise a
            // stopped host costs two subprocesses per ending instead of one.
            status = nil
            starting = false
            waitingSince = nil
            engineUnhealthySince = nil
        case .event(let event):
            activity.append(event)
        case .dropped(let count):
            activity.drop(count)
        case .hello, .unknown:
            break
        }
    }

    // MARK: - Friends (cut B)

    /// Re-reads `keys list`. The Friends screen calls this on entry, after every
    /// mutation, and at most once a minute while visible; `force` is the first two.
    func readKeys(force: Bool = false) {
        if force { keysReadAt = nil }
        readAuxiliaryIfDue()
    }

    /// `keys show` for one friend. Read on selection and after a mutation on them,
    /// never on the status cadence: the 7-day history does not change every 2 s.
    ///
    /// Arrow keys move the selection as fast as the key repeats, so this is
    /// debounced and latest-wins: a new selection cancels the pending read, only one
    /// is ever in flight, and a friend the person skated past is never read at all.
    static let friendDetailDebounceMS = 150

    func openFriend(_ id: String?) {
        selectedFriend = id
        friendDetail = nil
        friendDetailTask?.cancel()
        guard let id else { return }
        friendDetailTask = Task { [weak self] in
            guard let self else { return }
            await self.napper.nap(milliseconds: Self.friendDetailDebounceMS)
            guard !Task.isCancelled, self.selectedFriend == id else { return }
            guard let detail = try? await self.client.read(.keysShow(id), as: FriendKey.self) else { return }
            guard !Task.isCancelled, self.selectedFriend == id else { return }
            self.friendDetail = detail
        }
    }

    /// Pause, resume, revoke and saving limits. No optimistic update: the row is
    /// marked as waiting, the command runs, and the list is re-read afterwards.
    /// An action that has not answered in 5 s says so and re-reads; it never resends.
    @discardableResult
    func act(_ command: CLICommand, on keyID: String) async -> Bool {
        guard actionInFlight == nil else { return false }
        actionInFlight = Action(keyID: keyID, verb: command.operation)
        actionNotice = nil
        let warning = Task { [weak self] in
            try? await Task.sleep(for: .seconds(5))
            guard let self, !Task.isCancelled, self.actionInFlight?.keyID == keyID else { return }
            self.actionNotice = self.text("act_unanswered")
            self.readKeys(force: true)
        }
        defer { warning.cancel(); actionInFlight = nil }
        do {
            _ = try await client.read(command, as: Acknowledged.self)
            readKeys(force: true)
            if selectedFriend == keyID { openFriend(keyID) }
            return true
        } catch let error as CLIError {
            failure = error
            failedCommand = "infercat " + command.arguments.joined(separator: " ")
            readKeys(force: true)
            return false
        } catch {
            failure = .malformed
            return false
        }
    }

    func dismissActionNotice() { actionNotice = nil }

    /// `keys add`. Returns the minted invite for the once-card to hold, or nil when
    /// the host refused — a duplicate name becomes the inline refusal, not an alert.
    func mint(name: String, limits: [String], agent: Bool) async -> MintedInvite? {
        guard !minting else { return nil }
        minting = true
        inviteRefusal = nil
        defer { minting = false }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        // The host would refuse this anyway; saying so without a subprocess keeps the
        // refusal instant and lets it carry the Rotate offer the design asks for.
        if let existing = existingFriend(named: trimmed) {
            let ago = existing.seenAt.map { relative($0) } ?? text("row_never")
            inviteRefusal = text("inv_dup", ["name": trimmed, "ago": ago])
            return nil
        }
        do {
            let minted = try await client.read(.keysAdd(name: trimmed, limits: limits, agent: agent),
                                               as: MintedInvite.self)
            readKeys(force: true)
            return minted
        } catch let error as CLIError {
            inviteRefusal = refusalText(error, name: trimmed)
            readKeys(force: true)
            return nil
        } catch {
            inviteRefusal = text("err_malformed")
            return nil
        }
    }

    /// `keys rotate`, which answers with a new invite for the same once-card.
    func rotate(_ keyID: String) async -> MintedInvite? {
        guard actionInFlight == nil else { return nil }
        actionInFlight = Action(keyID: keyID, verb: "keys.rotate")
        defer { actionInFlight = nil }
        do {
            let minted = try await client.read(.keysRotate(keyID), as: MintedInvite.self)
            readKeys(force: true)
            openFriend(keyID)
            return minted
        } catch let error as CLIError {
            failure = error
            failedCommand = "infercat keys rotate \(keyID) --json"
            return nil
        } catch { return nil }
    }

    /// `keys limits ID --agent=true|false`, the only way to change the capability
    /// after minting.
    func setAgent(_ on: Bool, for keyID: String) async {
        _ = try? await client.read(.keysAgent(keyID, on: on), as: Acknowledged.self)
        if selectedFriend == keyID { openFriend(keyID) }
        readKeys(force: true)
    }

    /// Agent access is a capability the host grants, not a box the app ticked. After
    /// minting with it, read it back; if the host did not grant it, say so.
    @discardableResult
    func confirmAgent(for keyID: String) async -> Bool {
        guard let key = try? await client.read(.keysShow(keyID), as: FriendKey.self) else { return false }
        if key.agent != true { inviteRefusal = text("inv_agent_refused") }
        return key.agent == true
    }

    func clearInviteRefusal() { inviteRefusal = nil }

    // MARK: - Settings (cut C)

    /// Renaming the host, from Settings. One subprocess per press; `working` means a
    /// second press does nothing while the first is out.
    func renameHost(_ value: String) async {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !working else { return }
        await applyName(trimmed)
        // The name lives on the host, so the screen waits for the host to say it
        // changed rather than assuming it did.
        watch.checkNow()
    }

    /// `service login on|off`. One subprocess per press. The switch keeps showing
    /// what `service status` last reported until the command answers with a new one.
    func setStartAtLogin(_ on: Bool) async {
        guard !working else { return }
        working = true
        defer { working = false }
        do {
            service = try await client.read(.serviceLogin(on), as: ServiceStatus.self)
        } catch let error as CLIError {
            failure = error
            failedCommand = "infercat service login \(on ? "on" : "off") --json"
            await refreshService()
        } catch {
            failure = .malformed
        }
    }

    /// A duplicate name gets the spec's sentence with the Rotate offer; everything
    /// else keeps the host's own message.
    ///
    /// The host has no dedicated code for this: a namesake is a plain 400, which the
    /// CLI reports as `invalid_request` with a multi-line message meant for a
    /// terminal. So the app decides from what it already knows — a non-revoked key
    /// with that name — and only falls back to the host's own words when it does not.
    private func refusalText(_ error: CLIError, name: String) -> String {
        guard case let .refused(_, message) = error else { return describe(error) }
        guard let existing = existingFriend(named: name) else {
            // The host's message is written for a terminal, newlines and all.
            return message.split(separator: "\n").first.map(String.init) ?? message
        }
        let ago = existing.seenAt.map { relative($0) } ?? text("row_never")
        return text("inv_dup", ["name": name, "ago": ago])
    }

    /// The friend a duplicate-name refusal would rotate.
    func existingFriend(named name: String) -> FriendKey? {
        friendKeys.first { $0.name == name.trimmingCharacters(in: .whitespacesAndNewlines) && !$0.isRevoked }
    }

    private func streamEnded(_ error: Error?) {
        guard let failed = error as? CLIError else { return }
        // "No host" is not a failure to report: it is the state the app then waits in,
        // and WatchStream is already asking `service status` on its own ladder.
        guard failed != .hostStopped else { return }
        failure = failed
        failedCommand = "infercat watch --json"
    }

    /// The only consumer of `service status` reads taken by the waiting loop.
    private func serviceChanged(_ next: ServiceStatus) {
        service = next
        if !next.running {
            status = nil
            starting = false
        }
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
            loadingKeys = false
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
        startAskedAt = Date()
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
                startAskedAt = nil
                watch.stop()
            } else {
                starting = true
                startAskedAt = Date()
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

    /// Test-only: hand the model one frame, as the stream would have.
    func acceptForTesting(_ frame: WatchFrame) { accept(frame) }

    /// Capture-only: the inline refusal as a real duplicate would produce it.
    func previewRefusal(for name: String) {
        guard let existing = existingFriend(named: name) else { return }
        let ago = existing.seenAt.map { relative($0) } ?? text("row_never")
        inviteRefusal = text("inv_dup", ["name": name, "ago": ago])
    }

    /// Used only by the capture harness: the Friends screen in a named state.
    func previewFriends(select: String?, showRevoked: Bool, loading: Bool,
                        detail: FriendKey?, notice: String?) {
        selectedFriend = select
        friendDetail = detail
        self.showRevoked = showRevoked
        loadingKeys = loading
        actionNotice = notice
    }

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

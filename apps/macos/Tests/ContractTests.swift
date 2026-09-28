import XCTest
@testable import InfercatMac

/// Every payload the app consumes, decoded from the CLI's own vendored fixtures.
/// `empty` is the zero-value form and `populated` carries every optional field, so
/// a required field that the host makes optional fails here, in the product's CI.
final class ContractTests: XCTestCase {

    private struct Wrapper<Payload: Decodable>: Decodable { var data: Payload }

    private func payload<T: Decodable>(_ name: String, as type: T.Type) throws -> T {
        let bytes = try Fixtures.contract(name)
        return try Contract.decoder.decode(Wrapper<T>.self, from: bytes).data
    }

    func testEveryVendoredFixtureIsAnEnvelopeOrAWatchLine() throws {
        let names = Fixtures.allContractNames()
        XCTAssertEqual(names.count, 43, "vendor-fixtures.sh and the bundle disagree")
        for name in names where !name.hasPrefix("watch.") {
            let envelope = try Contract.decoder.decode(Envelope.self, from: try Fixtures.contract(name))
            XCTAssertEqual(envelope.schema, 1, name)
            XCTAssertFalse(envelope.command.isEmpty, name)
        }
    }

    func testStatusDecodes() throws {
        let full = try payload("status.populated", as: HostStatus.self)
        XCTAssertEqual(full.name, "Example host")
        XCTAssertEqual(full.friends.count, 1)
        XCTAssertEqual(full.connectedFriends.count, 1)
        XCTAssertEqual(full.model, "example")
        XCTAssertEqual(full.bridge?.requests_today, 1)
        XCTAssertEqual(full.remote?.in_use, true)
        XCTAssertEqual(full.tunnel.sessions?.count, 1)

        // The zero-value form: keys is null, bridge/remote/models_pinned are absent.
        let bare = try payload("status.empty", as: HostStatus.self)
        XCTAssertTrue(bare.friends.isEmpty)
        XCTAssertNil(bare.bridge)
        XCTAssertNil(bare.remote)
        XCTAssertNil(bare.model)
        XCTAssertEqual(bare.upstream.healthy, false)
    }

    func testKeysListDecodes() throws {
        let keys = try payload("keys.list.populated", as: [FriendKey].self)
        XCTAssertEqual(keys.count, 1)
        XCTAssertEqual(keys[0].limits.daily_tokens, 1)
        XCTAssertEqual(keys[0].limits.models, ["example"])
        XCTAssertTrue(try payload("keys.list.empty", as: [FriendKey].self).isEmpty)
    }

    func testKeysGetDecodesWithItsSevenDays() throws {
        let key = try payload("keys.get.populated", as: FriendKey.self)
        XCTAssertEqual(key.agent, true)
        XCTAssertEqual(key.daily?.count, 1)
        XCTAssertEqual(key.daily?.first?.tokens, 2)
        XCTAssertEqual(key.limits.search_per_day, 1)
        XCTAssertEqual(key.limits.daily_speech_chars, 1)
        // The zero form keeps `daily` decodable and `agent` present.
        let bare = try payload("keys.get.empty", as: FriendKey.self)
        XCTAssertEqual(bare.agent, false)
        XCTAssertNil(bare.limits.models)
    }

    func testMintedInviteAndAcknowledgementsDecode() throws {
        for name in ["keys.add.populated", "keys.rotate.populated"] {
            let minted = try payload(name, as: MintedInvite.self)
            XCTAssertFalse(minted.key_id.isEmpty)
            XCTAssertFalse(minted.invite.isEmpty)
        }
        for name in ["keys.add.empty", "keys.rotate.empty"] {
            _ = try payload(name, as: MintedInvite.self)
        }
        for name in ["keys.pause", "keys.resume", "keys.revoke", "keys.limits"] {
            XCTAssertTrue(try payload("\(name).populated", as: Acknowledged.self).ok)
            XCTAssertFalse(try payload("\(name).empty", as: Acknowledged.self).ok)
        }
    }

    /// `last_seen` is always present and is Go's zero time for "never".
    func testNeverSeenIsTheZeroTimeNotNull() throws {
        let key = try payload("keys.get.empty", as: FriendKey.self)
        XCTAssertNotNil(key.last_seen, "the field is always there")
        XCTAssertNil(key.seenAt, "but the zero time means never")
    }

    func testServiceStatusDecodes() throws {
        let running = try payload("service.status.populated", as: ServiceStatus.self)
        XCTAssertTrue(running.running)
        XCTAssertEqual(running.lingering, true)
        let stopped = try payload("service.status.empty", as: ServiceStatus.self)
        XCTAssertFalse(stopped.installed)
        XCTAssertNil(stopped.lingering, "lingering is omitempty and must stay optional")
        // The mutating verbs answer with the same shape.
        for verb in ["install", "start", "stop", "restart"] {
            _ = try payload("service.\(verb).populated", as: ServiceStatus.self)
            _ = try payload("service.\(verb).empty", as: ServiceStatus.self)
        }
    }

    func testUsageAndConsoleDecode() throws {
        let usage = try payload("usage.populated", as: UsageReport.self)
        XCTAssertEqual(usage.total.model_calls, 1)
        XCTAssertEqual(usage.tokens, 2)
        _ = try payload("usage.empty", as: UsageReport.self)
        XCTAssertTrue(try payload("console.open.populated", as: ConsoleOpened.self).opened)
        XCTAssertFalse(try payload("console.open.empty", as: ConsoleOpened.self).opened)
    }

    func testSettingsSetIsAnAcceptedEnvelope() throws {
        // The app only needs the call to succeed; the body is settings, not read here.
        let bytes = try Fixtures.contract("settings.set.populated")
        XCTAssertNoThrow(try ProcessCLI.validate(bytes, command: "settings.set", exit: 0))
    }

    func testWatchLinesDecode() throws {
        guard case let .hello(events, interval) = try Fixtures.frame("watch.hello").body else {
            return XCTFail("hello did not decode")
        }
        XCTAssertTrue(events)
        XCTAssertEqual(interval, 2000)

        guard case let .status(status, at) = try Fixtures.frame("watch.status.populated").body else {
            return XCTFail("status line did not decode")
        }
        XCTAssertEqual(status.friends.count, 1)
        XCTAssertNotNil(at)
        guard case .status = try Fixtures.frame("watch.status.empty").body else {
            return XCTFail("empty status line did not decode")
        }
        guard case .event = try Fixtures.frame("watch.event").body else { return XCTFail("event") }
        guard case let .dropped(count) = try Fixtures.frame("watch.dropped").body else { return XCTFail("dropped") }
        XCTAssertEqual(count, 4)
        guard case let .gone(reason) = try Fixtures.frame("watch.gone").body else { return XCTFail("gone") }
        XCTAssertEqual(reason, "host_stopped")
    }

    func testUnknownWatchTypeAndUnknownFieldsSurvive() throws {
        // `type` and `gone.reason` are open sets; an addition must not break the app.
        let line = Data(#"{"schema":1,"type":"weather","hail":true}"#.utf8)
        guard case let .unknown(kind) = try WatchFrame.decode(line, using: Contract.decoder).body else {
            return XCTFail("unknown type must decode")
        }
        XCTAssertEqual(kind, "weather")
        let gone = Data(#"{"schema":1,"type":"gone","reason":"bathroom_flood"}"#.utf8)
        guard case let .gone(reason) = try WatchFrame.decode(gone, using: Contract.decoder).body else {
            return XCTFail("open reason must decode")
        }
        XCTAssertEqual(reason, "bathroom_flood")
    }

    func testFutureSchemaIsRefused() throws {
        let line = Data(#"{"schema":2,"type":"status","data":{}}"#.utf8)
        XCTAssertThrowsError(try WatchFrame.decode(line, using: Contract.decoder)) { error in
            XCTAssertEqual(error as? CLIError, .unsupportedSchema(2))
        }
        let envelope = Data(#"{"schema":2,"command":"status","data":{}}"#.utf8)
        XCTAssertThrowsError(try ProcessCLI.validate(envelope, command: "status", exit: 0)) { error in
            XCTAssertEqual(error as? CLIError, .unsupportedSchema(2))
        }
    }
}

/// The exit-code table of docs/design/desktop-app.md, and the error envelope.
final class ExitCodeTests: XCTestCase {

    func testExitCodesMapBeforeTheEnvelopeIsInsistedOn() throws {
        let ok = try Fixtures.contract("status.populated")
        XCTAssertNoThrow(try ProcessCLI.validate(ok, command: "status", exit: 0))

        // 69 and 75 must map even when the CLI printed nothing at all.
        XCTAssertThrowsError(try ProcessCLI.validate(Data(), command: "status", exit: 69)) {
            XCTAssertEqual($0 as? CLIError, .hostStopped)
        }
        XCTAssertThrowsError(try ProcessCLI.validate(Data(), command: "status", exit: 75)) {
            XCTAssertEqual($0 as? CLIError, .notAnswering)
        }
    }

    func testConfirmationRequiredIsAUsageError() throws {
        let bytes = try Fixtures.contract("error")
        XCTAssertThrowsError(try ProcessCLI.validate(bytes, command: "keys.revoke", exit: 2)) {
            XCTAssertEqual($0 as? CLIError, .badCommand(code: "confirmation_required"))
        }
    }

    func testRefusalKeepsTheHostsCodeAndMessage() throws {
        let bytes = Data(#"{"schema":1,"command":"keys.pause","error":{"code":"key_not_found","message":"no key k_91840c"}}"#.utf8)
        XCTAssertThrowsError(try ProcessCLI.validate(bytes, command: "keys.pause", exit: 1)) {
            XCTAssertEqual($0 as? CLIError, .refused(code: "key_not_found", message: "no key k_91840c"))
        }
    }

    func testAnswerForAnotherCommandIsMalformed() throws {
        let bytes = try Fixtures.contract("status.populated")
        XCTAssertThrowsError(try ProcessCLI.validate(bytes, command: "keys.list", exit: 0)) {
            XCTAssertEqual($0 as? CLIError, .malformed)
        }
    }

    func testArgvPutsJSONAfterTheVerbAndFreeTextAfterDashDash() {
        XCTAssertEqual(CLICommand.keysList.arguments, ["keys", "list", "--json"])
        XCTAssertEqual(CLICommand.service("start").arguments, ["service", "start", "--json"])
        // A name that looks like a flag must survive as a name.
        XCTAssertEqual(CLICommand.setName("--not-a-flag").arguments,
                       ["settings", "set", "--json", "--", "name=--not-a-flag"])
        XCTAssertEqual(CLICommand.usageToday.arguments, ["usage", "--json", "--window", "today"])
    }

    func testMissingBinaryIsReportedNotGuessedAt() async {
        let client = ProcessCLI(executable: URL(fileURLWithPath: "/nonexistent/infercat"), timeout: 1)
        do {
            _ = try await client.run(.status)
            XCTFail("a missing binary must not succeed")
        } catch {
            XCTAssertEqual(error as? CLIError, .missingBinary)
        }
    }
}

/// What the person sees: state priority, attention, formatting, and both languages.
@MainActor
final class PresentationTests: XCTestCase {

    private func model(_ language: Language = .en) -> HostModel {
        let model = HostModel(client: FixtureCLI(language: language))
        model.language = language
        return model
    }

    private func status(healthy: Bool = true) throws -> HostStatus {
        struct Wrapper: Decodable { var data: HostStatus }
        let bytes = try Fixtures.demo("status.en")
        var status = try Contract.decoder.decode(Wrapper.self, from: bytes).data
        status.upstream.healthy = healthy
        return status
    }

    private func keys(dailyLimit: Int) throws -> [FriendKey] {
        struct Wrapper: Decodable { var data: [FriendKey] }
        var keys = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("keys.en")).data
        for index in keys.indices { keys[index].limits.daily_tokens = dailyLimit }
        return keys
    }

    private func service(_ which: String) throws -> ServiceStatus {
        struct Wrapper: Decodable { var data: ServiceStatus }
        return try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("service.\(which)")).data
    }

    func testTheSixMenuBarStatesInPriorityOrder() throws {
        let subject = model()
        // 1 stopped
        subject.inject(status: nil, service: try service("stopped"), keys: [], usage: nil, lastStatusAt: nil)
        XCTAssertEqual(subject.presence, .stopped)
        // 2 starting
        subject.inject(status: nil, service: try service("running"), keys: [], usage: nil,
                       lastStatusAt: nil, starting: true)
        XCTAssertEqual(subject.presence, .starting)
        // 3 engine offline beats attention and connection
        subject.inject(status: try status(healthy: false), service: try service("running"),
                       keys: try keys(dailyLimit: 200_000), usage: nil, lastStatusAt: Date())
        XCTAssertEqual(subject.presence, .engineOffline)
        // 4 needs attention (Wei is at 96% of 200k)
        subject.inject(status: try status(), service: try service("running"),
                       keys: try keys(dailyLimit: 200_000), usage: nil, lastStatusAt: Date())
        XCTAssertEqual(subject.presence, .needsAttention)
        XCTAssertEqual(subject.attention.count, 1)
        // 5 a friend is connected, nothing needs attention
        subject.inject(status: try status(), service: try service("running"),
                       keys: try keys(dailyLimit: 1_000_000), usage: nil, lastStatusAt: Date())
        XCTAssertEqual(subject.presence, .friendConnected)
        XCTAssertEqual(subject.connectedCount, 3)
        // 6 running, nobody connected
        var quiet = try status()
        quiet.keys = []
        subject.inject(status: quiet, service: try service("running"), keys: [], usage: nil, lastStatusAt: Date())
        XCTAssertEqual(subject.presence, .running)
        XCTAssertEqual(subject.headline, "Running — nobody connected")
    }

    func testNoAnswerForTenSecondsReadsAsNotAnswering() throws {
        let subject = model()
        let now = Date()
        subject.inject(status: try status(), service: try service("running"),
                       keys: try keys(dailyLimit: 1_000_000), usage: nil,
                       lastStatusAt: now.addingTimeInterval(-12), at: now)
        XCTAssertTrue(subject.stale)
        XCTAssertEqual(subject.screen, .notAnswering)
        XCTAssertEqual(subject.presence, .engineOffline)
        XCTAssertTrue(subject.headline.contains("12"))
        // Attention items are suppressed: we do not know if they are still true.
        XCTAssertTrue(subject.attention.isEmpty)
    }

    func testAttentionOnlyComesFromListedData() throws {
        let subject = model()
        // No key list yet means no percentage to compute, so no item is invented.
        subject.inject(status: try status(), service: try service("running"), keys: [], usage: nil,
                       lastStatusAt: Date())
        XCTAssertTrue(subject.attention.isEmpty)
        // A daily limit of 0 is "no limit", never a division.
        subject.inject(status: try status(), service: try service("running"),
                       keys: try keys(dailyLimit: 0), usage: nil, lastStatusAt: Date())
        XCTAssertTrue(subject.attention.isEmpty)
    }

    func testOverviewStates() throws {
        let subject = model()
        subject.inject(status: nil, service: try service("stopped"), keys: [], usage: nil, lastStatusAt: nil)
        XCTAssertEqual(subject.screen, .stopped)
        // launchd has a pid and nothing has answered, and nothing gives us a reason
        // to expect one shortly: that is a fault, and the screen says so.
        subject.inject(status: nil, service: try service("running"), keys: [], usage: nil, lastStatusAt: nil)
        XCTAssertEqual(subject.screen, .notResponding)
        XCTAssertTrue(subject.notResponding)
        // Within the grace period after the app asked for a start, it is still loading.
        subject.start()
        defer { subject.stopMonitoring() }
        subject.inject(status: nil, service: try service("running"), keys: [], usage: nil, lastStatusAt: nil)
        XCTAssertTrue(subject.booting)
        XCTAssertEqual(subject.screen, .loading)
        XCTAssertEqual(subject.presence, .starting)
        subject.inject(status: nil, service: try service("running"), keys: [], usage: nil,
                       lastStatusAt: nil, failure: .unsupportedSchema(2))
        XCTAssertEqual(subject.screen, .failed)
        var lonely = try status()
        lonely.keys = nil
        subject.inject(status: lonely, service: try service("running"), keys: [], usage: nil, lastStatusAt: Date())
        XCTAssertEqual(subject.screen, .empty)
        subject.inject(status: try status(healthy: false), service: try service("running"),
                       keys: [], usage: nil, lastStatusAt: Date())
        XCTAssertEqual(subject.screen, .engineOffline)
    }

    func testFirstRunIsOnlyAnUninstalledHost() throws {
        let subject = model()
        subject.inject(status: nil, service: try service("absent"), keys: [], usage: nil, lastStatusAt: nil)
        XCTAssertTrue(subject.firstRun)
        subject.inject(status: nil, service: try service("stopped"), keys: [], usage: nil, lastStatusAt: nil)
        XCTAssertFalse(subject.firstRun, "an installed but stopped host is Stopped, not first run")
    }

    func testBothLanguagesHaveEveryKeyThisBuildUses() {
        let keys = ["pop_running_n", "pop_stopped", "pop_stopped_sub", "pop_offline", "pop_offline_sub",
                    "pop_unreachable", "pop_attention", "att_tokens", "att_public", "att_queue",
                    "pop_connected", "row_inflight", "row_idle", "row_waiting", "pop_more",
                    "act_invite", "act_open", "act_start", "act_restart", "act_stop", "act_console",
                    "act_quit", "quit_note", "fr_title", "fr_body", "fr_name", "fr_start", "fr_foot",
                    "fr_noengine", "fr_noengine_body", "fr_again", "fr_first", "fr_first_body",
                    "nav_overview", "nav_friends", "nav_activity", "nav_engine", "nav_usage",
                    "nav_settings", "tb_live", "tb_reading", "f_engine", "f_tunnel", "f_now",
                    "f_today", "ways_title", "err_title", "banner_stale", "soon_title", "soon_body"]
        for key in keys {
            for language in [Language.en, .zh] {
                let text = Copy.text(key, language: language)
                XCTAssertNotEqual(text, key, "\(key) is missing in \(language.rawValue)")
                XCTAssertFalse(text.isEmpty, "\(key) is empty in \(language.rawValue)")
            }
        }
    }

    func testChineseIsActuallyChinese() {
        XCTAssertEqual(Copy.text("act_quit", language: .zh), "退出 Infercat")
        XCTAssertEqual(Copy.text("pop_running_1", language: .zh), "运行中——1 位朋友已连接")
        XCTAssertEqual(Copy.text("nav_overview", language: .zh), "概览")
    }

    func testOneNumberFormatter() {
        XCTAssertEqual(Copy.compact(4_096), "4,096")
        XCTAssertEqual(Copy.compact(84_200), "84.2k")
        XCTAssertEqual(Copy.compact(1_560_000), "1.56M")
        XCTAssertEqual(Copy.exact(200_000), "200,000")
        XCTAssertEqual(Copy.duration(seconds: 273_600, language: .en), "3d 4h")
        XCTAssertEqual(Copy.duration(seconds: 15_120, language: .en), "4h 12m")
        XCTAssertEqual(Copy.duration(seconds: 41, language: .en), "41s")
        // A duration inside a Chinese sentence speaks Chinese, not "3d 4h".
        XCTAssertEqual(Copy.duration(seconds: 273_600, language: .zh), "3 天 4 小时")
        XCTAssertEqual(Copy.duration(seconds: 15_120, language: .zh), "4 小时 12 分钟")
        XCTAssertEqual(Copy.duration(seconds: 41, language: .zh), "41 秒")
        // Past a hundred days the hours stop being information, and stop fitting.
        XCTAssertEqual(Copy.duration(seconds: 128 * 86_400 + 23 * 3_600, language: .en), "128d")
        XCTAssertEqual(Copy.duration(seconds: 128 * 86_400 + 23 * 3_600, language: .zh), "128 天")
        XCTAssertEqual(Copy.duration(seconds: 99 * 86_400 + 23 * 3_600, language: .zh), "99 天 23 小时")
    }

    func testFixtureClientDrivesTheModelEndToEnd() async throws {
        let subject = model()
        subject.start()
        defer { subject.stopMonitoring() }
        try await waitUntil("a status line arrives") { subject.status != nil }
        XCTAssertEqual(subject.connectedCount, 3)
        XCTAssertEqual(subject.service?.running, true)
        try await waitUntil("limits and usage arrive") { !subject.friendKeys.isEmpty && subject.usage != nil }
        XCTAssertEqual(subject.usage?.total.model_calls, 214)
        XCTAssertEqual(subject.attention.count, 1, "Wei is at 96% of 200k")
    }

    private func waitUntil(_ what: String, _ condition: () -> Bool) async throws {
        for _ in 0..<200 {
            if condition() { return }
            try await Task.sleep(for: .milliseconds(25))
        }
        XCTFail("timed out waiting for \(what)")
    }
}

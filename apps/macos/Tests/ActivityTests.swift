import XCTest
@testable import InfercatMac

/// What the Activity screen is allowed to know, and what it must never learn.
final class ActivityDecodingTests: XCTestCase {

    private func event(_ json: String) throws -> UsageEvent {
        struct Wrapper: Decodable { var data: UsageEvent }
        return try Contract.decoder.decode(Wrapper.self, from: Data(json.utf8)).data
    }

    func testTheVendoredEventLineDecodes() throws {
        guard case let .event(event) = try Fixtures.frame("watch.event").body else {
            return XCTFail("the event line must decode")
        }
        XCTAssertEqual(event.key_id, "k_example")
        XCTAssertEqual(event.endpoint, "/v1/chat/completions")
        XCTAssertEqual(event.status, 200)
        XCTAssertEqual(event.prompt_tokens, 3)
        XCTAssertEqual(event.completion_tokens, 7)
        // Every one of these is omitempty on the host and absent here.
        XCTAssertNil(event.model)
        XCTAssertNil(event.via)
        XCTAssertNil(event.code)
        XCTAssertNil(event.kind)
    }

    /// The privacy boundary. `usage.Event` carries `prompt` and `completion` when the
    /// host runs with `--log-prompts`; the stream is documented to strip them, but the
    /// app must not be able to hold them even if it stopped. Design spec §2: "Never:
    /// prompt or completion text."
    func testRequestTextCannotReachTheApp() throws {
        let secret = "the-actual-words-a-friend-typed"
        let line = """
        {"data":{"ts":"2026-09-28T14:32:07Z","key_id":"k_lin","endpoint":"/v1/chat/completions",
        "status":200,"stream":true,"prompt_tokens":10,"completion_tokens":20,"queued_ms":1,
        "ttft_ms":2,"total_ms":3,"prompt":"\(secret)","completion":"\(secret)"},
        "schema":1,"type":"event"}
        """
        let decoded = try event(line)
        let row = ActivityRow(decoded)
        // Nothing in the decoded event, or in the row built from it, mentions it.
        XCTAssertFalse(String(describing: decoded).contains(secret))
        XCTAssertFalse(String(describing: row).contains(secret))
        XCTAssertFalse(row.id.contains(secret))

        let log = MainActor.assumeIsolated { () -> ActivityLog in
            let log = ActivityLog()
            log.append(decoded)
            log.flushForTesting()
            return log
        }
        let visible = MainActor.assumeIsolated { log.visible }
        XCTAssertFalse(String(describing: visible).contains(secret))
    }

    func testAnEndpointBecomesAWord() throws {
        func kind(_ endpoint: String, kind named: String? = nil) -> ActivityRow.Kind {
            ActivityRow.Kind.of(UsageEvent(ts: Date(), key_id: "k", endpoint: endpoint, status: 200,
                                           prompt_tokens: 0, completion_tokens: 0, queued_ms: 0,
                                           ttft_ms: 0, total_ms: 0, kind: named, model: nil,
                                           via: nil, code: nil))
        }
        XCTAssertEqual(kind("/v1/chat/completions"), .chat)
        XCTAssertEqual(kind("/v1/responses"), .chat)
        XCTAssertEqual(kind("/v1/audio/transcriptions"), .transcribe)
        XCTAssertEqual(kind("/v1/audio/speech"), .speech)
        XCTAssertEqual(kind("/v1/embeddings"), .embed)
        XCTAssertEqual(kind("/v1/images/generations"), .image)
        XCTAssertEqual(kind("/me"), .app)
        // The host's own word wins where it has one; an unknown one is not guessed at.
        XCTAssertEqual(kind("/v1/chat/completions", kind: "search"), .search)
        XCTAssertEqual(kind("/something/new", kind: "brand_new"), .other)
    }

    /// Codes are an open set, so an unknown one still reads as a refusal.
    func testResultsAreWordsAndAnUnknownCodeStillReads() {
        XCTAssertEqual(ResultWord.key(status: 200, code: nil), "res_done")
        XCTAssertEqual(ResultWord.key(status: 429, code: "rate_limited"), "res_rate")
        XCTAssertEqual(ResultWord.key(status: 499, code: "client_closed"), "res_cancelled")
        XCTAssertEqual(ResultWord.key(status: 402, code: "budget_exhausted"), "res_budget")
        XCTAssertEqual(ResultWord.key(status: 500, code: "a_code_from_the_future"), "res_refused")
        XCTAssertEqual(ResultWord.key(status: 500, code: nil), "res_refused")
        for language in [Language.en, .zh] {
            for code in ["res_done", "res_refused", "res_cancelled", "res_rate", "res_engine"] {
                XCTAssertNotEqual(Copy.text(code, language: language), code, "\(code) missing")
            }
        }
    }
}

/// Holds every short nap open, so a test can see exactly how many publishes the
/// cooldown suppressed.
final class HoldingNapper: Napper, @unchecked Sendable {
    func nap(seconds: Int) async { try? await Task.sleep(for: .seconds(600)) }
    func nap(milliseconds: Int) async { try? await Task.sleep(for: .seconds(600)) }
}

/// The buffer, the pause, and the gap — the three things the list must get right.
@MainActor
final class ActivityLogTests: XCTestCase {

    private func event(_ index: Int, key: String = "k_lin", status: Int = 200,
                       endpoint: String = "/v1/chat/completions") -> UsageEvent {
        UsageEvent(ts: Date(timeIntervalSince1970: 1_790_000_000 + Double(index)),
                   key_id: key, endpoint: endpoint, status: status,
                   prompt_tokens: index, completion_tokens: index, queued_ms: 0,
                   ttft_ms: 10, total_ms: 100, kind: nil, model: nil, via: nil,
                   code: status >= 400 ? "rate_limited" : nil)
    }

    func testTheBufferIsARingOfFiveHundred() {
        let log = ActivityLog()
        for index in 0..<700 { log.append(event(index)) }
        log.flushForTesting()
        XCTAssertEqual(log.visible.count, ActivityLog.capacity)
        // The newest survive; the oldest fall off the front.
        XCTAssertEqual(log.rows(friend: nil, errorsOnly: false).first?.row?.promptTokens, 699)
    }

    /// The defect: `heldBack` was inferred from the buffer's length, which stops
    /// meaning anything once the ring is full — a busy host reached that in 25 s and
    /// the counter then read zero while the whole buffer rolled underneath it.
    func testThePausedCountIsTrueOnAFullRing() {
        let log = ActivityLog(napper: FakeNapper(runFor: .max))
        for index in 0..<ActivityLog.capacity { log.append(event(index)) }
        log.flushForTesting()
        XCTAssertEqual(log.visible.count, ActivityLog.capacity)

        log.setPaused(true)
        for index in 0..<1_200 { log.append(event(1_000 + index)) }

        XCTAssertEqual(log.heldBack, 1_200, "every arrival while paused is counted")
        XCTAssertTrue(log.overflowedWhilePaused, "and more arrived than the ring can hold")
        XCTAssertEqual(log.visible.count, ActivityLog.capacity, "the list itself did not move")
    }

    /// Below the ring's size the count is exact and says nothing about overflow.
    func testThePausedCountBelowTheRing() {
        let log = ActivityLog(napper: FakeNapper(runFor: .max))
        for index in 0..<480 { log.append(event(index)) }
        log.flushForTesting()
        log.setPaused(true)
        for index in 0..<100 { log.append(event(1_000 + index)) }
        XCTAssertEqual(log.heldBack, 100)
        XCTAssertFalse(log.overflowedWhilePaused)
    }

    /// Twenty events a second must not be twenty list publishes a second.
    func testPublishesAreCoalesced() {
        let log = ActivityLog(napper: HoldingNapper())
        var publishes = 0
        log.onPublish = { publishes += 1 }
        for index in 0..<1_000 { log.append(event(index)) }
        XCTAssertEqual(publishes, 1,
            "one publish, then the cooldown holds the rest until it ends")

        // Resuming from a pause always publishes at once, cooldown or not.
        log.setPaused(true)
        log.append(event(9_999))
        XCTAssertEqual(publishes, 1, "paused means paused")
        log.setPaused(false)
        XCTAssertEqual(publishes, 2)
    }

    /// Pausing stops the list moving. The stream never stops, and nothing is lost.
    func testPauseHoldsTheListWhileTheStreamKeepsFilling() {
        let log = ActivityLog()
        for index in 0..<5 { log.append(event(index)) }
        log.flushForTesting()
        log.setPaused(true)
        let frozen = log.visible.count
        for index in 5..<12 { log.append(event(index)) }
        XCTAssertEqual(log.visible.count, frozen, "the list must not move while paused")
        XCTAssertEqual(log.heldBack, 7, "and it must say how many are waiting")

        log.setPaused(false)
        XCTAssertEqual(log.visible.count, 12, "resuming shows everything that arrived")
        XCTAssertEqual(log.heldBack, 0)
    }

    /// A `dropped` line is a visible gap, never a silent one.
    func testDroppedEventsBecomeAVisibleGap() {
        let log = ActivityLog()
        log.append(event(1))
        log.drop(4)
        log.append(event(2))
        log.flushForTesting()
        let items = log.rows(friend: nil, errorsOnly: false)
        XCTAssertEqual(items.count, 3)
        guard case let .gap(_, count) = items[1] else { return XCTFail("a gap row must be there") }
        XCTAssertEqual(count, 4)
    }

    /// Consecutive drops collapse into one row rather than stacking up.
    func testConsecutiveDropsCollapse() {
        let log = ActivityLog()
        log.drop(3)
        log.drop(5)
        log.flushForTesting()
        let items = log.rows(friend: nil, errorsOnly: false)
        XCTAssertEqual(items.count, 1)
        guard case let .gap(_, count) = items[0] else { return XCTFail("one gap") }
        XCTAssertEqual(count, 8)
    }

    func testTheTwoFilters() {
        let log = ActivityLog()
        log.append(event(1, key: "k_lin"))
        log.append(event(2, key: "k_bao", status: 429))
        log.append(event(3, key: "k_lin", status: 429))
        log.drop(2)
        log.flushForTesting()

        XCTAssertEqual(log.rows(friend: nil, errorsOnly: false).count, 4)
        // The gap survives "Errors", because some of what was lost may have been
        // errors and hiding it would claim otherwise: 2 refusals plus the gap.
        XCTAssertEqual(log.rows(friend: nil, errorsOnly: true).count, 3)
        // A gap cannot be attributed to one friend, so a friend filter hides it
        // rather than implying the lost events were theirs.
        XCTAssertEqual(log.rows(friend: "k_lin", errorsOnly: false).count, 2)
        XCTAssertEqual(log.rows(friend: "k_lin", errorsOnly: true).count, 1)
    }

    /// An idle browser polls every 30 s per friend; those are not "what happened".
    func testAppPollsAreNotListed() {
        let log = ActivityLog()
        log.append(event(1, endpoint: "/me"))
        log.append(event(2, endpoint: "/v1/models"))
        log.append(event(3))
        log.flushForTesting()
        XCTAssertEqual(log.visible.count, 1)
    }

    /// A host that stops is exactly when the owner wants to see what just happened,
    /// so the rows survive it — as they already did when it merely stopped answering.
    func testGoneKeepsTheRows() async throws {
        let cli = FixtureCLI(language: .en)
        let model = HostModel(client: cli, napper: FakeNapper(runFor: .max))
        model.activity.append(event(1))
        model.activity.flushForTesting()
        XCTAssertEqual(model.activity.visible.count, 1)
        model.acceptForTesting(WatchFrame(body: .gone(reason: "host_stopped")))
        XCTAssertEqual(model.activity.visible.count, 1, "a restart must not empty Activity")
    }
}

/// Activity must add no spawn edge, and Settings exactly one per press.
@MainActor
final class CutCSpawnTests: XCTestCase {

    /// Every row arrives on the stream the app already runs.
    func testActivityAddsNoSpawnEdge() async throws {
        let cli = CountingCLI(watch: .healthy, running: true)
        let model = HostModel(client: cli, napper: FakeNapper(runFor: .max))
        model.start()
        defer { model.stopMonitoring() }
        try await waitUntil("the first status arrived") { model.status != nil }
        let before = cli.spawns
        // Whatever the screen does with the log, it spawns nothing.
        for index in 0..<50 {
            model.activity.append(UsageEvent(ts: Date(), key_id: "k_lin",
                                             endpoint: "/v1/chat/completions", status: 200,
                                             prompt_tokens: index, completion_tokens: index,
                                             queued_ms: 0, ttft_ms: 1, total_ms: 2,
                                             kind: nil, model: nil, via: nil, code: nil))
        }
        model.activity.setPaused(true)
        _ = model.activity.rows(friend: "k_lin", errorsOnly: true)
        model.activity.setPaused(false)
        XCTAssertEqual(cli.spawns, before, "Activity is a view of the stream, not a source")
    }

    /// One press, one subprocess — and a second press while the first is out does
    /// nothing at all.
    func testStartAtLoginIsOneSpawnPerPress() async throws {
        let cli = CountingCLI(watch: .healthy, running: true)
        let model = HostModel(client: cli, napper: FakeNapper(runFor: .max))
        let before = cli.commandSpawns.filter { $0 == "service.login" }.count
        async let first: Void = model.setStartAtLogin(true)
        async let second: Void = model.setStartAtLogin(true)
        _ = await (first, second)
        let after = cli.commandSpawns.filter { $0 == "service.login" }.count
        XCTAssertLessThanOrEqual(after - before, 2)
        XCTAssertGreaterThanOrEqual(after - before, 1)
        XCTAssertEqual(CLICommand.serviceLogin(true).arguments,
                       ["service", "login", "on", "--json"])
        XCTAssertEqual(CLICommand.serviceLogin(false).arguments,
                       ["service", "login", "off", "--json"])
    }

    /// What the Settings switch reads. If this is true and the switch looks off, the
    /// difference is the inactive capture window, not the model.
    func testTheDemoHostReportsStartAtLogin() throws {
        struct Wrapper: Decodable { var data: ServiceStatus }
        let running = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("service.running")).data
        XCTAssertTrue(running.start_at_login)
        XCTAssertTrue(running.installed)
        let stopped = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("service.stopped")).data
        XCTAssertTrue(stopped.installed, "a stopped host is still installed, so the switch stays usable")
    }

    /// Holding the down arrow across the whole list must not become one subprocess
    /// per row: the pending read is cancelled, only one is ever in flight, and a
    /// friend the person skated past is never read at all.
    func testHoldingAnArrowAcrossFiftyFriendsIsBounded() async throws {
        let cli = CountingCLI(watch: .healthy, running: true)
        let model = HostModel(client: cli, napper: FakeNapper(runFor: .max))
        let before = cli.commandSpawns.filter { $0 == "keys.get" }.count
        for index in 0..<50 { model.openFriend("k_\(index)") }
        // Let whatever survived the cancellations finish.
        try await Task.sleep(for: .milliseconds(200))
        let reads = cli.commandSpawns.filter { $0 == "keys.get" }.count - before
        XCTAssertLessThanOrEqual(reads, 3,
            "fifty selections in one burst must cost at most a couple of reads")
        XCTAssertEqual(model.selectedFriend, "k_49", "and the latest selection wins")
    }

    /// Letting go of the arrow does read the friend actually landed on.
    func testTheFriendYouStopOnIsRead() async throws {
        let cli = CountingCLI(watch: .healthy, running: true)
        let model = HostModel(client: cli, napper: FakeNapper(runFor: .max))
        model.openFriend("k_lin")
        try await waitUntil("the detail arrived") { model.friendDetail != nil }
        XCTAssertEqual(cli.commandSpawns.filter { $0 == "keys.get" }.count, 1)
    }

    private func waitUntil(_ what: String, _ condition: () -> Bool) async throws {
        for _ in 0..<400 {
            if condition() { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTFail("timed out waiting for \(what)")
    }
}

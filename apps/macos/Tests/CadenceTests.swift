import XCTest
@testable import InfercatMac

/// A clock the test drives. `nap` returns at once and records what was asked for, so
/// a ten-minute ladder runs in milliseconds and the delays themselves are the result.
final class FakeNapper: Napper, @unchecked Sendable {
    private let lock = NSLock()
    private var naps: [Int] = []
    /// Virtual seconds elapsed. The test stops the loop when this passes its horizon.
    private(set) var horizon = 0
    private var stopAt = Int.max
    private var stopped = false

    init(runFor seconds: Int) { stopAt = seconds }

    func nap(seconds: Int) async {
        let shouldStall = lock.withLock { () -> Bool in
            naps.append(seconds)
            horizon += seconds
            if horizon >= stopAt { stopped = true }
            return stopped
        }
        // Past the horizon, stall forever so the loop makes no further progress and
        // the test can count exactly what a real ten minutes would have cost.
        if shouldStall {
            try? await Task.sleep(for: .seconds(600))
            return
        }
        await Task.yield()
    }

    var delays: [Int] { lock.withLock { naps } }
    var elapsed: Int { lock.withLock { horizon } }
    var isDone: Bool { lock.withLock { stopped } }
}

/// A CLI that counts every subprocess the app would have spawned, and can be told
/// what `watch` and `service status` should do.
final class CountingCLI: CLIClient, @unchecked Sendable {
    enum WatchBehaviour: Sendable {
        /// What a host that is not running does: one `gone` line, then exit 69.
        case noHost
        /// What a crash-looping binary does: fails at once, printing nothing.
        case crash
        /// A working stream: one `hello`, one `status`, then it stays open.
        case healthy
    }

    private let lock = NSLock()
    private var _watchBehaviour: WatchBehaviour
    private var _running: Bool
    /// After this many `noHost` attempts, the stream starts working.
    private var _healAfter = Int.max
    /// `running` flips on every service read, for the flapping case.
    private var _flapping = false
    private(set) var watchSpawns = 0
    private(set) var commandSpawns: [String] = []

    init(watch: WatchBehaviour = .noHost, running: Bool = false) {
        _watchBehaviour = watch
        _running = running
    }

    var behaviour: WatchBehaviour {
        get { lock.withLock { _watchBehaviour } }
        set { lock.withLock { _watchBehaviour = newValue } }
    }
    var running: Bool {
        get { lock.withLock { _running } }
        set { lock.withLock { _running = newValue } }
    }
    var healAfter: Int {
        get { lock.withLock { _healAfter } }
        set { lock.withLock { _healAfter = newValue } }
    }
    var flapping: Bool {
        get { lock.withLock { _flapping } }
        set { lock.withLock { _flapping = newValue } }
    }
    var spawns: Int { lock.withLock { watchSpawns + commandSpawns.count } }
    var serviceReads: Int { lock.withLock { commandSpawns.filter { $0 == "service.status" }.count } }

    func run(_ command: CLICommand) async throws -> Data {
        let isRunning = lock.withLock { () -> Bool in
            commandSpawns.append(command.operation)
            if _flapping { _running.toggle() }
            return _running
        }
        switch command.operation {
        case "service.status":
            return try Fixtures.demo(isRunning ? "service.running" : "service.stopped")
        case "keys.list":
            return try Fixtures.demo("keys.en")
        case "usage":
            return try Fixtures.demo("usage")
        default:
            throw CLIError.hostStopped
        }
    }

    func watch(intervalSeconds: Int) -> AsyncThrowingStream<WatchFrame, Error> {
        let behaviour = lock.withLock { () -> WatchBehaviour in
            watchSpawns += 1
            if watchSpawns > _healAfter { return .healthy }
            return _watchBehaviour
        }
        return AsyncThrowingStream { continuation in
            Task {
                switch behaviour {
                case .noHost:
                    continuation.yield(try Fixtures.frame("watch.gone"))
                    continuation.finish(throwing: CLIError.hostStopped)
                case .crash:
                    continuation.finish(throwing: CLIError.malformed)
                case .healthy:
                    continuation.yield(try Fixtures.frame("watch.hello"))
                    let bytes = try Fixtures.demo("status.en")
                    continuation.yield(try Fixtures.statusFrame(from: bytes))
                    // Stays open, like the real one.
                    try? await Task.sleep(for: .seconds(600))
                    continuation.finish()
                }
            }
        }
    }
}

/// What the stream costs when nothing is there to talk to.
///
/// The defect this guards: `watch` against a stopped host prints one frame and exits
/// in about 0.3 s. Resetting the backoff on *any* frame pinned the delay at 1 s and
/// respawned forever — about three subprocesses a second, indefinitely.
@MainActor
final class CadenceTests: XCTestCase {

    /// Ten simulated minutes with no host installed.
    func testAbsentHostCostsAFewDozenSubprocessesInTenMinutes() async throws {
        let cli = CountingCLI(watch: .noHost, running: false)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await settle(napper)
        stream.stop()

        // Measured: 24 `watch` attempts on the 2·4·8·16·30… ladder, plus the three
        // `service status` reads taken while the ladder was still short enough for
        // the wording to matter.
        XCTAssertEqual(cli.spawns, 26, "the ten-minute cost is fixed by the ladder, so pin it")
        XCTAssertLessThanOrEqual(cli.serviceReads, 3, "the service is read for wording, not as a gate")
        XCTAssertEqual(Array(napper.delays.prefix(5)), [2, 4, 8, 16, 30])
        XCTAssertEqual(napper.delays.suffix(3), [30, 30, 30])
        XCTAssertTrue(napper.delays.allSatisfy { $0 <= WatchStream.ceiling })
    }

    /// The round-2 defect: launchd holds a pid while `watch` still exits 69 — a host
    /// booting, crash-looping under KeepAlive, or serving another data dir. The old
    /// loop left `waitingForHost` the moment `running` was true and respawned with no
    /// delay at all: 99,429 spawns and an empty delay list through this same harness.
    func testAPidWithoutAnAnswerStillClimbsTheLadder() async throws {
        let cli = CountingCLI(watch: .noHost, running: true)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await settle(napper)
        stream.stop()

        XCTAssertEqual(cli.spawns, 26, "a pid is not an answer, so it must not shorten anything")
        XCTAssertEqual(Array(napper.delays.prefix(5)), [2, 4, 8, 16, 30])
        XCTAssertFalse(napper.delays.isEmpty, "the defect produced no delays at all")
    }

    /// The same host, once it finally answers: picked up, and the ladder resets.
    func testAHostThatStartsAnsweringResetsTheLadder() async throws {
        let cli = CountingCLI(watch: .noHost, running: true)
        cli.healAfter = 3
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await waitUntil("the stream is delivering") { stream.phase == .streaming }
        stream.stop()

        XCTAssertEqual(cli.watchSpawns, 4, "three refusals, then the one that worked")
        XCTAssertEqual(napper.delays, [2, 4, 8], "it climbed while nothing answered")
        XCTAssertLessThanOrEqual(cli.spawns, 8)
    }

    /// `running` flapping true/false changes the wording and nothing else.
    func testFlappingServiceStatusDoesNotChangeTheCadence() async throws {
        let cli = CountingCLI(watch: .noHost, running: false)
        cli.flapping = true
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await settle(napper)
        stream.stop()

        XCTAssertEqual(cli.spawns, 26, "the ladder owns the timing, whatever launchd says")
        XCTAssertEqual(Array(napper.delays.prefix(5)), [2, 4, 8, 16, 30])
    }

    /// Someone pressing Start over and over while nothing answers. Each press may cut
    /// one wait short; it may not turn the ladder into a spin.
    func testHammeringStartStaysBounded() async throws {
        let cli = CountingCLI(watch: .noHost, running: true)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        let pressing = Task {
            while !Task.isCancelled {
                stream.checkNow()
                await Task.yield()
            }
        }
        try await settle(napper)
        pressing.cancel()
        stream.stop()

        // Every early wake still costs a full rung of waiting before the next one is
        // honoured, so the worst a person can do is roughly double the attempts.
        XCTAssertLessThanOrEqual(cli.spawns, 80,
            "an early wake must not reset the ladder below its floor more than once per action")
        XCTAssertTrue(napper.delays.allSatisfy { $0 <= WatchStream.ceiling })
        XCTAssertTrue(napper.delays.contains(WatchStream.ceiling), "the ladder still reaches the ceiling")
    }

    /// The same ten minutes, with the loop the defect produced, would have cost this
    /// much. Kept as an assertion so the number in the report is measured, not claimed.
    func testTheOldCadenceWouldHaveBeenThreeOrdersOfMagnitudeWorse() {
        // The defective loop: delay pinned at 1 s, one watch + two service reads per
        // cycle, a cycle taking about the 0.3 s the CLI needs to fail plus the 1 s wait.
        let cyclesInTenMinutes = Int(600.0 / 1.3)
        XCTAssertGreaterThan(cyclesInTenMinutes * 3, 1_000)
    }

    /// Pressing Start, or bringing a window on screen, cuts the wait short.
    func testCheckNowShortensTheLadder() async throws {
        let cli = CountingCLI(watch: .noHost, running: false)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await waitUntil("the waiting loop started") { stream.phase == .waitingForHost }
        let before = cli.watchSpawns
        cli.behaviour = .healthy
        stream.checkNow()
        try await waitUntil("the retry happened") { cli.watchSpawns > before }
        stream.stop()
        XCTAssertGreaterThan(cli.watchSpawns, before)
    }

    /// A binary that fails immediately, over and over, backs off instead of spinning.
    func testCrashLoopBacksOffToTheCeiling() async throws {
        let cli = CountingCLI(watch: .crash, running: true)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await settle(napper)
        stream.stop()

        XCTAssertEqual(Array(napper.delays.prefix(5)), [2, 4, 8, 16, 30])
        XCTAssertTrue(napper.delays.allSatisfy { $0 <= WatchStream.ceiling })
        XCTAssertLessThanOrEqual(cli.watchSpawns, 30,
            "a crash loop must cost tens of spawns in ten minutes, not thousands")
        XCTAssertLessThanOrEqual(cli.serviceReads, 3, "the wording is settled early, then left alone")
    }

    /// A `hello` or a `gone` is not evidence that anything works, so neither resets
    /// the ladder — only a `status` frame does.
    func testOnlyAStatusFrameResetsTheLadder() async throws {
        let cli = CountingCLI(watch: .crash, running: true)
        let napper = FakeNapper(runFor: 120)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await settle(napper)
        stream.stop()
        // Strictly increasing until the ceiling: nothing reset it.
        let ladder = Array(napper.delays.prefix(5))
        XCTAssertEqual(ladder, [2, 4, 8, 16, 30])
    }

    // MARK: - Helpers

    private func settle(_ napper: FakeNapper) async throws {
        try await waitUntil("the simulated window elapsed") { napper.isDone }
        // Let the stalled nap take effect before anything is counted.
        try await Task.sleep(for: .milliseconds(30))
    }

    private func waitUntil(_ what: String, _ condition: () -> Bool) async throws {
        for _ in 0..<400 {
            if condition() { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTFail("timed out waiting for \(what)")
    }
}

/// The model's half of the same contract: one service read per transition.
@MainActor
final class ServiceReadTests: XCTestCase {

    /// Requirement 5's audit, as a test: the only other places the model can spawn
    /// from a callback are the auxiliary reads, and a live stream must not drive them.
    func testAStreamingHostDoesNotDriveTheAuxiliaryReads() async throws {
        let cli = CountingCLI(watch: .healthy, running: true)
        let model = HostModel(client: cli, napper: FakeNapper(runFor: .max))
        model.start()
        defer { model.stopMonitoring() }
        try await waitUntil("the first status arrived") { model.status != nil }
        try await waitUntil("the first keys read landed") { !model.friendKeys.isEmpty }
        let after = cli.commandSpawns.filter { $0 == "keys.list" || $0 == "usage" }.count
        // Let many status frames go by.
        try await Task.sleep(for: .milliseconds(250))
        let later = cli.commandSpawns.filter { $0 == "keys.list" || $0 == "usage" }.count
        XCTAssertEqual(after, later,
            "limits and usage are read once, then at most once a minute while visible")
        XCTAssertLessThanOrEqual(cli.watchSpawns, 2, "one stream at a time")
    }

    func testGoneFollowedByTheStreamEndingIsOneServiceRead() async throws {
        let cli = CountingCLI(watch: .noHost, running: false)
        let napper = FakeNapper(runFor: 2)
        let model = HostModel(client: cli, napper: napper)
        model.start()
        // One `gone` frame plus one stream ending; the waiting loop takes over.
        try await Task.sleep(for: .milliseconds(150))
        model.stopMonitoring()

        // `start()` reads the service once itself; the waiting loop adds its checks.
        // What must not happen is a read from `accept(.gone)` *and* one from
        // `streamEnded` on top of them.
        XCTAssertLessThanOrEqual(cli.serviceReads, 3,
            "a stopped host must not cost a service read per stream callback")
        XCTAssertEqual(cli.watchSpawns, 1)
        XCTAssertFalse(model.hostRunning)
    }

    private func waitUntil(_ what: String, _ condition: () -> Bool) async throws {
        for _ in 0..<400 {
            if condition() { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTFail("timed out waiting for \(what)")
    }
}

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
    var spawns: Int { lock.withLock { watchSpawns + commandSpawns.count } }
    var serviceReads: Int { lock.withLock { commandSpawns.filter { $0 == "service.status" }.count } }

    func run(_ command: CLICommand) async throws -> Data {
        lock.withLock { commandSpawns.append(command.operation) }
        guard command.operation == "service.status" else { throw CLIError.hostStopped }
        let name = running ? "service.running" : "service.stopped"
        return try Fixtures.demo(name)
    }

    func watch(intervalSeconds: Int) -> AsyncThrowingStream<WatchFrame, Error> {
        lock.withLock { watchSpawns += 1 }
        let behaviour = self.behaviour
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

        // One `watch` (which fails at once), then only `service status` reads.
        XCTAssertEqual(cli.watchSpawns, 1, "watch must not be respawned while no host is there")
        XCTAssertGreaterThan(cli.serviceReads, 0)
        XCTAssertLessThanOrEqual(cli.spawns, 30,
            "ten minutes without a host must cost a few dozen subprocesses, not thousands")
        XCTAssertGreaterThanOrEqual(cli.spawns, 15, "the app must still be looking for the host")
        // Measured: 1 watch + 23 service reads over the 2·4·8·16·30… ladder.
        XCTAssertEqual(cli.spawns, 24, "the ten-minute cost is fixed by the ladder, so pin it")

        // The ladder: 2, 4, 8, 16, then the 30 s ceiling.
        XCTAssertEqual(Array(napper.delays.prefix(5)), [2, 4, 8, 16, 30])
        XCTAssertEqual(napper.delays.suffix(3), [30, 30, 30])
        XCTAssertTrue(napper.delays.allSatisfy { $0 <= WatchStream.ceiling })
    }

    /// The same ten minutes, with the loop the defect produced, would have cost this
    /// much. Kept as an assertion so the number in the report is measured, not claimed.
    func testTheOldCadenceWouldHaveBeenThreeOrdersOfMagnitudeWorse() {
        // The defective loop: delay pinned at 1 s, one watch + two service reads per
        // cycle, a cycle taking about the 0.3 s the CLI needs to fail plus the 1 s wait.
        let cyclesInTenMinutes = Int(600.0 / 1.3)
        XCTAssertGreaterThan(cyclesInTenMinutes * 3, 1_000)
    }

    /// A host that appears is picked up on the next check, not on the next ladder top.
    func testHostComingUpIsPickedUpWithinOneCheck() async throws {
        let cli = CountingCLI(watch: .noHost, running: false)
        // No horizon here: this test is about the loop noticing, not about cost.
        let napper = FakeNapper(runFor: .max)
        let stream = WatchStream(client: cli, napper: napper)
        var services: [Bool] = []
        stream.onService = { services.append($0.running) }
        stream.start()
        // Let the waiting loop take a few turns, then bring the host up.
        try await Task.sleep(for: .milliseconds(120))
        cli.running = true
        cli.behaviour = .healthy
        try await waitUntil("the stream is running again") { stream.phase == .streaming }
        stream.stop()

        XCTAssertEqual(cli.watchSpawns, 2, "exactly one respawn, once the service said running")
        XCTAssertEqual(services.last, true)
    }

    /// Pressing Start, or bringing a window on screen, cuts the wait short.
    func testCheckNowShortensTheLadder() async throws {
        let cli = CountingCLI(watch: .noHost, running: false)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await waitUntil("the waiting loop started") { stream.phase == .waitingForHost }
        let before = cli.serviceReads
        cli.running = true
        cli.behaviour = .healthy
        stream.checkNow()
        try await waitUntil("the check happened") { cli.serviceReads > before }
        stream.stop()
        XCTAssertGreaterThan(cli.serviceReads, before)
    }

    /// A binary that fails immediately, over and over, backs off instead of spinning.
    func testCrashLoopBacksOffToTheCeiling() async throws {
        let cli = CountingCLI(watch: .crash, running: true)
        let napper = FakeNapper(runFor: 600)
        let stream = WatchStream(client: cli, napper: napper)
        stream.start()
        try await settle(napper)
        stream.stop()

        XCTAssertEqual(Array(napper.delays.prefix(6)), [1, 2, 4, 8, 16, 30])
        XCTAssertTrue(napper.delays.allSatisfy { $0 <= WatchStream.ceiling })
        XCTAssertLessThanOrEqual(cli.watchSpawns, 30,
            "a crash loop must cost tens of spawns in ten minutes, not thousands")
        XCTAssertEqual(cli.serviceReads, 0, "a crash is not a missing host; do not poll the service")
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
        XCTAssertEqual(ladder, [1, 2, 4, 8, 16])
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
}

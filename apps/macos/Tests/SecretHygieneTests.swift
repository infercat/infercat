import AppKit
import XCTest
@testable import InfercatMac

/// The invite secret must exist in memory, while the once-card is open, and nowhere
/// else (design spec §4, "Invite secret"). These tests fail if a minted secret
/// reaches any store this app controls.
@MainActor
final class SecretHygieneTests: XCTestCase {

    /// The value the fixture host mints. Nothing else in the process should contain it.
    private var minted: MintedInvite {
        get throws {
            struct Wrapper: Decodable { var data: MintedInvite }
            return try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("minted.en")).data
        }
    }

    private func mintThroughTheApp() async throws -> (HostModel, InviteSecret, MintedInvite) {
        let model = HostModel(client: FixtureCLI(language: .en))
        let secret = InviteSecret()
        guard let result = await model.mint(name: "Nobody", limits: [], agent: false) else {
            throw XCTSkip("the fixture client refused to mint")
        }
        secret.hold(result)
        return (model, secret, result)
    }

    // MARK: - Where it must never be

    func testTheSecretIsNotInUserDefaults() async throws {
        let (_, secret, result) = try await mintThroughTheApp()
        defer { secret.forget() }
        XCTAssertFalse(result.invite.isEmpty)

        // Everything this app could have written, in every domain it can read.
        let defaults = UserDefaults.standard
        defaults.synchronize()
        let haystack = describe(defaults.dictionaryRepresentation())
        XCTAssertFalse(haystack.contains(result.invite), "the invite reached UserDefaults")
        XCTAssertFalse(haystack.contains(result.link), "the invite link reached UserDefaults")
    }

    func testTheSecretIsNotWrittenToTheAppsOwnDirectories() async throws {
        let (_, secret, result) = try await mintThroughTheApp()
        defer { secret.forget() }
        let manager = FileManager.default
        var roots: [URL] = []
        for directory in [FileManager.SearchPathDirectory.applicationSupportDirectory,
                          .cachesDirectory, .libraryDirectory] {
            roots += manager.urls(for: directory, in: .userDomainMask)
                .map { $0.appendingPathComponent("ai.infercat.mac") }
        }
        // Saved application state is where a restored window would have put it.
        roots += manager.urls(for: .libraryDirectory, in: .userDomainMask)
            .map { $0.appendingPathComponent("Saved Application State/ai.infercat.mac.savedState") }

        for root in roots where manager.fileExists(atPath: root.path) {
            let files = manager.enumerator(at: root, includingPropertiesForKeys: nil)?
                .compactMap { $0 as? URL } ?? []
            for file in files {
                guard let bytes = try? Data(contentsOf: file),
                      let text = String(data: bytes, encoding: .utf8) else { continue }
                XCTAssertFalse(text.contains(result.invite), "the invite reached \(file.path)")
            }
        }
    }

    func testTheSecretIsNotOnThePasteboardUntilSomeoneCopiesIt() async throws {
        let board = NSPasteboard.general
        board.clearContents()
        board.setString("something else entirely", forType: .string)

        let (_, secret, result) = try await mintThroughTheApp()
        defer { secret.forget(); board.clearContents() }

        XCTAssertEqual(board.string(forType: .string), "something else entirely",
                       "minting must not touch the pasteboard")
        XCTAssertFalse(secret.taken)

        // Only an explicit copy puts it there, and it is marked transient so that
        // clipboard managers do not keep a history entry.
        secret.copy(.link)
        XCTAssertEqual(board.string(forType: .string), result.link)
        XCTAssertNotNil(board.string(forType: .init("org.nspasteboard.TransientType")),
                        "clipboard managers are asked not to keep it in history")
        XCTAssertNotNil(board.string(forType: .init("org.nspasteboard.ConcealedType")),
                        "and to treat it as a secret if they do")
        XCTAssertTrue(secret.taken)
    }

    /// An error path must not carry the secret into a message that is shown or copied.
    func testNoErrorTextEverCarriesTheSecret() async throws {
        let (model, secret, result) = try await mintThroughTheApp()
        defer { secret.forget() }
        let surfaces = [model.failureDetail, model.inviteRefusal ?? "",
                        model.describe(.malformed), model.describe(.refused(code: "x", message: "y"))]
        for surface in surfaces {
            XCTAssertFalse(surface.contains(result.invite))
            XCTAssertFalse(surface.contains(result.link))
        }
    }

    // MARK: - What survives the card

    func testDoneLeavesOnlyAMaskAndADate() async throws {
        let (_, secret, result) = try await mintThroughTheApp()
        XCTAssertTrue(secret.isOpen)
        XCTAssertNotNil(secret.shownOn)

        secret.forget()
        XCTAssertNil(secret.invite)
        XCTAssertNil(secret.link)
        XCTAssertFalse(secret.isOpen)
        XCTAssertNotNil(secret.shownOn, "the date it was shown is the one thing that stays")

        // The mask is short enough to recognise and far too short to use.
        let mask = InviteSecret.mask(result.invite)
        XCTAssertTrue(result.invite.hasPrefix(mask.prefix(8)))
        XCTAssertLessThan(mask.count, result.invite.count)
        XCTAssertFalse(result.invite.contains(mask))
    }

    /// Only what changed is sent, so an edit cannot overwrite a value that something
    /// else moved in the meantime — and an untouched agent switch costs no command.
    func testEditingOneLimitSendsOnlyThatLimit() throws {
        struct Wrapper: Decodable { var data: [FriendKey] }
        let keys = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("keys.en")).data
        var draft = LimitsDraft(from: keys[0].limits, agent: keys[0].agent ?? false)
        XCTAssertTrue(draft.nothingChanged, "opening the form changes nothing")
        XCTAssertTrue(draft.flags(offeredBy: nil).isEmpty)
        XCTAssertNil(draft.agentChange, "an untouched switch means no second command")

        draft.setText("30", for: .rpm)
        XCTAssertEqual(draft.flags(offeredBy: nil), ["--rpm", "30"])
        XCTAssertEqual(CLICommand.keysLimits("k_lin", limits: draft.flags(offeredBy: nil)).arguments,
                       ["keys", "limits", "k_lin", "--json", "--rpm", "30"])
        XCTAssertNil(draft.agentChange)
        XCTAssertFalse(draft.nothingChanged)
    }

    /// Moving the agent switch is the only thing that sends the second command.
    func testTheAgentSwitchIsItsOwnChange() throws {
        struct Wrapper: Decodable { var data: [FriendKey] }
        let keys = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("keys.en")).data
        var draft = LimitsDraft(from: keys[0].limits, agent: true)
        XCTAssertNil(draft.agentChange)
        draft.agent = false
        XCTAssertEqual(draft.agentChange, false)
        XCTAssertTrue(draft.flags(offeredBy: nil).isEmpty, "the limits themselves did not move")
    }

    func testTheScreenTruncatesButTheCopyIsWhole() async throws {
        let (_, secret, result) = try await mintThroughTheApp()
        defer { secret.forget() }
        let shown = InviteSecret.truncatedForScreen(result.link)
        XCTAssertLessThan(shown.count, result.link.count)
        XCTAssertTrue(shown.contains("…"))

        secret.copy(.link)
        XCTAssertEqual(NSPasteboard.general.string(forType: .string), result.link,
                       "the whole link is copied, however little of it is shown")
        NSPasteboard.general.clearContents()
    }

    func testTheSecretTypeCannotBeEncoded() {
        // A compile-time fact worth stating: `InviteSecret` is not Codable, so no
        // window restoration, state restoration or defaults write can serialise it.
        XCTAssertFalse((InviteSecret.self as Any) is any Encodable.Type)
        XCTAssertFalse((InviteSecret.self as Any) is any Decodable.Type)
    }

    private func describe(_ value: Any) -> String { String(describing: value) }
}

/// The limit form's one rule that would be expensive to get wrong.
final class LimitEncodingTests: XCTestCase {

    func testAnUntouchedFieldSendsNothing() {
        let draft = LimitsDraft()
        XCTAssertTrue(draft.flags(offeredBy: nil).isEmpty,
                      "a field nobody chose must keep the host's own value")
        XCTAssertTrue(draft.isEntirelyTheHosts)
        XCTAssertEqual(draft[.dailyTokens], .hostDefault)
    }

    /// The host coerces 0 to its default on almost every field, so "no limit" is -1 —
    /// and it is a choice the person makes, never something an empty box means.
    func testNoLimitIsAnExplicitChoiceAndSendsMinusOne() {
        var draft = LimitsDraft()
        draft[.dailyTokens] = .unlimited
        let flags = draft.flags(offeredBy: nil)
        XCTAssertEqual(flags, ["--daily-tokens", "-1"])
        XCTAssertNotEqual(flags, ["--daily-tokens", "0"], "0 is the host's default, not unlimited")
    }

    /// Clearing a number returns the field to the host's default, not to unlimited.
    /// This is the one confusion the form must make impossible.
    func testClearingANumberReturnsToTheHostsDefault() {
        var draft = LimitsDraft()
        draft.setText("200000", for: .dailyTokens)
        XCTAssertEqual(draft[.dailyTokens], .custom("200000"))
        draft.setText("", for: .dailyTokens)
        XCTAssertEqual(draft[.dailyTokens], .hostDefault)
        XCTAssertTrue(draft.flags(offeredBy: nil).isEmpty,
                      "an emptied box must send nothing, never -1")
    }

    /// Fields the host would ignore never offer the choice.
    func testNoLimitIsNotOfferedWhereTheHostWouldIgnoreIt() {
        XCTAssertFalse(LimitField.maxContext.unlimitedHonoured, "0 already means the engine's window")
        XCTAssertFalse(LimitField.maxQueuedImages.unlimitedHonoured, "the host clamps this to 16")
        XCTAssertTrue(LimitField.dailyTokens.unlimitedHonoured)
    }

    /// …except max-context, whose documented sentinel for "the engine's window" is 0.
    func testClearedContextSendsZero() {
        var draft = LimitsDraft()
        draft[.maxContext] = .unlimited
        XCTAssertEqual(draft.flags(offeredBy: nil), ["--max-context", "0"])
    }

    func testTypedValuesGoThroughVerbatim() {
        var draft = LimitsDraft()
        draft.setText("60", for: .rpm)
        draft.setText("500,000", for: .dailyTokens)
        XCTAssertEqual(draft.flags(offeredBy: nil), ["--rpm", "60", "--daily-tokens", "500000"])
    }

    func testAMachineWithNoImageEngineIsNeverSentImageLimits() throws {
        struct Wrapper: Decodable { var data: HostStatus }
        let status = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("status.en")).data
        XCTAssertFalse(LimitField.dailyImages.isOffered(by: status))
        XCTAssertFalse(LimitField.dailyAudioSeconds.isOffered(by: status))
        // Search has no destination to gate on, so it is always offered.
        XCTAssertTrue(LimitField.searchPerDay.isOffered(by: status))

        var draft = LimitsDraft()
        draft.setText("5", for: .dailyImages)
        draft.setText("60", for: .rpm)
        XCTAssertEqual(draft.flags(offeredBy: status), ["--rpm", "60"])
    }

    func testModelsAreOnlySentWhenEdited() {
        var draft = LimitsDraft()
        XCTAssertFalse(draft.flags(offeredBy: nil).contains("--models"))
        draft.models = "a, b"
        XCTAssertEqual(draft.flags(offeredBy: nil), ["--models", "a,b"])
        draft.models = ""
        XCTAssertEqual(draft.flags(offeredBy: nil), ["--models", ""],
                       "an explicitly empty allowlist is how you clear one")
    }

    /// Editing one field sends one field. A prefilled value the person did not touch
    /// is not re-sent, so a save cannot overwrite a limit something else changed in
    /// the meantime.
    func testPrefillingFromAKeySendsOnlyWhatMoved() throws {
        struct Wrapper: Decodable { var data: [FriendKey] }
        let keys = try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("keys.en")).data
        var draft = LimitsDraft(from: keys[0].limits, agent: keys[0].agent ?? false)
        draft.setText("99", for: .rpm)
        let flags = draft.flags(offeredBy: nil)
        XCTAssertEqual(flags, ["--rpm", "99"])
        XCTAssertFalse(flags.contains("--daily-tokens"), "an untouched field is left alone")
        XCTAssertTrue(draft.agent)
        XCTAssertNil(draft.agentChange, "and the switch nobody moved sends nothing")
    }

    func testArgvKeepsJSONAfterTheVerbAndTheNameAfterDashDash() {
        let add = CLICommand.keysAdd(name: "--force", limits: ["--rpm", "60"], agent: true)
        XCTAssertEqual(add.arguments,
                       ["keys", "add", "--json", "--rpm", "60", "--agent", "--", "--force"])
        XCTAssertEqual(CLICommand.keysRevoke("k_1").arguments,
                       ["keys", "revoke", "k_1", "--yes", "--json"])
        XCTAssertEqual(CLICommand.keysAgent("k_1", on: false).arguments,
                       ["keys", "limits", "k_1", "--json", "--agent=false"])
    }
}

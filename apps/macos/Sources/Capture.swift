import AppKit
import SwiftUI

/// The offscreen screenshot harness: `Infercat.app/Contents/MacOS/Infercat --capture DIR`
/// renders every state this build has, in both appearances and both languages, from
/// fixtures only. It never talks to a host, a data directory or the network.
@MainActor
enum Capture {

    struct Scene {
        enum Sheet: Sendable { case mint, limits, once, duplicate, edit }
        enum Surface: Sendable {
            case popover, firstRun, window, friends, sheet(Sheet)
            /// The main window opened on a named screen.
            case screen(Route)
        }
        let name: String
        let surface: Surface
        let build: (HostModel, Language) -> Void
    }

    static let scenes: [Scene] = [
        // The six menu-bar states (design spec §3), as the popover says them.
        Scene(name: "popover-1-stopped", surface: .popover) { model, language in
            model.inject(status: nil, service: try? Demo.service("stopped"), keys: [], usage: nil, lastStatusAt: nil)
            _ = language
        },
        Scene(name: "popover-2-starting", surface: .popover) { model, _ in
            model.inject(status: nil, service: try? Demo.service("running"), keys: [], usage: nil,
                         lastStatusAt: nil, starting: true)
        },
        Scene(name: "popover-3-engine-offline", surface: .popover) { model, language in
            model.inject(status: Demo.status(language, engineHealthy: false), service: try? Demo.service("running"),
                         keys: Demo.keys(language), usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "popover-4-needs-attention", surface: .popover) { model, language in
            model.inject(status: Demo.status(language), service: try? Demo.service("running"),
                         keys: Demo.keys(language), usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "popover-5-friend-connected", surface: .popover) { model, language in
            model.inject(status: Demo.status(language), service: try? Demo.service("running"),
                         keys: Demo.keys(language, generous: true), usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "popover-6-running", surface: .popover) { model, language in
            model.inject(status: Demo.status(language, connected: false), service: try? Demo.service("running"),
                         keys: Demo.keys(language, generous: true), usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "popover-7-not-answering", surface: .popover) { model, language in
            model.inject(status: Demo.status(language), service: try? Demo.service("running"),
                         keys: Demo.keys(language, generous: true), usage: try? Demo.usage(),
                         lastStatusAt: Demo.clock.addingTimeInterval(-12), at: Demo.clock)
        },

        // First run.
        Scene(name: "firstrun-1-welcome", surface: .firstRun) { model, _ in
            model.inject(status: nil, service: try? Demo.service("absent"), keys: [], usage: nil, lastStatusAt: nil)
        },
        Scene(name: "firstrun-2-starting", surface: .firstRun) { model, _ in
            model.inject(status: nil, service: try? Demo.service("absent"), keys: [], usage: nil,
                         lastStatusAt: nil, starting: true)
        },
        Scene(name: "firstrun-3-no-engine", surface: .firstRun) { model, language in
            model.inject(status: Demo.status(language, engineHealthy: false, engineFound: false),
                         service: try? Demo.service("running"), keys: [], usage: nil, lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "firstrun-4-name-not-saved", surface: .firstRun) { model, language in
            model.inject(status: Demo.status(language), service: try? Demo.service("running"),
                         keys: Demo.keys(language, generous: true), usage: try? Demo.usage(),
                         lastStatusAt: Demo.clock, nameFailed: "settings set --json · exit 75",
                         at: Demo.clock.addingTimeInterval(2))
        },

        // Overview, in its seven states.
        Scene(name: "overview-1-running", surface: .window) { model, language in
            model.inject(status: Demo.status(language), service: try? Demo.service("running"),
                         keys: Demo.keys(language), usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "overview-2-engine-offline", surface: .window) { model, language in
            model.inject(status: Demo.status(language, engineHealthy: false), service: try? Demo.service("running"),
                         keys: Demo.keys(language, generous: true), usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "overview-3-stopped", surface: .window) { model, _ in
            model.inject(status: nil, service: try? Demo.service("stopped"), keys: [], usage: nil, lastStatusAt: nil)
        },
        Scene(name: "overview-4-empty", surface: .window) { model, language in
            model.inject(status: Demo.status(language, friends: false), service: try? Demo.service("running"),
                         keys: [], usage: try? Demo.usage(), lastStatusAt: Demo.clock, at: Demo.clock.addingTimeInterval(2))
        },
        Scene(name: "overview-5-loading", surface: .window) { model, _ in
            model.inject(status: nil, service: try? Demo.service("running"), keys: [], usage: nil, lastStatusAt: nil)
        },
        Scene(name: "overview-6-error", surface: .window) { model, _ in
            model.inject(status: nil, service: try? Demo.service("running"), keys: [], usage: nil,
                         lastStatusAt: nil, failure: .unsupportedSchema(2))
        },
        Scene(name: "overview-7-not-answering", surface: .window) { model, language in
            model.inject(status: Demo.status(language), service: try? Demo.service("running"),
                         keys: Demo.keys(language, generous: true), usage: try? Demo.usage(),
                         lastStatusAt: Demo.clock.addingTimeInterval(-12), at: Demo.clock)
        },

        // Friends (cut B).
        Scene(name: "friends-1-list", surface: .friends) { model, language in
            Demo.friends(model, language)
        },
        Scene(name: "friends-2-inspector", surface: .friends) { model, language in
            Demo.friends(model, language, select: "k_lin")
        },
        Scene(name: "friends-3-paused", surface: .friends) { model, language in
            Demo.friends(model, language, select: "k_pia")
        },
        Scene(name: "friends-4-revoked", surface: .friends) { model, language in
            Demo.friends(model, language, select: "k_old", showRevoked: true)
        },
        Scene(name: "friends-5-empty", surface: .friends) { model, language in
            Demo.friends(model, language, keys: [])
        },
        Scene(name: "friends-6-loading", surface: .friends) { model, language in
            Demo.friends(model, language, keys: [], loading: true)
        },
        Scene(name: "friends-7-not-answering", surface: .friends) { model, language in
            Demo.friends(model, language, select: "k_lin", stale: true)
        },
        Scene(name: "friends-8-action-unanswered", surface: .friends) { model, language in
            Demo.friends(model, language, select: "k_wei", notice: true)
        },

        // The invite sheet, its detour, and the once-card.
        Scene(name: "invite-1-name", surface: .sheet(.mint)) { model, language in
            Demo.friends(model, language)
        },
        Scene(name: "invite-2-limits", surface: .sheet(.limits)) { model, language in
            Demo.friends(model, language)
        },
        Scene(name: "invite-3-once-card", surface: .sheet(.once)) { model, language in
            Demo.friends(model, language)
        },
        Scene(name: "invite-4-duplicate", surface: .sheet(.duplicate)) { model, language in
            Demo.friends(model, language)
        },
        Scene(name: "invite-5-edit-limits", surface: .sheet(.edit)) { model, language in
            Demo.friends(model, language, select: "k_lin")
        },

        // Activity and Settings (cut C).
        Scene(name: "activity-1-live", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
            Demo.activity(model, language)
        },
        Scene(name: "activity-2-errors", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
            Demo.activity(model, language, errorsOnly: true)
        },
        Scene(name: "activity-3-paused", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
            Demo.activity(model, language, paused: true)
        },
        Scene(name: "activity-4-dropped", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
            Demo.activity(model, language, dropped: true)
        },
        Scene(name: "activity-7-paused-overflow", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
            Demo.activity(model, language, paused: true, overflow: true)
        },
        Scene(name: "activity-5-empty", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
        },
        Scene(name: "activity-6-not-answering", surface: .screen(.activity)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true), stale: true)
            Demo.activity(model, language)
        },
        Scene(name: "settings-1-running", surface: .screen(.settings)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true))
        },
        Scene(name: "settings-2-stopped", surface: .screen(.settings)) { model, _ in
            model.inject(status: nil, service: try? Demo.service("stopped"), keys: [],
                         usage: nil, lastStatusAt: nil)
        },
        Scene(name: "settings-3-not-answering", surface: .screen(.settings)) { model, language in
            Demo.friends(model, language, keys: Demo.keys(language, generous: true), stale: true)
        },

        // The founder picks the mono face on a real build (design spec §10.3).
        Scene(name: "font-plex-vs-sf", surface: .popover) { _, _ in }
    ]

    static func run(into directory: URL) -> Bool {
        Brand.registerFont()
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            for language in [Language.en, .zh] {
                for dark in [false, true] {
                    for scene in scenes {
                        let image = try render(scene, language: language, dark: dark)
                        let name = "\(scene.name)-\(language.rawValue)-\(dark ? "dark" : "light").png"
                        guard let png = image.representation(using: .png, properties: [:]) else {
                            throw CLIError.malformed
                        }
                        try png.write(to: directory.appendingPathComponent(name))
                    }
                }
            }
            FileHandle.standardError.write(Data("captured \(scenes.count * 4) screens\n".utf8))
            return true
        } catch {
            FileHandle.standardError.write(Data("capture failed: \(error)\n".utf8))
            return false
        }
    }

    private static func render(_ scene: Scene, language: Language, dark: Bool) throws -> NSBitmapImageRep {
        let model = HostModel(client: FixtureCLI(language: language))
        model.language = language
        model.hostName = language == .zh ? "Max 的工作站" : "Max's workstation"
        scene.build(model, language)
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)

        if scene.name == "font-plex-vs-sf" {
            return try snapshot(FontSample(language: language), width: 430, appearance: appearance)
        }
        switch scene.surface {
        case .popover:
            return try snapshot(PopoverView(model: model, openWindow: {}, openConsole: {}),
                                width: 316, appearance: appearance)
        case .firstRun:
            return try snapshot(FirstRunView(model: model), width: 560, appearance: appearance)
        case .window:
            return try windowSnapshot(MainWindow(model: model, openConsole: {}),
                                      size: CGSize(width: 980, height: 640), appearance: appearance)
        case .friends:
            return try windowSnapshot(MainWindow(model: model, openConsole: {}, startOn: .friends),
                                      size: CGSize(width: 1060, height: 680), appearance: appearance)
        case .screen(let route):
            return try windowSnapshot(MainWindow(model: model, openConsole: {}, startOn: route),
                                      size: CGSize(width: 1120, height: 700), appearance: appearance)
        case .sheet(let kind):
            return try snapshot(SheetPreview(model: model, kind: kind), width: 460, appearance: appearance)
        }
    }

    /// Popover and first run size themselves; capture them at their fitting height.
    private static func snapshot<V: View>(_ view: V, width: CGFloat, appearance: NSAppearance?) throws -> NSBitmapImageRep {
        let hosting = NSHostingView(rootView: AnyView(view.frame(width: width)))
        hosting.appearance = appearance
        hosting.frame = CGRect(x: 0, y: 0, width: width, height: 1200)
        hosting.layoutSubtreeIfNeeded()
        let height = max(80, hosting.fittingSize.height)
        hosting.frame = CGRect(x: 0, y: 0, width: width, height: height)
        hosting.layoutSubtreeIfNeeded()
        return try bitmap(of: hosting, opaque: true)
    }

    /// The main window needs a real window: NavigationSplitView and the toolbar only
    /// lay out inside one. It is placed far offscreen and torn down straight after.
    private static func windowSnapshot<V: View>(_ view: V, size: CGSize, appearance: NSAppearance?) throws -> NSBitmapImageRep {
        // The window has to be composited to be captured, so it is placed on screen
        // for the fraction of a second the capture takes, then torn down.
        let window = NSWindow(contentRect: CGRect(origin: .zero, size: size),
                              styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.center()
        window.appearance = appearance
        window.toolbarStyle = .unified
        window.contentViewController = NSHostingController(rootView: AnyView(view))
        window.setContentSize(size)
        window.makeKeyAndOrderFront(nil)
        RunLoop.current.run(until: Date().addingTimeInterval(0.6))
        defer { window.orderOut(nil); window.contentViewController = nil }
        // `cacheDisplay` is used rather than a screen capture because a screen capture
        // needs a permission this unsigned build cannot have, in CI least of all.
        // It draws everything except vibrancy, which is why the window's own surfaces
        // are opaque (see MainWindow) instead of materials.
        guard let frame = window.contentView?.superview ?? window.contentView else { throw CLIError.malformed }
        frame.layoutSubtreeIfNeeded()
        RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        return try bitmap(of: frame, opaque: true)
    }

    private static func bitmap(of view: NSView, opaque: Bool) throws -> NSBitmapImageRep {
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { throw CLIError.malformed }
        if opaque {
            let background = NSGraphicsContext(bitmapImageRep: rep)
            NSGraphicsContext.saveGraphicsState()
            NSGraphicsContext.current = background
            (view.effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                ? NSColor(calibratedWhite: 0.12, alpha: 1)
                : NSColor.white).setFill()
            view.bounds.fill()
            NSGraphicsContext.restoreGraphicsState()
        }
        view.cacheDisplay(in: view.bounds, to: rep)
        return rep
    }
}

/// The invite sheet at each of its steps, without a window to present it from.
private struct SheetPreview: View {
    @ObservedObject var model: HostModel
    let kind: Capture.Scene.Sheet

    var body: some View {
        InviteSheet(model: model, request: request, preview: preview) {}
    }

    private var request: InviteRequest {
        switch kind {
        case .edit: InviteRequest(kind: .limits(keyID: "k_lin", name: model.friendKeys.first?.name ?? "Lin"))
        case .duplicate: InviteRequest(kind: .mint(prefillName: model.friendKeys.first?.name ?? "Lin"))
        default: InviteRequest(kind: .mint(prefillName: ""))
        }
    }

    private var preview: InviteSheet.Preview? {
        switch kind {
        case .mint: nil
        case .limits: .limits
        case .once: .once
        case .duplicate: .duplicate
        case .edit: nil
        }
    }
}

/// IBM Plex Mono beside SF Mono at 11 pt, in both appearances, for the founder's pick.
private struct FontSample: View {
    let language: Language
    private let line = "84.2k · 1.56M · 4,096 · k_9f21c0 · qwen3.8-flash-next"

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("IBM Plex Mono · 11 pt").font(.caption).foregroundStyle(.secondary)
            Text(line).font(Brand.mono(11))
            Divider()
            Text("SF Mono · 11 pt").font(.caption).foregroundStyle(.secondary)
            Text(line).font(.system(size: 11, design: .monospaced))
            Text(Copy.text("f_today_v", language: language, ["c": "214", "t": "331k"]))
                .font(Brand.mono(11))
        }
        .padding(22)
        .frame(width: 430, alignment: .leading)
    }
}

/// Demo payloads, decoded from the bundled fixtures and adjusted per scene.
@MainActor
enum Demo {
    /// 2026-09-28T14:35Z — the moment the demo fixtures describe, so "last seen"
    /// reads as the past and the freshness line reads as live.
    static let clock = Date(timeIntervalSince1970: 1_790_606_100)

    static func service(_ which: String) throws -> ServiceStatus {
        struct Wrapper: Decodable { var data: ServiceStatus }
        return try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("service.\(which)")).data
    }
    static func usage() throws -> UsageReport {
        struct Wrapper: Decodable { var data: UsageReport }
        return try Contract.decoder.decode(Wrapper.self, from: try Fixtures.demo("usage")).data
    }

    static func status(_ language: Language, engineHealthy: Bool = true, engineFound: Bool = true,
                       connected: Bool = true, friends: Bool = true) -> HostStatus? {
        guard var object = try? JSONSerialization.jsonObject(with: try Fixtures.demo("status.\(language.code)")) as? [String: Any],
              var data = object["data"] as? [String: Any] else { return nil }
        if !engineHealthy {
            var upstream = data["upstream"] as? [String: Any] ?? [:]
            upstream["healthy"] = false
            if !engineFound { upstream["url"] = ""; upstream["kind"] = "" }
            data["upstream"] = upstream
            var queue = data["queue"] as? [String: Any] ?? [:]
            queue["in_flight"] = 0
            queue["waiting"] = 2
            data["queue"] = queue
        }
        if !friends { data["keys"] = nil }
        else if !connected, var keys = data["keys"] as? [[String: Any]] {
            keys = keys.map { var row = $0; row["connected"] = false; row["in_flight"] = 0; return row }
            data["keys"] = keys
        }
        object["data"] = data
        guard let bytes = try? JSONSerialization.data(withJSONObject: object) else { return nil }
        struct Wrapper: Decodable { var data: HostStatus }
        return try? Contract.decoder.decode(Wrapper.self, from: bytes).data
    }

    /// `generous` raises the daily limit so no friend trips the 90% attention rule.
    /// A Friends screen in one of its states, all of it from the bundled fixtures.
    static func friends(_ model: HostModel, _ language: Language, select: String? = nil,
                        keys: [FriendKey]? = nil, showRevoked: Bool = false,
                        loading: Bool = false, stale: Bool = false, notice: Bool = false) {
        let rows = keys ?? self.keys(language, generous: true)
        model.inject(status: status(language), service: try? service("running"),
                     keys: rows, usage: try? usage(),
                     lastStatusAt: stale ? clock.addingTimeInterval(-12) : clock,
                     at: stale ? clock : clock.addingTimeInterval(2))
        model.previewFriends(select: select, showRevoked: showRevoked, loading: loading,
                             detail: select == nil ? nil : detail(language),
                             notice: notice ? model.text("act_unanswered") : nil)
    }

    /// A plausible tail of settled requests, built from the demo friends. Everything
    /// here is counts and timings; there is nowhere for text to live.
    static func activity(_ model: HostModel, _ language: Language,
                         errorsOnly: Bool = false, paused: Bool = false,
                         dropped: Bool = false, overflow: Bool = false) {
        let keys = self.keys(language, generous: true)
        let rows: [(Int, String, Int, String?, Int, Int, Int, Int)] = [
            (0, "/v1/chat/completions", 200, nil, 1_204, 386, 212, 9_600),
            (1, "/v1/chat/completions", 200, nil, 842, 1_190, 198, 27_400),
            (2, "/v1/audio/transcriptions", 200, nil, 0, 0, 0, 3_100),
            (3, "/v1/chat/completions", 200, nil, 6_120, 2_048, 340, 51_000),
            (4, "/v1/chat/completions", 429, "rate_limited", 0, 0, 0, 4),
            (5, "/v1/embeddings", 200, nil, 512, 0, 0, 400),
            (6, "/v1/chat/completions", 200, nil, 2_310, 744, 240, 18_200),
            (7, "/v1/chat/completions", 200, nil, 988, 1_422, 205, 33_900),
            (8, "/v1/chat/completions", 499, "client_closed", 760, 92, 221, 2_800),
            (9, "/v1/chat/completions", 200, nil, 4_402, 1_876, 310, 46_100),
        ]
        for (index, endpoint, status, code, into, out, ttft, total) in rows.reversed() {
            let owner = keys[index % 3]
            model.activity.append(UsageEvent(
                ts: clock.addingTimeInterval(-Double(index) * 97),
                key_id: owner.id, endpoint: endpoint, status: status,
                prompt_tokens: into, completion_tokens: out, queued_ms: 120,
                ttft_ms: ttft, total_ms: total, kind: nil,
                model: "qwen3.8-flash-next", via: index == 3 ? "bridge" : "tunnel", code: code))
            if dropped, index == 4 { model.activity.drop(6) }
        }
        model.activity.previewErrorsOnly = errorsOnly
        model.activity.flushForTesting()
        if paused {
            model.activity.setPaused(true)
            // A dozen for the everyday case; more than the ring holds for the one the
            // counter used to get wrong.
            for index in 0..<(overflow ? 1_200 : 12) {
                model.activity.append(UsageEvent(
                    ts: clock, key_id: keys[index % 3].id, endpoint: "/v1/chat/completions",
                    status: 200, prompt_tokens: 120, completion_tokens: 340, queued_ms: 0,
                    ttft_ms: 190, total_ms: 8_100, kind: nil, model: nil, via: nil, code: nil))
            }
        }
    }

    static func minted(_ language: Language) -> MintedInvite? {
        struct Wrapper: Decodable { var data: MintedInvite }
        guard let bytes = try? Fixtures.demo("minted.\(language.code)") else { return nil }
        return try? Contract.decoder.decode(Wrapper.self, from: bytes).data
    }

    static func detail(_ language: Language) -> FriendKey? {
        struct Wrapper: Decodable { var data: FriendKey }
        guard let bytes = try? Fixtures.demo("keyshow.\(language.code)") else { return nil }
        return try? Contract.decoder.decode(Wrapper.self, from: bytes).data
    }

    static func keys(_ language: Language, generous: Bool = false) -> [FriendKey] {
        guard var object = try? JSONSerialization.jsonObject(with: try Fixtures.demo("keys.\(language.code)")) as? [String: Any],
              var rows = object["data"] as? [[String: Any]] else { return [] }
        if generous {
            rows = rows.map { row in
                var row = row
                var limits = row["limits"] as? [String: Any] ?? [:]
                limits["daily_tokens"] = 1_000_000
                row["limits"] = limits
                return row
            }
        }
        object["data"] = rows
        guard let bytes = try? JSONSerialization.data(withJSONObject: object) else { return [] }
        struct Wrapper: Decodable { var data: [FriendKey] }
        return (try? Contract.decoder.decode(Wrapper.self, from: bytes).data) ?? []
    }
}

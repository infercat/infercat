import SwiftUI

/// The six screens of the sidebar. Overview is cut A; the rest are cut B/C and say so.
enum Route: String, CaseIterable, Identifiable, Sendable {
    case overview, friends, activity, engine, usage, settings
    var id: String { rawValue }

    var copyKey: String { "nav_\(rawValue)" }
    var symbol: String {
        switch self {
        case .overview: "square.grid.2x2"
        case .friends: "person.2"
        case .activity: "waveform.path.ecg"
        case .engine: "gearshape.2"
        case .usage: "chart.bar"
        case .settings: "slider.horizontal.3"
        }
    }
    /// ⌘1–⌘6 (design spec §5, Keyboard).
    var shortcut: KeyEquivalent {
        switch self {
        case .overview: "1"
        case .friends: "2"
        case .activity: "3"
        case .engine: "4"
        case .usage: "5"
        case .settings: "6"
        }
    }
    /// Everything but Engine and Usage, which are v1.
    var inThisBuild: Bool { self != .engine && self != .usage }
}

/// The main window: sidebar, toolbar, and the screen. First run replaces the whole
/// window — no sidebar and no toolbar until there is a host to show.
struct MainWindow: View {
    @ObservedObject var model: HostModel
    var openConsole: () -> Void
    /// The capture harness opens the window on a named screen; the app always starts
    /// on Overview.
    var startOn: Route = .overview
    @State private var route: Route = .overview
    @State private var confirmStop = false
    @State private var invite: InviteRequest?

    var body: some View {
        Group {
            if model.firstRun || model.missingBinary || model.nameFailed != nil {
                FirstRunView(model: model)
            } else {
                split
            }
        }
        .tint(Brand.cobalt)
        .onAppear { route = startOn }
        .sheet(item: $invite) { request in
            InviteSheet(model: model, request: request) { invite = nil }
        }
        .background(
            Button("") { invite = InviteRequest() }
                .keyboardShortcut("n", modifiers: .command)
                .disabled(!model.hostRunning)
                .opacity(0).frame(width: 0, height: 0).accessibilityHidden(true)
        )
        .confirmationDialog(stopTitle, isPresented: $confirmStop) {
            Button(model.text("stop_ok"), role: .destructive) { Task { await model.lifecycle("stop") } }
            Button(model.text("cancel"), role: .cancel) {}.keyboardShortcut(.defaultAction)
        } message: {
            Text(model.text("stop_body", ["names": model.connected.map(\.name).joined(separator: ", ")]))
        }
    }

    private var split: some View {
        NavigationSplitView {
            sidebar
                .navigationSplitViewColumnWidth(min: 180, ideal: 180, max: 220)
        } detail: {
            screen
                .frame(minWidth: 620, minHeight: 460)
                // Opaque, not a material: the content under the toolbar is text we
                // want at full contrast, and a blurred headline reads as disabled.
                .background(Color(nsColor: .textBackgroundColor))
                .toolbarBackground(.visible, for: .windowToolbar)
                .toolbar { toolbar }
                .navigationTitle(model.text(route.copyKey))
                .navigationSubtitle(subtitle)
        }
    }

    // MARK: - Sidebar

    private var sidebar: some View {
        VStack(spacing: 0) {
            List(selection: $route) {
                ForEach([Route.overview, .friends, .activity]) { entry in
                    row(entry)
                }
                Section(model.text("nav_machine")) {
                    ForEach([Route.engine, .usage, .settings]) { entry in
                        row(entry)
                    }
                }
            }
            // `.inset` rather than `.sidebar`: the sidebar style draws its selection
            // as a vibrancy layer, which the capture harness cannot render and which
            // Increase Contrast flattens. This is the same system list, drawn opaquely.
            .listStyle(.inset)
            .scrollContentBackground(.hidden)
            Divider()
            hostFoot
        }
        .background(shortcuts)
    }

    private func row(_ entry: Route) -> some View {
        Label(model.text(entry.copyKey), systemImage: entry.symbol)
            .badge(entry == .friends ? model.connectedCount : 0)
            .listRowSeparator(.hidden)
            .tag(entry)
    }

    /// ⌘1–⌘6 move between screens (design spec §5). They are buttons with no size,
    /// because a List row cannot carry a shortcut.
    private var shortcuts: some View {
        ForEach(Route.allCases) { entry in
            Button("") { route = entry }
                .keyboardShortcut(entry.shortcut, modifiers: .command)
                .opacity(0)
                .frame(width: 0, height: 0)
        }
        .accessibilityHidden(true)
    }

    /// The host's state pinned to the sidebar's foot, visible from every screen.
    private var hostFoot: some View {
        HStack(spacing: 7) {
            StatusSquare(kind: model.hostRunning ? (model.stale ? .down : .connected) : .idle,
                         label: model.hostRunning ? model.text("sb_running") : model.text("sq_stopped"))
            VStack(alignment: .leading, spacing: 1) {
                Text(model.hostRunning ? model.text("sb_running") : model.text("pop_stopped"))
                    .font(.system(.callout, weight: .semibold))
                if let uptime = model.status?.uptime_s, model.hostRunning {
                    // One line, always: the foot is the narrowest place in the app and
                    // a wrapped uptime pushes the whole block taller on every screen.
                    Text(model.text("sb_up", ["t": Copy.duration(seconds: uptime, language: model.language)]))
                        .font(Brand.mono(10)).foregroundStyle(.secondary)
                        .lineLimit(1)
                        .fixedSize(horizontal: true, vertical: false)
                }
            }
            Spacer(minLength: 4)
            if model.hostRunning {
                Button(model.text("sb_stop")) {
                    if model.connectedCount > 0 || (model.status?.queue.in_flight ?? 0) > 0 { confirmStop = true }
                    else { Task { await model.lifecycle("stop") } }
                }
                .controlSize(.small)
                .disabled(model.working)
            } else {
                Button(model.text("act_start")) { Task { await model.lifecycle("start") } }
                    .controlSize(.small)
                    .disabled(model.working)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .accessibilityElement(children: .contain)
    }

    private var stopTitle: String {
        model.connectedCount == 1
            ? model.text("stop_title_1")
            : model.text("stop_title_n", ["n": String(model.connectedCount)])
    }

    // MARK: - Toolbar

    /// The model, and nothing else. The title area is narrow, and the uptime this
    /// line used to carry was always the part that got truncated — the sidebar foot
    /// already shows it, on every screen.
    private var subtitle: String {
        if route == .friends { return model.friendsSubtitle }
        if route == .activity { return model.activitySubtitle }
        guard model.hostRunning, let served = model.status?.model else { return "" }
        return route == .overview ? served : ""
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .status) {
            // Nothing to say about freshness when the host is stopped: say nothing,
            // rather than leave an empty control in the toolbar.
            if !freshness.isEmpty {
                Text(freshness)
                    .font(Brand.mono(10))
                    .foregroundStyle(model.stale ? Brand.down : .secondary)
                    .padding(.horizontal, 6)
                    .fixedSize()
                    .accessibilityLabel(freshness)
            }
        }
        ToolbarItem(placement: .primaryAction) {
            Button(model.text("act_invite")) { invite = InviteRequest() }
                .disabled(!model.hostRunning)
        }
    }

    /// "live · 2 s ago" while answers arrive; the last good read's time once they stop.
    private var freshness: String {
        if model.status == nil && model.hostRunning { return model.text("tb_reading") }
        guard let at = model.lastStatusAt else { return "" }
        if model.stale {
            return model.text("tb_last", ["time": at.formatted(date: .omitted, time: .standard)])
        }
        return model.text("tb_live", ["n": String(model.age)])
    }

    // MARK: - Screens

    @ViewBuilder
    private var screen: some View {
        switch route {
        case .overview:
            OverviewScreen(model: model, openConsole: openConsole, invite: $invite)
        case .friends:
            FriendsScreen(model: model, openConsole: openConsole, invite: $invite)
        case .activity:
            ActivityScreen(model: model, log: model.activity, openConsole: openConsole) { keyID in
                model.selectedFriend = keyID
                route = .friends
            }
        case .settings:
            SettingsScreen(model: model, openConsole: openConsole)
        default:
            ComingSoonScreen(model: model, route: route, openConsole: openConsole)
        }
    }
}

/// Friends, Activity, Engine, Usage and Settings are cut B/C. The entry stays in the
/// sidebar so the shape of the app is honest, and the screen says what it is and
/// offers the surface that already does the job.
struct ComingSoonScreen: View {
    @ObservedObject var model: HostModel
    let route: Route
    var openConsole: () -> Void

    var body: some View {
        VStack(spacing: 14) {
            Loaf().frame(width: 44, height: 44).foregroundStyle(.secondary)
            Text(model.text("soon_title", ["screen": model.text(route.copyKey)]))
                .font(.system(.title3, weight: .semibold))
            Text(model.text("soon_body", ["screen": model.text(route.copyKey)]))
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
            Button(model.text("act_console"), action: openConsole)
                .buttonStyle(.borderedProminent)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .contain)
    }
}

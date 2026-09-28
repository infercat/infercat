import SwiftUI

/// The menu-bar popover (design spec §3 and the §7 copy table). 316 pt wide.
/// It opens on one sentence of state, then attention, then who is connected, then
/// the two buttons, then a block that behaves like menu items.
struct PopoverView: View {
    @ObservedObject var model: HostModel
    var openWindow: () -> Void
    var openConsole: () -> Void
    @State private var confirmStop = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            if !model.attention.isEmpty { block(model.text("pop_attention")) { attentionRows } }
            if !model.connected.isEmpty, model.hostRunning { block(model.text("pop_connected")) { friendRows } }
            actions
            Divider()
            menuBlock
        }
        .frame(width: 316)
        .tint(Brand.cobalt)
        .confirmationDialog(stopTitle, isPresented: $confirmStop) {
            Button(model.text("stop_ok"), role: .destructive) { Task { await model.lifecycle("stop") } }
            Button(model.text("cancel"), role: .cancel) {}.keyboardShortcut(.defaultAction)
        } message: {
            Text(model.text("stop_body", ["names": model.connected.map(\.name).joined(separator: ", ")]))
        }
    }

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 7) {
                StatusSquare(kind: square, label: squareWord)
                Text(model.headline)
                    .font(.system(.body, weight: .semibold))
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text(meta)
                .font(Brand.mono())
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            if let sentence = stateSentence {
                Text(sentence)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 2)
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 14)
        .padding(.bottom, 12)
    }

    private var square: StatusSquare.Kind {
        switch model.presence {
        case .stopped: .idle
        case .starting: .idle
        case .engineOffline: .down
        case .needsAttention: .caution
        case .friendConnected: .connected
        case .running: .connected
        }
    }
    private var squareWord: String {
        switch model.presence {
        case .stopped: model.text("sq_stopped")
        case .starting: model.text("pop_starting")
        case .engineOffline: model.text("sq_down")
        case .needsAttention: model.text("sq_attention")
        default: model.text("sq_connected")
        }
    }

    /// The mono line under the headline: who we are, what we serve, how fast.
    private var meta: String {
        guard let status = model.status, model.hostRunning else {
            if let service = model.service, service.installed, !service.since.isEmpty {
                return model.text("pop_stopped_at", ["time": service.since])
            }
            return model.hostName
        }
        var parts = [status.name.isEmpty ? model.hostName : status.name]
        if let served = status.model { parts.append(served) }
        if status.engine.metrics {
            parts.append("\(Int(status.engine.tokens_per_s_1m.rounded())) tok/s")
        }
        if model.stale, let at = model.lastStatusAt {
            parts.append(model.text("as_of", ["time": at.formatted(date: .omitted, time: .standard)]))
        }
        return parts.joined(separator: " · ")
    }

    private var stateSentence: String? {
        if !model.hostRunning { return model.text("pop_stopped_sub") }
        if model.stale { return nil }
        if let upstream = model.status?.upstream, !upstream.healthy {
            let kind = upstream.kind.isEmpty ? "engine" : upstream.kind
            let addr = upstream.url.isEmpty ? "—" : upstream.url
            return model.text("pop_offline_sub", ["kind": kind, "addr": addr])
        }
        return nil
    }

    // MARK: - Blocks

    @ViewBuilder
    private func block<Content: View>(_ label: String, @ViewBuilder content: () -> Content) -> some View {
        Divider()
        VStack(alignment: .leading, spacing: 7) {
            BlockLabel(text: label)
            content()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }

    private var attentionRows: some View {
        ForEach(model.attention.prefix(3)) { item in
            HStack(alignment: .top, spacing: 7) {
                StatusSquare(kind: .caution).padding(.top, 5)
                Text(model.shortAttention(item))
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 6)
                Button(model.text("pop_open"), action: openWindow).buttonStyle(.link)
            }
            .accessibilityElement(children: .combine)
        }
    }

    private var friendRows: some View {
        VStack(alignment: .leading, spacing: 7) {
            ForEach(model.connected.prefix(4)) { friend in
                Button(action: openWindow) {
                    HStack(spacing: 7) {
                        StatusSquare(kind: friend.in_flight > 0 ? .inFlight : .connected)
                        Text(friend.name).font(.system(.callout, weight: .semibold)).lineLimit(1).truncationMode(.middle)
                        Text("· " + rowState(friend)).font(.callout).foregroundStyle(.secondary).lineLimit(1)
                        Spacer(minLength: 6)
                        Text(model.text("row_today", ["x": Copy.compact(friend.today_tokens)]))
                            .font(Brand.mono()).foregroundStyle(.secondary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityElement(children: .combine)
                .accessibilityLabel(model.text("a11y_row", ["name": friend.name, "state": rowState(friend), "x": Copy.exact(friend.today_tokens)]))
            }
            if model.connected.count > 4 {
                Text(model.text("pop_more", ["n": String(model.connected.count - 4)]))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func rowState(_ friend: HostStatus.Friend) -> String {
        if model.status?.upstream.healthy == false { return model.text("row_waiting") }
        if friend.in_flight > 0 { return model.text("row_inflight", ["n": String(friend.in_flight)]) }
        return model.text("row_idle")
    }

    // MARK: - Actions

    @ViewBuilder
    private var actions: some View {
        Divider()
        VStack(alignment: .leading, spacing: 8) {
            if !model.hostRunning {
                Button { Task { await model.lifecycle("start") } } label: {
                    Text(model.text("act_start")).frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .disabled(model.working)
            } else if model.status?.upstream.healthy == false, !model.stale {
                HStack(spacing: 8) {
                    Button(model.text("act_engine"), action: openWindow).buttonStyle(.borderedProminent)
                    Button(model.text("act_restart")) { Task { await model.lifecycle("restart") } }
                        .disabled(model.working)
                }
            } else {
                HStack(spacing: 8) {
                    Button(model.text("act_invite")) {}
                        .disabled(true)
                        .help(model.text("soon_invite"))
                    Button(model.text("act_open"), action: openWindow).buttonStyle(.borderedProminent)
                }
            }
            if model.working {
                Text(model.text("waiting")).font(.caption).foregroundStyle(.secondary)
            }
        }
        .controlSize(.regular)
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }

    private var stopTitle: String {
        model.connectedCount == 1
            ? model.text("stop_title_1")
            : model.text("stop_title_n", ["n": String(model.connectedCount)])
    }

    @ViewBuilder
    private var menuBlock: some View {
        VStack(alignment: .leading, spacing: 0) {
            if model.hostRunning {
                if model.status?.upstream.healthy != false || model.stale {
                    MenuRow(title: model.text("act_restart"), enabled: !model.working) {
                        Task { await model.lifecycle("restart") }
                    }
                }
                MenuRow(title: model.text("act_stop"), enabled: !model.working) {
                    if model.connectedCount > 0 || (model.status?.queue.in_flight ?? 0) > 0 {
                        confirmStop = true
                    } else {
                        Task { await model.lifecycle("stop") }
                    }
                }
            } else {
                MenuRow(title: model.text("act_open"), enabled: true, action: openWindow)
            }
            MenuRow(title: model.text("act_console"), enabled: true, action: openConsole)
            MenuRow(title: model.text("act_quit"), enabled: true, trailing: model.text("quit_note")) {
                NSApplication.shared.terminate(nil)
            }
        }
        .padding(.vertical, 6)
    }
}

/// A row in the popover's lower block. It reads and highlights like a menu item,
/// because that is the muscle memory a menu-bar app owns (mockups, "Why a popover").
private struct MenuRow: View {
    let title: String
    var enabled = true
    var trailing: String?
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Text(title).font(.callout)
                Spacer(minLength: 8)
                if let trailing {
                    Text(trailing).font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 5)
            .contentShape(Rectangle())
            .background(hovering && enabled ? Color.primary.opacity(0.07) : .clear)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .opacity(enabled ? 1 : 0.4)
        .onHover { hovering = $0 }
    }
}

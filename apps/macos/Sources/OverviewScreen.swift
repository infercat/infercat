import SwiftUI

/// Overview answers "is it sharing, and with whom" in one sentence, then four facts,
/// then what needs attention, then who is connected, then the ways in (design spec §2).
/// Seven states, all from listed data: running, engine offline, stopped, empty,
/// loading, error, not answering.
struct OverviewScreen: View {
    @ObservedObject var model: HostModel
    var openConsole: () -> Void
    var onStop: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if model.stale { staleBanner }
                switch model.screen {
                case .loading: LoadingBody()
                case .stopped: stoppedBody
                case .failed: failedBody
                case .empty: emptyBody
                case .running, .engineOffline, .notAnswering: liveBody
                }
            }
            // The top inset clears the toolbar's glass band, so the one sentence of
            // state is never read through it and never looks disabled.
            .padding(.top, 60)
            .padding(.horizontal, 24)
            .padding(.bottom, 24)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .hardScrollEdge()
        .opacity(model.stale ? 0.55 : 1)
    }

    // MARK: - Banners

    private var staleBanner: some View {
        HStack(spacing: 8) {
            StatusSquare(kind: .down, label: model.text("sq_down"))
            Text(model.text("banner_stale", ["n": String(model.age)]))
                .font(.callout)
            Spacer(minLength: 8)
            Button(model.text("act_restart")) { Task { await model.lifecycle("restart") } }
                .controlSize(.small)
                .disabled(model.working)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(Brand.down.opacity(0.12))
        .opacity(1)
        .accessibilityElement(children: .combine)
    }

    // MARK: - Bodies

    private var stoppedBody: some View {
        VStack(alignment: .leading, spacing: 16) {
            headline(square: .idle, word: model.text("sq_stopped"),
                     title: model.text("pop_stopped"),
                     detail: stoppedDetail)
            Text(model.text("pop_stopped_sub"))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(model.text("act_start")) { Task { await model.lifecycle("start") } }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(model.working)
        }
    }

    private var stoppedDetail: String? {
        guard let service = model.service, service.installed, !service.since.isEmpty else { return nil }
        return model.text("pop_stopped_at", ["time": service.since])
    }

    private var failedBody: some View {
        VStack(alignment: .leading, spacing: 14) {
            headline(square: .down, word: model.text("sq_down"), title: model.text("err_title"), detail: nil)
            Text(model.describe(model.failure ?? .malformed))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Text(model.failureDetail)
                .font(Brand.mono(10))
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
                .padding(9)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.primary.opacity(0.06))
            HStack(spacing: 10) {
                Button(model.text("err_copy")) {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(model.failureDetail, forType: .string)
                }
                Button(model.text("err_retry")) { model.refreshNow() }
                    .buttonStyle(.borderedProminent)
            }
        }
    }

    private var emptyBody: some View {
        VStack(alignment: .leading, spacing: 20) {
            headline(square: .connected, word: model.text("sq_connected"),
                     title: runningTitle, detail: model.text("ov_nobody"))
            facts
            VStack(spacing: 12) {
                Loaf().frame(width: 40, height: 40).foregroundStyle(.secondary)
                Text(model.text("fr_first")).font(.system(.title3, weight: .semibold))
                Text(model.text("fr_first_body"))
                    .font(.callout).foregroundStyle(.secondary)
                    .multilineTextAlignment(.center).frame(maxWidth: 340)
                Button(model.text("act_invite")) {}
                    .buttonStyle(.borderedProminent)
                    .disabled(true)
                    .help(model.text("soon_invite"))
                Button(model.text("act_console"), action: openConsole).buttonStyle(.link)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 28)
            ways
        }
    }

    private var liveBody: some View {
        VStack(alignment: .leading, spacing: 20) {
            if model.status?.upstream.healthy == false, !model.stale {
                headline(square: .down, word: model.text("sq_down"),
                         title: model.headline, detail: model.text("ov_degraded"))
            } else if model.stale {
                headline(square: .idle, word: model.text("sq_down"),
                         title: model.text("ov_was", ["what": wasWhat]),
                         detail: model.lastStatusAt.map { model.text("as_of", ["time": $0.formatted(date: .omitted, time: .standard)]) })
            } else {
                headline(square: .connected, word: model.text("sq_connected"),
                         title: runningTitle, detail: runningDetail)
            }
            facts
            if !model.attention.isEmpty { attentionBlock }
            if !model.friends.isEmpty { connectedBlock }
            ways
        }
    }

    // MARK: - Pieces

    private func headline(square kind: StatusSquare.Kind, word: String, title: String, detail: String?) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 9) {
                StatusSquare(kind: kind, size: 10, label: word)
                Text(title)
                    .font(.system(.title2, weight: .semibold))
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let detail {
                Text(detail).font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var runningTitle: String {
        guard let served = model.status?.model else { return model.text("ov_running_bare") }
        return model.text("ov_running", ["model": served])
    }

    private var runningDetail: String {
        var parts = [wasWhat]
        if let flight = model.status?.queue.in_flight, flight > 0 {
            parts.append(flight == 1 ? model.text("ov_inflight_1") : model.text("ov_inflight_n", ["n": String(flight)]))
        }
        if let uptime = model.status?.uptime_s {
            parts.append(model.text("sb_up", ["t": Copy.duration(seconds: uptime)]))
        }
        return parts.joined(separator: " · ")
    }

    private var wasWhat: String {
        switch model.connectedCount {
        case 0: model.text("ov_friends_0")
        case 1: model.text("ov_friends_1")
        default: model.text("ov_friends_n", ["n": String(model.connectedCount)])
        }
    }

    /// The console's own four facts, in the console's order.
    private var facts: some View {
        let status = model.status
        let upstream = status?.upstream
        let engine = status?.engine
        return LazyVGrid(columns: Array(repeating: GridItem(.flexible(minimum: 120), spacing: 18), count: 4),
                         alignment: .leading, spacing: 18) {
            FactCell(key: model.text("f_engine"),
                     value: upstream?.healthy == true ? model.text("f_healthy") : model.text("f_offline"),
                     detail: upstream.map {
                         model.text("f_slots", ["n": String($0.slots), "c": Copy.compact($0.model_context)])
                     })
            FactCell(key: model.text("f_tunnel"),
                     value: model.text("f_connected_n", ["n": String(status?.tunnel.clients ?? 0)]),
                     detail: (status?.tunnel.region).flatMap { $0.isEmpty ? nil : model.text("f_relay", ["region": $0]) })
            FactCell(key: model.text("f_now"),
                     value: model.text("f_now_v", ["a": String(status?.queue.in_flight ?? 0),
                                                   "b": String(status?.queue.waiting ?? 0)]),
                     detail: engine?.metrics == true
                        ? model.text("f_tokens_s", ["n": String(Int((engine?.tokens_per_s_1m ?? 0).rounded()))])
                        : model.text("f_unreported"))
            FactCell(key: model.text("f_today"),
                     value: todayValue,
                     detail: todayDetail)
        }
    }

    private var todayValue: String {
        guard let usage = model.usage else { return model.text("f_unreported") }
        return model.text("f_today_v", ["c": Copy.compact(usage.total.model_calls),
                                        "t": Copy.compact(usage.tokens)])
    }
    private var todayDetail: String? {
        guard let usage = model.usage else { return nil }
        return usage.total.errors == 0
            ? model.text("f_errors_0")
            : model.text("f_errors_n", ["n": String(usage.total.errors)])
    }

    private var attentionBlock: some View {
        VStack(alignment: .leading, spacing: 10) {
            BlockLabel(text: model.text("pop_attention"))
            ForEach(model.attention) { item in
                HStack(alignment: .top, spacing: 8) {
                    StatusSquare(kind: .caution, label: model.text("sq_attention")).padding(.top, 5)
                    Text(item.text).font(.callout).fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 10)
                    Button(remedyTitle(item.remedy), action: openConsole)
                        .controlSize(.small)
                        .help(model.text("soon_invite"))
                }
                .accessibilityElement(children: .combine)
            }
        }
    }

    private func remedyTitle(_ remedy: HostModel.Attention.Remedy) -> String {
        switch remedy {
        case .changeLimit: model.text("act_change_limit")
        case .openSettings: model.text("act_open_settings")
        case .openEngine: model.text("act_engine")
        }
    }

    private var connectedBlock: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                BlockLabel(text: model.text("pop_connected"))
                Spacer()
                Text(model.text("ov_all", ["n": String(model.friends.count)]))
                    .font(.caption).foregroundStyle(.secondary)
            }
            if model.connected.isEmpty {
                Text(model.text("pop_running_0")).font(.callout).foregroundStyle(.secondary)
            }
            ForEach(model.connected) { friend in
                HStack(spacing: 9) {
                    StatusSquare(kind: friend.in_flight > 0 ? .inFlight : .connected,
                                 label: friend.in_flight > 0 ? model.text("sq_inflight") : model.text("sq_connected"))
                    Text(friend.name).font(.system(.callout, weight: .semibold)).frame(width: 110, alignment: .leading)
                    Text(rowState(friend)).font(.callout).foregroundStyle(.secondary)
                    Spacer(minLength: 10)
                    if let way = model.status?.way(for: friend) {
                        Text(model.text(way == .tunnel ? "via_tunnel" : "via_public"))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Text(model.text("row_today", ["x": Copy.compact(friend.today_tokens)]))
                        .font(Brand.mono()).frame(width: 110, alignment: .trailing)
                }
                .accessibilityElement(children: .combine)
                .accessibilityLabel("\(friend.name), \(rowState(friend)), \(Copy.exact(friend.today_tokens)) tokens today")
            }
        }
    }

    private func rowState(_ friend: HostStatus.Friend) -> String {
        if model.status?.upstream.healthy == false { return model.text("row_waiting") }
        if friend.in_flight > 0 {
            let limit = model.friendKeys.first(where: { $0.id == friend.id })?.limits.rpm ?? 0
            let flight = model.text("row_inflight", ["n": String(friend.in_flight)])
            guard limit > 0 else { return flight }
            return flight + " · " + model.text("row_minute", ["used": String(friend.rpm_used), "limit": String(limit)])
        }
        return model.text("row_idle")
    }

    /// One quiet line: exposure is a fact a host should never have to hunt for.
    private var ways: some View {
        HStack(spacing: 18) {
            Text(model.text("ways_title")).font(.caption).fontWeight(.semibold)
            wayLabel(model.text("ways_tunnel"), on: (model.status?.tunnel.clients ?? 0) >= 0 && model.hostRunning, note: nil)
            wayLabel(model.text("ways_public"), on: model.status?.bridge?.enabled == true,
                     note: model.status?.bridge.map { model.text("ways_requests", ["n": String($0.requests_today)]) })
            wayLabel(model.text("ways_remote"), on: model.status?.remote?.enabled == true, note: nil)
            Spacer(minLength: 8)
            Button(model.text("ways_change"), action: openConsole).buttonStyle(.link).font(.caption)
        }
        .padding(.top, 4)
        .overlay(alignment: .top) { Rectangle().fill(.primary.opacity(0.18)).frame(height: 1) }
    }

    private func wayLabel(_ name: String, on: Bool, note: String?) -> some View {
        HStack(spacing: 4) {
            Text(name).font(.caption).foregroundStyle(.secondary)
            Text(model.text(on ? "ways_on" : "ways_off")).font(.caption).fontWeight(.semibold)
            if on, let note {
                Text("· " + note).font(Brand.mono(10)).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// The first read, after 300 ms. Skeletons do not pulse under Reduce Motion (§5).
private struct LoadingBody: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var dim = false

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            skeleton(width: 320, height: 22)
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 18), count: 4),
                      alignment: .leading, spacing: 18) {
                ForEach(0..<4, id: \.self) { _ in
                    VStack(alignment: .leading, spacing: 6) {
                        Rectangle().fill(.primary).frame(height: 1)
                        skeleton(width: 84, height: 12)
                    }
                }
            }
            skeleton(width: nil, height: 14)
            skeleton(width: 420, height: 14)
        }
        .onAppear { if !reduceMotion { withAnimation(.easeInOut(duration: 1).repeatForever()) { dim = true } } }
        .accessibilityLabel(Text("Reading the host"))
    }

    private func skeleton(width: CGFloat?, height: CGFloat) -> some View {
        Rectangle()
            .fill(Color.primary.opacity(dim ? 0.07 : 0.14))
            .frame(maxWidth: width ?? .infinity, alignment: .leading)
            .frame(height: height)
    }
}

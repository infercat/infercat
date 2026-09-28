import SwiftUI

/// Which sheet to open, and why. `rotate` reuses the once-card with a new secret.
struct InviteRequest: Identifiable, Equatable {
    enum Kind: Equatable {
        case mint(prefillName: String)
        case rotate(keyID: String, name: String)
        case limits(keyID: String, name: String)
    }
    var id = UUID()
    var kind: Kind = .mint(prefillName: "")

    var prefillName: String {
        switch kind {
        case .mint(let name): name
        case .rotate(_, let name), .limits(_, let name): name
        }
    }
}

/// One friend in full (design spec §2). Pause is the only visible verb, because it
/// is the only one a host can take back; rotate and revoke sit behind •••.
struct FriendInspector: View {
    @ObservedObject var model: HostModel
    let row: HostModel.FriendRow
    @Binding var invite: InviteRequest?
    /// Return in the list moves focus here, to the one verb a host can take back.
    var primaryFocus: FocusState<Bool>.Binding
    @State private var confirmRevoke = false
    @State private var confirmRotate = false

    private var detail: FriendKey? {
        model.friendDetail?.id == row.id ? model.friendDetail : row.key
    }
    private var busy: Bool { model.actionInFlight?.keyID == row.id }
    private var actionsEnabled: Bool { !busy && !model.stale && model.hostRunning }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                top
                now
                sevenDays
                limits
                inviteBlock
            }
            .padding(.top, 60)
            .padding(.horizontal, 22)
            .padding(.bottom, 24)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .opacity(model.stale ? 0.55 : 1)
        .background(shortcuts)
        .alert(model.text("rev_title", ["name": row.name]), isPresented: $confirmRevoke) {
            Button(model.text("rev_ok", ["name": row.name]), role: .destructive) {
                Task { await model.act(.keysRevoke(row.id), on: row.id) }
            }
            Button(model.text("rev_pause")) {
                Task { await model.act(.keysPause(row.id), on: row.id) }
            }
            Button(model.text("cancel"), role: .cancel) {}.keyboardShortcut(.defaultAction)
        } message: {
            Text(model.text("rev_body", ["name": row.name]))
        }
        .alert(model.text("rot_title", ["name": row.name]), isPresented: $confirmRotate) {
            Button(model.text("rot_ok"), role: .destructive) {
                invite = InviteRequest(kind: .rotate(keyID: row.id, name: row.name))
            }
            Button(model.text("cancel"), role: .cancel) {}.keyboardShortcut(.defaultAction)
        } message: {
            Text(model.text("rot_body", ["name": row.name]))
        }
    }

    /// Space toggles Pause, ⌘⌫ opens the Revoke alert (design spec §5).
    private var shortcuts: some View {
        Group {
            Button("") { togglePause() }
                .keyboardShortcut(.space, modifiers: [])
            Button("") { if actionsEnabled { confirmRevoke = true } }
                .keyboardShortcut(.delete, modifiers: .command)
        }
        .opacity(0).frame(width: 0, height: 0).accessibilityHidden(true)
        .disabled(!actionsEnabled)
    }

    private func togglePause() {
        guard actionsEnabled else { return }
        let command: CLICommand = row.paused ? .keysResume(row.id) : .keysPause(row.id)
        Task { await model.act(command, on: row.id) }
    }

    // MARK: - Top

    private var top: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(row.name)
                    .font(.system(.title2, weight: .semibold))
                    .lineLimit(1).truncationMode(.middle)
                Text(identity).font(Brand.mono(10)).foregroundStyle(.secondary).textSelection(.enabled)
            }
            Spacer(minLength: 8)
            HStack(spacing: 8) {
                Button(busy ? model.text("waiting") : model.text(row.paused ? "act_resume" : "act_pause")) {
                    togglePause()
                }
                .disabled(!actionsEnabled)
                .focused(primaryFocus)
                Menu {
                    Button(model.text("act_edit_limits")) {
                        invite = InviteRequest(kind: .limits(keyID: row.id, name: row.name))
                    }
                    Button(model.text("act_rotate")) { confirmRotate = true }
                    Divider()
                    Button(model.text("act_revoke"), role: .destructive) { confirmRevoke = true }
                }
                label: { Image(systemName: "ellipsis") }
                    .menuStyle(.borderlessButton)
                    .menuIndicator(.hidden)
                    .frame(width: 24)
                    .disabled(!actionsEnabled)
                    .accessibilityLabel(model.text("a11y_more"))
            }
        }
    }

    private var identity: String {
        var parts = [row.id, model.text(statusKey)]
        if let created = row.key.created_at {
            parts.append(model.text("fr_created", ["date": model.shortDate(created)]))
        }
        return parts.joined(separator: " · ")
    }
    private var statusKey: String {
        row.paused ? "sq_paused" : row.revoked ? "sq_revoked" : "fr_active"
    }

    // MARK: - Now

    private var now: some View {
        VStack(alignment: .leading, spacing: 10) {
            BlockLabel(text: model.text("insp_now"))
            HStack(spacing: 8) {
                StatusSquare(kind: row.inFlight > 0 ? .inFlight : (row.connected ? .connected : .idle),
                             label: row.connected ? model.text("sq_connected") : model.text("row_idle"))
                Text(model.rowStatusLine(row)).font(.callout)
            }
            meter("insp_rpm", used: row.live?.rpm_used ?? 0, limit: row.key.limits.rpm)
            meter("insp_tpm", used: row.live?.tpm_used ?? 0, limit: row.key.limits.tpm)
            meter("insp_today", used: row.todayTokens, limit: row.dailyLimit)
        }
    }

    /// One meter is one VoiceOver element: "Tokens today, 84,200 of 200,000, 42 percent".
    private func meter(_ key: String, used: Int, limit: Int) -> some View {
        let fraction = limit > 0 ? min(1, Double(used) / Double(limit)) : 0
        let hot = fraction >= 0.9
        return VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(model.text(key)).font(.caption).foregroundStyle(.secondary)
                Spacer()
                Text(limit > 0 ? "\(Copy.compact(used)) / \(Copy.compact(limit))" : Copy.compact(used))
                    .font(Brand.mono(10))
                    .foregroundStyle(hot ? Brand.down : .secondary)
            }
            Meter(fraction: fraction, hot: hot)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(limit > 0
            ? model.text("a11y_named_meter", ["what": model.text(key), "used": Copy.exact(used),
                                              "limit": Copy.exact(limit),
                                              "p": String(Int((fraction * 100).rounded()))])
            : "\(model.text(key)), \(Copy.exact(used))")
    }

    // MARK: - Seven days

    @ViewBuilder
    private var sevenDays: some View {
        let days = detail?.daily ?? []
        if !days.isEmpty {
            VStack(alignment: .leading, spacing: 10) {
                BlockLabel(text: model.text("insp_7d"))
                let peak = max(1, days.map(\.tokens).max() ?? 1)
                HStack(alignment: .bottom, spacing: 10) {
                    ForEach(days) { day in
                        VStack(spacing: 5) {
                            Text(Copy.compact(day.tokens)).font(Brand.mono(9)).foregroundStyle(.secondary)
                            ZStack(alignment: .bottom) {
                                Rectangle().fill(Color.primary.opacity(0.08)).frame(height: 48)
                                Rectangle()
                                    .fill(day.date == days.last?.date ? Brand.cobalt : Color.primary.opacity(0.55))
                                    .frame(height: max(1, 48 * CGFloat(day.tokens) / CGFloat(peak)))
                            }
                            .frame(height: 48)
                            Text(weekday(day.date, isLast: day.date == days.last?.date))
                                .font(.caption2).foregroundStyle(.secondary)
                        }
                        .frame(maxWidth: .infinity)
                        .accessibilityElement(children: .ignore)
                        .accessibilityLabel("\(weekdayLong(day.date)), \(Copy.exact(day.tokens)) tokens")
                    }
                }
            }
        }
    }

    private func dayDate(_ text: String) -> Date? {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        formatter.timeZone = TimeZone(identifier: "UTC")
        return formatter.date(from: text)
    }
    /// `daily[]` is always the last seven UTC days ending today, so the last bar is
    /// today by construction — no clock of ours has to agree with the host's.
    private func weekday(_ text: String, isLast: Bool = false) -> String {
        if isLast { return model.text("f_today") }
        guard let date = dayDate(text) else { return text }
        return date.formatted(.dateTime.weekday(.abbreviated).locale(locale))
    }
    private func weekdayLong(_ text: String) -> String {
        guard let date = dayDate(text) else { return text }
        return date.formatted(.dateTime.weekday(.wide).locale(locale))
    }
    private var locale: Locale { Locale(identifier: model.language.code == "zh" ? "zh-Hans" : "en_US") }

    // MARK: - Limits and invite

    private var limits: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                BlockLabel(text: model.text("inv_limits"))
                Spacer()
                Button(model.text("act_edit")) {
                    invite = InviteRequest(kind: .limits(keyID: row.id, name: row.name))
                }
                .buttonStyle(.link).font(.caption)
                .disabled(!actionsEnabled)
            }
            Text(limitsSummary)
                .font(Brand.mono(10))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if detail?.agent == true {
                Text(model.text("insp_agent_on")).font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    /// An existing key has no "host's default": the host already applied one and told
    /// us the number. A value at or below zero is the key being unlimited.
    private var limitsSummary: String {
        let limits = row.key.limits
        func line(_ key: String, _ value: Int) -> String {
            model.text(key, ["n": value > 0 ? Copy.compact(value) : model.text("inv_none")])
        }
        return [line("sum_rpm", limits.rpm), line("sum_daily", limits.daily_tokens),
                line("sum_conc", limits.max_concurrent), line("sum_out", limits.max_output_tokens)]
            .joined(separator: " · ")
    }

    private var inviteBlock: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                BlockLabel(text: model.text("insp_invite"))
                Spacer()
                Button(model.text("act_rotate")) { confirmRotate = true }
                    .buttonStyle(.link).font(.caption)
                    .disabled(!actionsEnabled)
            }
            // Only the hash is stored, so this is all the app can ever show again.
            Text(model.text("insp_masked"))
                .font(Brand.mono(10))
                .foregroundStyle(.secondary)
            if row.neverConnected {
                Text(model.text("after_waiting", ["name": row.name]))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

/// A revoked friend: read-only, and the one thing left to do is invite them again.
struct RevokedInspector: View {
    @ObservedObject var model: HostModel
    let key: FriendKey
    @Binding var invite: InviteRequest?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(key.name).font(.system(.title2, weight: .semibold))
            Text([key.id, model.text("sq_revoked")].joined(separator: " · "))
                .font(Brand.mono(10)).foregroundStyle(.secondary)
            Text(model.text("fr_revoked_body", ["name": key.name]))
                .font(.callout).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(model.text("act_invite")) {
                invite = InviteRequest(kind: .mint(prefillName: key.name))
            }
            .buttonStyle(.borderedProminent)
            .disabled(!model.hostRunning)
            Spacer()
        }
        .padding(.top, 60)
        .padding(.horizontal, 22)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

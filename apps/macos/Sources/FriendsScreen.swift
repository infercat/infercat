import SwiftUI

/// Friends: who may use this machine, who is on it, and how much of today's budget
/// each has used (design spec §2). The console's six-column table becomes a
/// three-column list plus an inspector, so it reads at any window width.
struct FriendsScreen: View {
    @ObservedObject var model: HostModel
    var openConsole: () -> Void
    @Binding var invite: InviteRequest?
    @FocusState private var filterFocused: Bool
    @State private var filter = ""

    var body: some View {
        Group {
            if model.friendKeys.isEmpty && model.status != nil && !model.loadingKeys {
                empty
            } else if model.friendKeys.isEmpty {
                FriendsSkeleton()
            } else {
                split
            }
        }
        .onAppear { model.readKeys(force: true) }
        .onChange(of: model.selectedFriend) { _, id in model.openFriend(id) }
        .background(
            Button("") { filterFocused = true }
                .keyboardShortcut("f", modifiers: .command)
                .opacity(0).frame(width: 0, height: 0)
                .accessibilityHidden(true)
        )
    }

    // MARK: - The split

    private var split: some View {
        HSplitView {
            list.frame(minWidth: 280, idealWidth: 360, maxWidth: 520)
            Group {
                if let selected = model.selectedFriend, let row = rows.first(where: { $0.id == selected }) {
                    FriendInspector(model: model, row: row, invite: $invite)
                } else if let revoked = model.friendKeys.first(where: { $0.id == model.selectedFriend }) {
                    RevokedInspector(model: model, key: revoked, invite: $invite)
                } else {
                    noSelection
                }
            }
            .frame(minWidth: 320, maxWidth: .infinity)
        }
    }

    private var rows: [HostModel.FriendRow] {
        let all = model.friendRows
        let needle = filter.trimmingCharacters(in: .whitespaces).lowercased()
        guard !needle.isEmpty else { return all }
        return all.filter { $0.name.lowercased().contains(needle) }
    }

    // MARK: - The list

    private var list: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if model.actionNotice != nil { actionBanner }
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(rows) { row in
                        FriendRowView(model: model, row: row,
                                      selected: model.selectedFriend == row.id)
                            .contentShape(Rectangle())
                            .onTapGesture { model.selectedFriend = row.id }
                    }
                    if !model.revokedFriends.isEmpty { revokedFold }
                }
                .padding(.bottom, 8)
                .opacity(model.stale ? 0.55 : 1)
            }
        }
        .background(Color(nsColor: .textBackgroundColor))
    }

    private var header: some View {
        HStack(spacing: 8) {
            TextField(model.text("fr_filter"), text: $filter)
                .textFieldStyle(.roundedBorder)
                .focused($filterFocused)
                .accessibilityLabel(model.text("fr_filter"))
            Spacer(minLength: 0)
            Text(model.text("fr_tokens_today").uppercased())
                .font(.caption2).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
    }

    /// "No answer from the host. Pausing Wei may have completed. Check the row before
    /// trying again." — shown once, dismissible, and never a resend (design §4).
    private var actionBanner: some View {
        HStack(spacing: 8) {
            StatusSquare(kind: .down, label: model.text("sq_down"))
            Text(model.actionNotice ?? "").font(.callout).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            Button(model.text("act_dismiss")) { model.dismissActionNotice() }.controlSize(.small)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(Brand.down.opacity(0.12))
        .accessibilityElement(children: .combine)
    }

    private var revokedFold: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                model.showRevoked.toggle()
            } label: {
                Text(model.text(model.showRevoked ? "fr_revoked_hide" : "fr_revoked_show",
                                ["n": String(model.revokedFriends.count)]))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
            }
            .buttonStyle(.plain)
            if model.showRevoked {
                ForEach(model.revokedFriends) { key in
                    HStack(spacing: 9) {
                        StatusSquare(kind: .idle, label: model.text("sq_revoked"))
                        Text(key.name).font(.callout).foregroundStyle(.secondary)
                            .lineLimit(1).truncationMode(.middle)
                        Spacer(minLength: 6)
                        Text(model.text("fr_revoked")).font(.caption).foregroundStyle(.secondary)
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 7)
                    .contentShape(Rectangle())
                    .onTapGesture { model.selectedFriend = key.id }
                    .accessibilityElement(children: .combine)
                }
            }
        }
    }

    // MARK: - Empty and no-selection

    private var empty: some View {
        VStack(spacing: 12) {
            Loaf().frame(width: 40, height: 40).foregroundStyle(.secondary)
            Text(model.text("fr_none")).font(.system(.title3, weight: .semibold))
            Text(model.text("fr_none_body"))
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center).frame(maxWidth: 340)
            Button(model.text("act_invite")) { invite = InviteRequest() }
                .buttonStyle(.borderedProminent)
                .disabled(!model.hostRunning)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var noSelection: some View {
        Text(model.text("fr_pick"))
            .font(.callout)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// One friend in the list: square, name, one status line, today against the limit.
struct FriendRowView: View {
    @ObservedObject var model: HostModel
    let row: HostModel.FriendRow
    let selected: Bool

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            StatusSquare(kind: square, label: squareWord)
            VStack(alignment: .leading, spacing: 2) {
                Text(row.name)
                    .font(.system(.callout, weight: .semibold))
                    .lineLimit(1).truncationMode(.middle)
                Text(waiting ?? model.rowStatusLine(row))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 10)
            VStack(alignment: .trailing, spacing: 4) {
                Text(budget)
                    .font(Brand.mono(10))
                    .foregroundStyle(row.isHot ? Brand.down : .secondary)
                Meter(fraction: row.used ?? 0, hot: row.isHot).frame(width: 104)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        .background(selected ? Color.accentColor.opacity(0.16) : .clear)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(voiceOver)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }

    private var waiting: String? {
        model.actionInFlight?.keyID == row.id ? model.text("waiting") : nil
    }

    private var square: StatusSquare.Kind {
        if row.paused { return .caution }
        if model.stale { return .idle }
        if row.inFlight > 0 { return .inFlight }
        if row.connected { return .connected }
        return .idle
    }
    private var squareWord: String {
        if row.paused { return model.text("sq_paused") }
        if row.inFlight > 0 { return model.text("sq_inflight") }
        if row.connected { return model.text("sq_connected") }
        return model.text("row_idle")
    }

    /// "84.2k / 200k", and the percentage as well once it is worth worrying about —
    /// colour is never the only signal (design spec §5).
    private var budget: String {
        guard row.dailyLimit > 0 else { return Copy.compact(row.todayTokens) }
        let base = "\(Copy.compact(row.todayTokens)) / \(Copy.compact(row.dailyLimit))"
        guard row.isHot, let used = row.used else { return base }
        return base + " · \(Int((used * 100).rounded()))%"
    }

    private var voiceOver: String {
        var parts = [row.name, model.rowStatusLine(row)]
        if row.dailyLimit > 0, let used = row.used {
            parts.append(model.text("a11y_meter", [
                "used": Copy.exact(row.todayTokens),
                "limit": Copy.exact(row.dailyLimit),
                "p": String(Int((used * 100).rounded())),
            ]))
        } else {
            parts.append(model.text("row_today", ["x": Copy.exact(row.todayTokens)]))
        }
        return parts.joined(separator: ", ")
    }
}

/// A 3 pt meter, square ends, no radius — the console's own (design spec §6).
struct Meter: View {
    let fraction: Double
    var hot = false

    var body: some View {
        GeometryReader { geometry in
            ZStack(alignment: .leading) {
                Rectangle().fill(Color.primary.opacity(0.14))
                Rectangle()
                    .fill(hot ? Brand.down : Brand.cobalt)
                    .frame(width: max(0, min(1, fraction)) * geometry.size.width)
            }
        }
        .frame(height: 3)
        .accessibilityHidden(true)
    }
}

/// The first read of the list.
private struct FriendsSkeleton: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            ForEach(0..<6, id: \.self) { index in
                Rectangle()
                    .fill(Color.primary.opacity(0.10))
                    .frame(maxWidth: index.isMultiple(of: 2) ? .infinity : 300)
                    .frame(height: 14)
            }
            Spacer()
        }
        .padding(24)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityLabel(Text("Reading the host"))
    }
}

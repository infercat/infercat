import Foundation

/// Friends, invites and the mutations behind them — the half of `HostModel` that
/// cut B added. It lives in its own file so the cut A state machine stays readable.
///
/// Every rule the design spec names about acting is here, not in a view:
/// no optimistic updates, an unanswered action re-reads rather than resends, and a
/// mutation is followed by a read so the row changes only once the host agrees.
extension HostModel {

    /// One row of the Friends list: the key as the host lists it, plus whatever the
    /// live status says about it right now.
    struct FriendRow: Identifiable, Sendable {
        let key: FriendKey
        let live: HostStatus.Friend?
        var id: String { key.id }
        var name: String { key.name }
        var connected: Bool { live?.connected ?? false }
        var inFlight: Int { live?.in_flight ?? 0 }
        var todayTokens: Int { live?.today_tokens ?? key.today_tokens }
        var dailyLimit: Int { key.limits.daily_tokens }
        var paused: Bool { key.isPaused }
        var revoked: Bool { key.isRevoked }
        var neverConnected: Bool { key.seenAt == nil }
        var lastSeen: Date? { key.seenAt }
        /// Fraction of today's tokens used, or nil when there is no daily limit.
        var used: Double? {
            guard dailyLimit > 0 else { return nil }
            return min(1, Double(todayTokens) / Double(dailyLimit))
        }
        var isHot: Bool { (used ?? 0) >= 0.9 }
    }

    /// Sort: in flight, then connected, then active by last seen, then paused
    /// (design spec §2, "Friends"). Revoked keys are not in this list at all.
    var friendRows: [FriendRow] {
        let live = Dictionary(uniqueKeysWithValues: friends.map { ($0.id, $0) })
        return friendKeys
            .filter { !$0.isRevoked }
            .map { FriendRow(key: $0, live: live[$0.id]) }
            .sorted { left, right in
                func rank(_ row: FriendRow) -> Int {
                    if row.paused { return 3 }
                    if row.inFlight > 0 { return 0 }
                    if row.connected { return 1 }
                    return 2
                }
                let (a, b) = (rank(left), rank(right))
                if a != b { return a < b }
                let seenA = left.lastSeen ?? .distantPast
                let seenB = right.lastSeen ?? .distantPast
                if seenA != seenB { return seenA > seenB }
                return left.name.localizedStandardCompare(right.name) == .orderedAscending
            }
    }

    var revokedFriends: [FriendKey] {
        friendKeys.filter(\.isRevoked).sorted { ($0.seenAt ?? .distantPast) > ($1.seenAt ?? .distantPast) }
    }

    /// "7 friends · 6 active · 1 paused" under the Friends title.
    var friendsSubtitle: String {
        let rows = friendRows
        guard !rows.isEmpty else { return "" }
        var parts = [rows.count == 1 ? text("fr_count_1") : text("fr_count_n", ["n": String(rows.count)])]
        let paused = rows.filter(\.paused).count
        if paused > 0 { parts.append(text("fr_paused_n", ["n": String(paused)])) }
        return parts.joined(separator: " · ")
    }

    /// "live · 214 requests today" under the Activity title. The count is the host's
    /// own `model_calls`, which is what the screen lists.
    var activitySubtitle: String {
        guard let usage else { return "" }
        return usage.total.model_calls == 1
            ? text("act_count_1")
            : text("act_count_n", ["n": Copy.exact(usage.total.model_calls)])
    }

    /// The row's one status line (design spec §2).
    func rowStatusLine(_ row: FriendRow) -> String {
        if row.paused {
            guard let seen = row.lastSeen else { return text("row_paused") }
            return text("row_paused") + " · " + text("row_last_seen", ["ago": relative(seen)])
        }
        if row.inFlight > 0 {
            let flight = text("row_inflight", ["n": String(row.inFlight)])
            guard let way = status?.way(for: row.live!) else { return flight }
            return flight + " · " + text(way == .tunnel ? "via_tunnel" : "via_public")
        }
        if row.connected { return text("row_connected") + " · " + text("row_idle") }
        if row.neverConnected {
            guard let created = row.key.created_at else { return text("row_never") }
            return text("row_never") + " · " + text("row_invited", ["date": shortDate(created)])
        }
        return text("row_last_seen", ["ago": relative(row.lastSeen ?? now)])
    }

    /// Relative under 24 h, a date after (design spec §4, "Numbers").
    func relative(_ date: Date) -> String {
        if now.timeIntervalSince(date) < 86_400 {
            let formatter = RelativeDateTimeFormatter()
            formatter.unitsStyle = .full
            formatter.locale = Locale(identifier: language.code == "zh" ? "zh-Hans" : "en_US")
            return formatter.localizedString(for: date, relativeTo: now)
        }
        return shortDate(date)
    }

    func shortDate(_ date: Date) -> String {
        date.formatted(.dateTime.day().month(.abbreviated).locale(
            Locale(identifier: language.code == "zh" ? "zh-Hans" : "en_US")))
    }
}

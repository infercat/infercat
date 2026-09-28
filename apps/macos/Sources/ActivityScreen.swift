import SwiftUI

/// Activity: what just happened, per request (design spec §2).
///
/// Counts and timings only — never what was said. The rows arrive on the watch stream
/// the app already runs, so opening this screen starts nothing and costs nothing.
struct ActivityScreen: View {
    @ObservedObject var model: HostModel
    @ObservedObject var log: ActivityLog
    var openConsole: () -> Void
    /// Jump to Friends with this friend selected.
    var showFriend: (String) -> Void

    @State private var friend: String?
    @State private var errorsOnly = false
    @State private var selected: String?
    @FocusState private var filterFocused: Bool

    private var items: [ActivityItem] { log.rows(friend: friend, errorsOnly: errorsOnly) }

    var body: some View {
        VStack(spacing: 0) {
            controls
            Divider()
            if model.stale { staleBanner }
            if items.isEmpty {
                empty
            } else {
                table
            }
        }
        .background(Color(nsColor: .textBackgroundColor))
        .onAppear { errorsOnly = log.previewErrorsOnly }
        .background(
            Button("") { filterFocused = true }
                .keyboardShortcut("f", modifiers: .command)
                .opacity(0).frame(width: 0, height: 0).accessibilityHidden(true)
        )
    }

    // MARK: - Controls

    private var controls: some View {
        HStack(spacing: 10) {
            Picker(model.text("act_all_friends"), selection: $friend) {
                Text(model.text("act_all_friends")).tag(String?.none)
                ForEach(model.friendRows) { row in
                    Text(row.name).tag(String?.some(row.id))
                }
            }
            .labelsHidden()
            .frame(width: 170)
            .focused($filterFocused)

            Picker("", selection: $errorsOnly) {
                Text(model.text("act_all")).tag(false)
                Text(model.text("act_errors")).tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 150)

            Button(log.paused
                   ? model.text("act_paused_n", ["n": String(log.heldBack)])
                   : model.text("act_pause")) {
                log.setPaused(!log.paused)
            }
            .controlSize(.regular)

            Spacer(minLength: 8)
            // The first thing a host wonders here is what they are able to see.
            Text(model.text("act_privacy"))
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 16)
        .padding(.top, 60)
        .padding(.bottom, 10)
    }

    private var staleBanner: some View {
        HStack(spacing: 8) {
            StatusSquare(kind: .down, label: model.text("sq_down"))
            Text(model.text("banner_stale", ["n": String(model.age)])).font(.callout)
            Spacer(minLength: 8)
            Button(model.text("act_restart")) { Task { await model.lifecycle("restart") } }
                .controlSize(.small)
                .disabled(model.working)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(Brand.down.opacity(0.12))
        .accessibilityElement(children: .combine)
    }

    // MARK: - The table

    private var table: some View {
        ScrollView {
            LazyVStack(spacing: 0, pinnedViews: .sectionHeaders) {
                Section {
                    ForEach(items) { item in
                        switch item {
                        case .gap(_, let count):
                            gapRow(count)
                        case .request(let row):
                            requestRow(row)
                        }
                    }
                } header: {
                    header
                }
            }
            .padding(.bottom, 10)
        }
        .opacity(model.stale ? 0.55 : 1)
    }

    private var header: some View {
        HStack(spacing: 0) {
            cell(model.text("act_time"), width: 78, align: .leading)
            cell(model.text("act_friend"), width: 110, align: .leading)
            cell(model.text("act_request"), width: 92, align: .leading)
            cell(model.text("act_result"), width: nil, align: .leading)
            cell(model.text("act_tokens"), width: 150, align: .trailing)
            cell(model.text("act_first"), width: 92, align: .trailing)
            cell(model.text("act_total"), width: 76, align: .trailing)
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 16)
        .padding(.vertical, 6)
        .background(Color(nsColor: .textBackgroundColor))
    }

    private func cell(_ text: String, width: CGFloat?, align: Alignment) -> some View {
        Text(text.uppercased())
            .frame(width: width, alignment: align)
            .frame(maxWidth: width == nil ? .infinity : nil, alignment: align)
    }

    @ViewBuilder
    private func requestRow(_ row: ActivityRow) -> some View {
        let isSelected = selected == row.id
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 0) {
                Text(time(row.at))
                    .font(Brand.mono(10)).frame(width: 78, alignment: .leading)
                Text(name(row.keyID))
                    .font(.callout).lineLimit(1).truncationMode(.middle)
                    .frame(width: 110, alignment: .leading)
                Text(model.text("kind_\(row.kind.rawValue)"))
                    .font(.callout).frame(width: 92, alignment: .leading)
                Text(model.text(ResultWord.key(status: row.status, code: row.code)))
                    .font(.callout)
                    .foregroundStyle(row.failed && !row.cancelled ? Brand.down : .primary)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(tokens(row))
                    .font(Brand.mono(10)).frame(width: 150, alignment: .trailing)
                Text(millis(row.ttftMS))
                    .font(Brand.mono(10)).frame(width: 92, alignment: .trailing)
                Text(seconds(row.totalMS))
                    .font(Brand.mono(10)).frame(width: 76, alignment: .trailing)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
            .background(isSelected ? Color.accentColor.opacity(0.16) : .clear)
            .onTapGesture { selected = isSelected ? nil : row.id }
            .accessibilityElement(children: .combine)
            .accessibilityLabel(voiceOver(row))

            if isSelected { detail(row) }
        }
    }

    /// Everything the row could not say, including the only place a code appears.
    private func detail(_ row: ActivityRow) -> some View {
        HStack(spacing: 14) {
            Text(detailLine(row))
                .font(Brand.mono(10))
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            Button(model.text("act_open_friend")) { showFriend(row.keyID) }
                .controlSize(.small)
            Button(model.text("err_copy")) {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(detailLine(row), forType: .string)
            }
            .controlSize(.small)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color.primary.opacity(0.05))
    }

    private func detailLine(_ row: ActivityRow) -> String {
        var parts: [String] = []
        if let served = row.model { parts.append(served) }
        if let via = row.via { parts.append(model.text(via == "bridge" ? "via_public" : "via_tunnel")) }
        parts.append(model.text("act_queued", ["n": String(row.queuedMS)]))
        parts.append("status \(row.status)")
        if let code = row.code, !code.isEmpty { parts.append(code) }
        return parts.joined(separator: " · ")
    }

    /// The stream is lossy by design on the host; when it can count what it lost, so
    /// can the screen.
    private func gapRow(_ count: Int) -> some View {
        HStack(spacing: 8) {
            StatusSquare(kind: .caution, label: model.text("sq_attention"))
            Text(model.text("act_dropped", ["n": String(count)]))
                .font(.caption).foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 6)
        .background(Brand.caution.opacity(0.10))
        .accessibilityElement(children: .combine)
    }

    // MARK: - Empty

    private var empty: some View {
        VStack(spacing: 12) {
            Loaf().frame(width: 40, height: 40).foregroundStyle(.secondary)
            Text(model.text(emptyTitleKey)).font(.system(.title3, weight: .semibold))
            Text(model.text("act_empty_body"))
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center).frame(maxWidth: 340)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var emptyTitleKey: String {
        if errorsOnly || friend != nil { return "act_empty_filtered" }
        return "act_empty"
    }

    // MARK: - Formatting

    private func name(_ keyID: String) -> String {
        model.friendKeys.first { $0.id == keyID }?.name ?? keyID
    }
    private func time(_ date: Date) -> String {
        date.formatted(.dateTime.hour().minute().second()
            .locale(Locale(identifier: model.language.code == "zh" ? "zh-Hans" : "en_US")))
    }
    private func tokens(_ row: ActivityRow) -> String {
        guard row.promptTokens > 0 || row.completionTokens > 0 else { return "—" }
        let out = row.completionTokens > 0 ? Copy.compact(row.completionTokens) : "—"
        return "\(Copy.compact(row.promptTokens)) → \(out)"
    }
    private func millis(_ value: Int) -> String { value > 0 ? "\(Copy.exact(value)) ms" : "—" }
    private func seconds(_ value: Int) -> String {
        guard value > 0 else { return "—" }
        if value < 1000 { return "\(value) ms" }
        return (Double(value) / 1000).formatted(.number.precision(.fractionLength(0...1))) + " s"
    }

    private func voiceOver(_ row: ActivityRow) -> String {
        [time(row.at), name(row.keyID), model.text("kind_\(row.kind.rawValue)"),
         model.text(ResultWord.key(status: row.status, code: row.code)),
         model.text("a11y_tokens", ["in": Copy.exact(row.promptTokens),
                                    "out": Copy.exact(row.completionTokens)])]
            .joined(separator: ", ")
    }
}

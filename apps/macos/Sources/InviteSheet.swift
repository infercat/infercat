import AppKit
import SwiftUI

/// New invite, Edit limits, and Rotate — one sheet, because they are the same form
/// and the same once-card (design spec §2, "New invite"; mockups "Invite").
///
/// Two steps: name the friend, then send them the secret. The secret exists only
/// while step two is on screen, and only `InviteSecret` ever holds it.
struct InviteSheet: View {
    @ObservedObject var model: HostModel
    let request: InviteRequest
    /// Capture-only: opens the sheet already at a given step.
    var preview: Preview?
    var dismiss: () -> Void

    enum Preview: Sendable { case limits, once, duplicate }

    @StateObject private var secret = InviteSecret()
    @State private var name = ""
    @State private var draft = LimitsDraft()
    @State private var showLimits = false
    @State private var showMore = false
    @State private var guardAlert = false
    /// The name the current refusal is about, so typing a different one clears it and
    /// merely setting the field back to the same name does not.
    @State private var refusedName: String?
    @FocusState private var nameFocused: Bool

    var body: some View {
        Group {
            if secret.isOpen {
                OnceCard(model: model, secret: secret, guardAlert: $guardAlert, done: finish)
            } else {
                form
            }
        }
        .frame(width: 460)
        .tint(Brand.cobalt)
        .background(NoRestoration())
        .onAppear(perform: prepare)
        .alert(model.text("guard_title"), isPresented: $guardAlert) {
            Button(model.text("guard_copy")) { secret.copy(.link); finish() }
            Button(model.text("guard_close"), role: .destructive) { finish() }
            Button(model.text("guard_back"), role: .cancel) {}.keyboardShortcut(.defaultAction)
        } message: {
            Text(model.text("guard_body", ["name": secret.name]))
        }
    }

    // MARK: - Setup and exit

    private var isLimitsOnly: Bool {
        if case .limits = request.kind { return true }
        return false
    }
    private var keyID: String? {
        switch request.kind {
        case .mint: nil
        case .rotate(let id, _), .limits(let id, _): id
        }
    }

    private func prepare() {
        if let preview {
            switch preview {
            case .limits:
                showLimits = true
                showMore = true
            case .once:
                if let minted = Demo.minted(model.language) { secret.hold(minted) }
            case .duplicate:
                name = request.prefillName
                refusedName = name
                model.previewRefusal(for: name)
            }
            if preview != .duplicate { name = request.prefillName }
            return
        }
        switch request.kind {
        case .mint(let prefill):
            name = prefill
            nameFocused = true
        case .rotate(let id, let friendName):
            name = friendName
            Task {
                guard let minted = await model.rotate(id) else { return dismiss() }
                secret.hold(minted)
            }
        case .limits(let id, let friendName):
            name = friendName
            showLimits = true
            if let key = model.friendKeys.first(where: { $0.id == id }) {
                draft = LimitsDraft(from: key.limits, agent: key.agent ?? false)
            }
        }
    }

    /// Every exit path forgets the secret. There is no other copy.
    private func finish() {
        secret.forget()
        dismiss()
    }

    // MARK: - Step 1

    private var form: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text(model.text(isLimitsOnly ? "lim_title" : "inv_title"))
                .font(.system(.title3, weight: .semibold))

            if !isLimitsOnly {
                VStack(alignment: .leading, spacing: 6) {
                    Text(model.text("inv_name")).font(.callout).fontWeight(.medium)
                    TextField(model.text("inv_name"), text: $name)
                        .textFieldStyle(.roundedBorder)
                        .focused($nameFocused)
                        .onChange(of: name) { _, typed in
                            if typed != refusedName { model.clearInviteRefusal() }
                        }
                    if let refusal = model.inviteRefusal {
                        VStack(alignment: .leading, spacing: 6) {
                            Text(refusal)
                                .font(.caption).foregroundStyle(Brand.down)
                                .fixedSize(horizontal: false, vertical: true)
                            if let existing = model.existingFriend(named: name) {
                                Button(model.text("act_rotate")) {
                                    model.clearInviteRefusal()
                                    Task {
                                        guard let minted = await model.rotate(existing.id) else { return }
                                        secret.hold(minted)
                                    }
                                }
                                .controlSize(.small)
                            }
                        }
                    } else {
                        Text(model.text("inv_name_hint")).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }

            if showLimits {
                LimitsFields(model: model, draft: $draft, showMore: $showMore)
            } else {
                summaryRow
            }

            HStack {
                if model.minting || model.actionInFlight != nil {
                    Text(model.text("inv_waiting")).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button(model.text("inv_cancel"), role: .cancel) { finish() }
                    .keyboardShortcut(.cancelAction)
                Button(model.text(isLimitsOnly ? "lim_save" : "inv_create"), action: submit)
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(!canSubmit || (isLimitsOnly && draft.nothingChanged))
            }
        }
        .padding(22)
    }

    private var summaryRow: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 3) {
                    BlockLabel(text: model.text("inv_limits"))
                    Text(draft.summary(language: model.language))
                        .font(.callout)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 10)
                Button(model.text("inv_change")) { showLimits = true }.controlSize(.small)
            }
            Text(model.text("inv_defaults")).font(.caption).foregroundStyle(.secondary)
        }
        .padding(.vertical, 2)
    }

    private var canSubmit: Bool {
        guard !model.minting, model.actionInFlight == nil, model.hostRunning else { return false }
        guard draft.isWellFormed(offeredBy: model.status) else { return false }
        return isLimitsOnly || !name.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private func submit() {
        refusedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let flags = draft.flags(offeredBy: model.status)
        if let id = keyID, isLimitsOnly {
            let agentChange = draft.agentChange
            Task {
                // One command for the limits that moved, and a second one only when
                // the agent switch itself was moved.
                if !flags.isEmpty { await model.act(.keysLimits(id, limits: flags), on: id) }
                if let wanted = agentChange { await model.setAgent(wanted, for: id) }
                dismiss()
            }
            return
        }
        Task {
            guard let minted = await model.mint(name: name, limits: flags, agent: draft.agent) else { return }
            secret.hold(minted)
            // Agent access is a capability, not a claim: read it back rather than
            // assume the mint applied it.
            if draft.agent { await model.confirmAgent(for: minted.key_id) }
        }
    }
}

/// The limit fields: four in place, the rest behind "More limits", and only the ones
/// this machine can actually serve.
struct LimitsFields: View {
    @ObservedObject var model: HostModel
    @Binding var draft: LimitsDraft
    @Binding var showMore: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            BlockLabel(text: model.text("inv_limits"))
            ForEach(LimitField.inPlace) { field in row(field) }
            DisclosureGroup(isExpanded: $showMore) {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(LimitField.more.filter { $0.isOffered(by: model.status) }) { field in
                        row(field)
                    }
                    modelsRow
                    if model.status?.agentAvailable == true {
                        Toggle(model.text("l_agent"), isOn: $draft.agent)
                            .toggleStyle(.checkbox)
                            .padding(.top, 2)
                    }
                }
                .padding(.top, 8)
            } label: {
                Text(model.text("inv_more")).font(.callout)
            }
            Text(model.text("inv_untouched")).font(.caption).foregroundStyle(.secondary)
        }
    }

    /// One field, and the menu that says which of the three states it is in. The
    /// menu is the only way to reach "No limit": an empty box means the host decides,
    /// and nothing about this sheet lets those two be confused.
    private func row(_ field: LimitField) -> some View {
        HStack(spacing: 12) {
            Text(model.text(field.copyKey))
                .font(.callout)
                .frame(width: 172, alignment: .leading)
            TextField(placeholder(field), text: Binding(
                get: { draft.text(field) },
                set: { draft.setText($0, for: field) }))
                .textFieldStyle(.roundedBorder)
                .font(Brand.mono(11, relativeTo: .body))
                .frame(width: 110)
                .multilineTextAlignment(.trailing)
                .disabled(draft[field] == .unlimited)
                .help(hint(field))
            Menu {
                Picker("", selection: Binding(
                    get: { state(field) },
                    set: { apply($0, to: field) })) {
                    Text(model.text("lim_state_default")).tag(0)
                    Text(model.text("lim_state_custom")).tag(1)
                    // A field the host would ignore never offers "No limit".
                    if field.unlimitedHonoured {
                        Text(model.text("lim_state_none")).tag(2)
                    }
                }
                .pickerStyle(.inline)
                .labelsHidden()
            } label: {
                Text(stateWord(field))
                    .font(.caption)
                    .frame(minWidth: 86, alignment: .leading)
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
            if let ceiling = field.hostCeiling, draft[field] != .hostDefault {
                Text(model.text("l_ceiling", ["n": String(ceiling)]))
                    .font(.caption2).foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(model.text(field.copyKey)), \(stateWord(field))")
    }

    private func hint(_ field: LimitField) -> String {
        guard field == .maxContext, let ceiling = model.status?.upstream.model_context, ceiling > 0
        else { return model.text(field.copyKey) }
        return model.text("inv_ceiling", ["n": Copy.compact(ceiling)])
    }

    private func state(_ field: LimitField) -> Int {
        switch draft[field] {
        case .hostDefault: 0
        case .custom: 1
        case .unlimited: 2
        }
    }

    private func apply(_ choice: Int, to field: LimitField) {
        switch choice {
        case 1: draft[field] = .custom(draft.text(field))
        case 2: draft[field] = .unlimited
        default: draft[field] = .hostDefault
        }
    }

    private func stateWord(_ field: LimitField) -> String {
        switch draft[field] {
        case .hostDefault: model.text("lim_state_default")
        case .custom: model.text("lim_state_custom")
        case .unlimited: model.text("lim_state_none")
        }
    }

    /// The menu beside the box already says which state the field is in, so the
    /// placeholder only carries what the menu cannot: the engine's own context window.
    private func placeholder(_ field: LimitField) -> String {
        guard field == .maxContext, draft[field] != .unlimited,
              let ceiling = model.status?.upstream.model_context, ceiling > 0 else { return "" }
        // The number alone; the sentence is the field's tooltip, where it has room.
        return Copy.compact(ceiling)
    }

    private var modelsRow: some View {
        HStack(spacing: 12) {
            Text(model.text("l_models")).font(.callout).frame(width: 190, alignment: .leading)
            TextField(model.text("inv_all_models"), text: $draft.models)
                .textFieldStyle(.roundedBorder)
                .font(Brand.mono(11, relativeTo: .body))
                // A mono font taller than the row's default clipped the field's
                // bottom edge; give it the height its own text needs.
                .frame(minHeight: 22)
                .padding(.vertical, 1)
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(model.text("l_models"))
    }
}

/// Step 2. The secret is on screen exactly once, and the app keeps no copy of it
/// after Done (design spec §4, "Invite secret").
struct OnceCard: View {
    @ObservedObject var model: HostModel
    @ObservedObject var secret: InviteSecret
    @Binding var guardAlert: Bool
    var done: () -> Void
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 8) {
                Text(model.text("once_title", ["name": secret.name]))
                    .font(.system(.title3, weight: .semibold))
                Text(model.text("once_tag").uppercased())
                    .font(.caption2)
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(Brand.caution.opacity(0.35))
                    .accessibilityLabel(model.text("once_tag"))
            }
            Text(model.text("once_body")).font(.callout).fixedSize(horizontal: false, vertical: true)

            HStack(alignment: .top, spacing: 16) {
                VStack(alignment: .leading, spacing: 10) {
                    // Middle-truncated on screen; `copy` always takes the whole thing.
                    // Shown middle-truncated and deliberately NOT selectable: a
                    // hand-copy of this text would carry the ellipsis and quietly
                    // fail. Copy link and Copy code are the ways out, and they take
                    // the whole string.
                    Text(InviteSecret.truncatedForScreen(secret.link ?? secret.invite ?? ""))
                        .font(Brand.mono(11, relativeTo: .body))
                        .padding(9)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.primary.opacity(0.06))
                        .textSelection(.disabled)
                        .accessibilityLabel(model.text("a11y_once_link"))
                    Text(model.text("once_use_buttons"))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    HStack(spacing: 8) {
                        Button(copied ? model.text("once_copied") : model.text("once_copy_link")) {
                            secret.copy(.link); flash()
                        }
                        .buttonStyle(.borderedProminent)
                        .keyboardShortcut(.defaultAction)
                        Button(model.text("once_copy_code")) { secret.copy(.code); flash() }
                        Button(model.text("once_share"), action: share)
                    }
                }
                qr
            }

            Text(model.text("once_send", ["name": secret.name]))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            HStack {
                Text(model.text("once_lost")).font(.caption).foregroundStyle(.secondary)
                Spacer()
                // The spec makes Copy link the default action, so Done stays quiet
                // until the person has actually taken the invite somewhere.
                doneButton
            }
        }
        .padding(22)
        .background(NoRestoration())
        // Escape runs the guard too, rather than dropping the secret silently.
        .background(
            Button("") { closeCard() }
                .keyboardShortcut(.cancelAction)
                .opacity(0).frame(width: 0, height: 0).accessibilityHidden(true)
        )
    }

    @ViewBuilder
    private var doneButton: some View {
        if secret.taken {
            Button(model.text("once_done")) { closeCard() }.buttonStyle(.borderedProminent)
        } else {
            Button(model.text("once_done")) { closeCard() }
        }
    }

    /// Always black on white, whatever the appearance: a camera needs the contrast.
    @ViewBuilder
    private var qr: some View {
        if let payload = secret.link ?? secret.invite,
           let image = QRCode.image(for: payload, side: 132) {
            VStack(spacing: 5) {
                Image(nsImage: image)
                    .interpolation(.none)
                    .frame(width: 132, height: 132)
                    .padding(6)
                    .background(Color.white)
                    .accessibilityLabel(model.text("a11y_qr", ["name": secret.name]))
                Text(model.text("once_scan")).font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    private func flash() {
        copied = true
        Task { try? await Task.sleep(for: .seconds(2)); copied = false }
    }

    /// The system share sheet. The app hands over the string and learns nothing back,
    /// so this counts as taken.
    private func share() {
        guard let payload = secret.link ?? secret.invite else { return }
        guard let view = NSApp.keyWindow?.contentView else { return }
        secret.markShared()
        NSSharingServicePicker(items: [payload])
            .show(relativeTo: .zero, of: view, preferredEdge: .minY)
    }

    private func closeCard() {
        if secret.taken { done() } else { guardAlert = true }
    }
}

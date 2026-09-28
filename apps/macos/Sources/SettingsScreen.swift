import SwiftUI

/// Settings, the v0 four (design spec §2, "Settings"): the host's name, whether it
/// starts at login, the app's language, and a way to the web console.
///
/// Every hint starts with a square: ink means it applies at once. Nothing here
/// applies at next start, so no yellow square appears in this build — the settings
/// that need one are v1.
struct SettingsScreen: View {
    @ObservedObject var model: HostModel
    var openConsole: () -> Void

    @State private var name = ""
    @State private var saved = false
    @FocusState private var nameFocused: Bool

    private var editable: Bool { model.hostRunning && !model.stale && !model.working }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 26) {
                if model.stale { staleBanner }
                nameSetting
                loginSetting
                languageSetting
                consoleSetting
            }
            .padding(.top, 60)
            .padding(.horizontal, 24)
            .padding(.bottom, 24)
            .frame(maxWidth: 560, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(Color(nsColor: .textBackgroundColor))
        .onAppear { name = model.status?.name ?? model.hostName }
        .onChange(of: model.status?.name) { _, current in
            // The host is the authority on its own name; only adopt it when the
            // person is not in the middle of typing a new one.
            if !nameFocused, let current, !current.isEmpty { name = current }
        }
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

    // MARK: - Name

    private var nameSetting: some View {
        setting(title: model.text("set_name"), hint: model.text("set_name_hint")) {
            HStack(spacing: 10) {
                TextField(model.text("set_name"), text: $name)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 300)
                    .focused($nameFocused)
                    .disabled(!editable)
                    .onSubmit { save() }
                Button(model.text("set_save"), action: save)
                    .disabled(!editable || !nameChanged)
                if saved {
                    Text(model.text("set_saved")).font(.caption).foregroundStyle(.secondary)
                } else if let failure = model.nameFailed {
                    Text(failure).font(.caption).foregroundStyle(Brand.down)
                }
            }
        }
    }

    private var startsAtLogin: Bool { model.service?.start_at_login ?? false }

    private var nameChanged: Bool {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return !trimmed.isEmpty && trimmed != (model.status?.name ?? "")
    }

    private func save() {
        guard nameChanged else { return }
        saved = false
        Task {
            await model.renameHost(name)
            if model.nameFailed == nil {
                saved = true
                try? await Task.sleep(for: .seconds(2))
                saved = false
            }
        }
    }

    // MARK: - Start at login

    private var loginSetting: some View {
        setting(title: model.text("set_login"), hint: model.text("set_login_hint")) {
            HStack(spacing: 10) {
                Toggle(model.text("set_login"), isOn: Binding(
                    get: { startsAtLogin },
                    set: { wanted in Task { await model.setStartAtLogin(wanted) } }))
                    .toggleStyle(.switch)
                    .labelsHidden()
                    // launchd owns this, not the host, so it works even while stopped.
                    .disabled(model.service?.installed != true || model.working)
                // A switch says its state in colour and position alone. Design spec
                // §5: colour is never the only signal — so it says it in a word too.
                Text(model.text(startsAtLogin ? "set_on" : "set_off"))
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            .accessibilityLabel("\(model.text("set_login")), \(model.text(startsAtLogin ? "set_on" : "set_off"))")
        }
    }

    // MARK: - Language

    private var languageSetting: some View {
        setting(title: model.text("set_language"), hint: model.text("set_language_hint")) {
            Picker(model.text("set_language"), selection: $model.language) {
                Text(model.text("set_lang_system")).tag(Language.system)
                Text("English").tag(Language.en)
                Text("简体中文").tag(Language.zh)
            }
            .labelsHidden()
            .pickerStyle(.radioGroup)
        }
    }

    // MARK: - Console

    private var consoleSetting: some View {
        setting(title: model.text("act_console"), hint: model.text("set_console_hint")) {
            Button(model.text("act_console"), action: openConsole)
                .disabled(!model.hostRunning)
        }
    }

    // MARK: - One setting

    @ViewBuilder
    private func setting<Content: View>(title: String, hint: String,
                                        @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.system(.callout, weight: .semibold))
            content()
            HStack(alignment: .firstTextBaseline, spacing: 7) {
                // Ink: applies at once. Everything in this build does.
                StatusSquare(kind: .connected, label: model.text("set_at_once"))
                Text(hint)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(title)
    }
}

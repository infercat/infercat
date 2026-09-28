import SwiftUI

/// First run: no sidebar and no toolbar until there is a host to show. One screen,
/// one button (design spec, mockups "First run"). Four shapes: welcome, starting,
/// no engine, damaged bundle — plus the honest failure of a name that did not save.
struct FirstRunView: View {
    @ObservedObject var model: HostModel
    @FocusState private var nameFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Loaf(asleep: model.status == nil).frame(width: 52, height: 52)
            Text(title)
                .font(.system(.title, weight: .semibold))
                .fixedSize(horizontal: false, vertical: true)
            Text(subtitle)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            content
            Spacer(minLength: 0)
        }
        .padding(44)
        .frame(width: 560, alignment: .leading)
        .frame(minHeight: 430, alignment: .top)
        .tint(Brand.cobalt)
        .onAppear { nameFocused = true }
    }

    private enum Shape { case welcome, starting, noEngine, damaged, nameFailed }
    private var shape: Shape {
        if model.missingBinary { return .damaged }
        if model.nameFailed != nil { return .nameFailed }
        if model.noEngine { return .noEngine }
        if model.starting { return .starting }
        return .welcome
    }

    private var title: String {
        switch shape {
        case .damaged: model.text("fr_nohost")
        case .nameFailed: model.text("fr_named", ["reason": model.nameFailed ?? ""])
        case .noEngine: model.text("fr_noengine")
        case .starting, .welcome: model.text("fr_title")
        }
    }

    private var subtitle: String {
        switch shape {
        case .damaged: model.text("fr_nohost_body")
        case .nameFailed: model.text("fr_body")
        case .noEngine: model.text("fr_noengine_body")
        case .starting, .welcome: model.text("fr_body")
        }
    }

    @ViewBuilder
    private var content: some View {
        switch shape {
        case .damaged:
            Link(model.text("fr_gethost"), destination: URL(string: "https://infercat.ai/download")!)
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
        case .nameFailed:
            HStack(spacing: 10) {
                Button(model.text("fr_retry")) { Task { await model.retryName() } }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(model.working)
                if model.working { Text(model.text("waiting")).font(.callout).foregroundStyle(.secondary) }
            }
        case .noEngine:
            VStack(alignment: .leading, spacing: 12) {
                Button(model.text("fr_again")) { model.refreshNow() }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                Link(model.text("fr_howto"), destination: URL(string: "https://infercat.ai/docs/engines")!)
                    .font(.callout)
            }
        case .starting, .welcome:
            VStack(alignment: .leading, spacing: 18) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(model.text("fr_name")).font(.callout).fontWeight(.medium)
                    TextField(model.text("fr_name"), text: $model.hostName)
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 320)
                        .focused($nameFocused)
                        .disabled(model.working || model.starting)
                    Text(model.text("fr_name_hint")).font(.caption).foregroundStyle(.secondary)
                }
                Button(model.text(model.starting ? "fr_starting" : "fr_start")) {
                    Task { await model.startSharing() }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .keyboardShortcut(.defaultAction)
                .disabled(model.working || model.starting || model.hostName.trimmingCharacters(in: .whitespaces).isEmpty)
                if let failure = model.failure, failure != .hostStopped {
                    Text(model.text("fr_failed", ["reason": model.describe(failure)]))
                        .font(.callout)
                        .foregroundStyle(Brand.down)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Text(model.text("fr_foot")).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

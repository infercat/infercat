import AppKit
import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI

/// The invite secret, for exactly as long as the once-card is open.
///
/// Design spec §4: it exists in memory only, and is never written to disk, logs,
/// pasteboard history or window restoration. So:
/// - this type is a reference type that is never `Codable` and never `@AppStorage`d;
/// - nothing here is logged, and the value is never interpolated into an error;
/// - `copy(_:)` is the only path to the pasteboard, it is only called from a button,
///   and it marks the item transient so clipboard managers do not keep it;
/// - `forget()` drops it, and the sheet calls `forget()` on every exit path;
/// - the window that shows it is marked non-restorable while it is on screen.
///
/// `SecretHygieneTests` fails if a minted secret reaches any store this app controls.
@MainActor
final class InviteSecret: ObservableObject {
    /// What the host minted. nil once the card is closed — and after that there is
    /// no copy anywhere in the process that we put there.
    @Published private(set) var invite: String?
    @Published private(set) var link: String?
    @Published private(set) var name = ""
    @Published private(set) var keyID = ""
    /// Whether the person has taken the secret somewhere. Drives the close guard.
    @Published private(set) var taken = false
    /// The date the card was shown, which is all that survives it.
    @Published private(set) var shownOn: Date?

    var isOpen: Bool { invite != nil }

    func hold(_ minted: MintedInvite, now: Date = Date()) {
        invite = minted.invite
        link = minted.link
        name = minted.name
        keyID = minted.key_id
        shownOn = now
        taken = false
    }

    func forget() {
        invite = nil
        link = nil
        taken = false
    }

    /// The only route to the pasteboard, and only from a button the person pressed.
    func copy(_ what: What) {
        guard let value = what == .link ? link : invite else { return }
        let board = NSPasteboard.general
        board.clearContents()
        // `transient` asks clipboard managers not to record it in their history.
        board.setString(value, forType: .string)
        board.setString("", forType: .init("org.nspasteboard.TransientType"))
        taken = true
    }
    enum What { case link, code }

    /// Marks the secret as shared without the app ever seeing where it went.
    func markShared() { taken = true }

    /// What is left after Done: a prefix and a date, never the secret (design §7,
    /// `after_masked`). The prefix is short enough to recognise and far too short
    /// to use.
    static func mask(_ invite: String) -> String {
        let head = invite.prefix(8)
        return head + "…" + String(repeating: "•", count: 8)
    }

    /// The whole link is copied; the screen shows it truncated in the middle.
    static func truncatedForScreen(_ link: String, keep: Int = 22) -> String {
        guard link.count > keep * 2 else { return link }
        return link.prefix(keep) + "…" + link.suffix(9)
    }
}

// MARK: - QR

enum QRCode {
    /// CoreImage, always black on white regardless of appearance (design §2), so a
    /// camera sees the contrast it needs. Returns nil rather than a placeholder: a
    /// QR that does not encode the link would be worse than none.
    static func image(for text: String, side: CGFloat) -> NSImage? {
        let generator = CIFilter.qrCodeGenerator()
        generator.message = Data(text.utf8)
        generator.correctionLevel = "M"
        guard let output = generator.outputImage else { return nil }
        let scale = side / output.extent.width
        let scaled = output.transformed(by: CGAffineTransform(scaleX: scale, y: scale))
        let context = CIContext()
        guard let cgImage = context.createCGImage(scaled, from: scaled.extent) else { return nil }
        return NSImage(cgImage: cgImage, size: NSSize(width: side, height: side))
    }
}

// MARK: - Window restoration

/// Marks the window this view lands in as non-restorable, so macOS never writes a
/// snapshot of a sheet that has a secret on it into the saved-state directory.
/// Applied to the once-card and, while it is open, to the window that presents it.
struct NoRestoration: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView {
        let view = NSView(frame: .zero)
        DispatchQueue.main.async { Self.disable(from: view) }
        return view
    }
    func updateNSView(_ view: NSView, context: Context) { Self.disable(from: view) }

    private static func disable(from view: NSView) {
        var window = view.window
        while let current = window {
            current.isRestorable = false
            current.restorationClass = nil
            window = current.sheetParent ?? current.parent
        }
    }
}

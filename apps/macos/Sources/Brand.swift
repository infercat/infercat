import AppKit
import CoreText
import SwiftUI

/// What the console's Swiss ink carries into the app (design spec §6): cobalt as the
/// accent, the loaf mark, IBM Plex Mono for measured values, 8 pt status squares.
/// Everything macOS draws — controls, sidebar, toolbar, sheets — stays the system's.
enum Brand {
    static let cobalt = Color(light: Color(red: 0x1F / 255, green: 0x3B / 255, blue: 1.0),
                              dark: Color(red: 0x3D / 255, green: 0x57 / 255, blue: 1.0))
    /// Yellow always carries an ink edge, so it is legible on both appearances.
    static let caution = Color(light: Color(red: 0.90, green: 0.78, blue: 0.0),
                               dark: Color(red: 0.98, green: 0.86, blue: 0.20))
    static let down = Color(light: Color(red: 0.78, green: 0.10, blue: 0.10),
                            dark: Color(red: 1.0, green: 0.42, blue: 0.40))

    /// Measured values only: numbers, ids, codes, URLs, model names.
    static func mono(_ size: CGFloat = 11, relativeTo style: Font.TextStyle = .caption) -> Font {
        .custom("IBMPlexMono", size: size, relativeTo: style)
    }

    static func registerFont() {
        guard let url = Bundle.main.url(forResource: "IBMPlexMono-Regular", withExtension: "ttf") else { return }
        CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
    }
}

extension Color {
    init(light: Color, dark: Color) {
        self.init(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            return NSColor(isDark ? dark : light)
        })
    }
}

/// The 8 pt status square. Colour is never the only signal: every square sits beside
/// its word, and VoiceOver reads the word (design spec §5).
struct StatusSquare: View {
    enum Kind: Sendable {
        case inFlight    // cobalt, filled
        case connected   // ink, filled
        case idle        // hollow
        case caution     // yellow with an ink edge
        case down        // red
    }
    let kind: Kind
    var size: CGFloat = 8
    var label: String?

    var body: some View {
        Rectangle()
            .fill(fill)
            .frame(width: size, height: size)
            .overlay(Rectangle().strokeBorder(Color.primary, lineWidth: edge))
            .accessibilityHidden(label == nil)
            .accessibilityLabel(label ?? "")
    }

    private var fill: Color {
        switch kind {
        case .inFlight: Brand.cobalt
        case .connected: .primary
        case .idle: .clear
        case .caution: Brand.caution
        case .down: Brand.down
        }
    }
    private var edge: CGFloat {
        switch kind {
        case .idle, .caution: 1
        default: 0
        }
    }
}

/// The loaf. Awake while the host runs, eyes closed when it is stopped. Drawn rather
/// than shipped as an asset so it is a template image at any size for the menu bar.
struct Loaf: View {
    var asleep = false

    var body: some View {
        Canvas { context, size in
            context.scaleBy(x: size.width / 32, y: size.height / 32)
            var shape = Path(ellipseIn: CGRect(x: 5.4, y: 4.7, width: 21.2, height: 17.8))
            shape.addRoundedRect(in: CGRect(x: 7, y: 21, width: 18, height: 8),
                                 cornerSize: CGSize(width: 2.6, height: 2.6))
            for x: CGFloat in [7, 19] {
                var ear = Path()
                ear.move(to: CGPoint(x: x, y: 10))
                ear.addLine(to: CGPoint(x: x, y: 2))
                ear.addLine(to: CGPoint(x: x + 6, y: 7))
                ear.closeSubpath()
                shape.addPath(ear)
            }
            context.fill(shape, with: .foreground)
            var tail = Path()
            tail.move(to: CGPoint(x: 25, y: 27.4))
            tail.addCurve(to: CGPoint(x: 27.4, y: 21.4),
                          control1: CGPoint(x: 29.4, y: 27.6), control2: CGPoint(x: 30.4, y: 23.4))
            context.stroke(tail, with: .foreground, style: StrokeStyle(lineWidth: 3, lineCap: .round))
            context.blendMode = .destinationOut
            for x: CGFloat in [11.6, 20.4] {
                if asleep {
                    var eye = Path()
                    eye.move(to: CGPoint(x: x - 3, y: 14.4))
                    eye.addQuadCurve(to: CGPoint(x: x + 3, y: 14.4), control: CGPoint(x: x, y: 17.6))
                    context.stroke(eye, with: .color(.white), style: StrokeStyle(lineWidth: 1.7, lineCap: .round))
                } else {
                    context.fill(Path(ellipseIn: CGRect(x: x - 3.3, y: 11.5, width: 6.6, height: 6.6)), with: .color(.white))
                }
            }
            if !asleep {
                context.blendMode = .normal
                for x: CGFloat in [11.6, 20.4] {
                    context.fill(Path(ellipseIn: CGRect(x: x - 2.38, y: 12.42, width: 4.76, height: 4.76)), with: .foreground)
                }
            }
        }
        .aspectRatio(1, contentMode: .fit)
        .accessibilityHidden(true)
    }
}

/// A fact cell: a rule, a small uppercase key, a value, a quiet second line.
struct FactCell: View {
    let key: String
    let value: String
    var detail: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Rectangle().fill(.primary).frame(height: 1)
            Text(key.uppercased()).font(.caption2).foregroundStyle(.secondary).padding(.top, 2)
            Text(value).font(.system(.callout, weight: .medium))
            if let detail {
                Text(detail).font(Brand.mono(10)).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityLabel([key, value, detail].compactMap { $0 }.joined(separator: ", "))
    }
}

extension View {
    /// macOS 26 blurs scrolled content under the toolbar. The one sentence of state
    /// sits at the top of every screen, so it must not read as dimmed: keep a hard
    /// edge where the system offers one, and do nothing on macOS 14 and 15.
    @ViewBuilder
    func hardScrollEdge() -> some View {
        if #available(macOS 26.0, *) {
            self.scrollEdgeEffectHidden(true, for: .top)
        } else {
            self
        }
    }
}

/// The small uppercase heading over a block ("Needs attention", "Connected now").
struct BlockLabel: View {
    let text: String
    var body: some View {
        Text(text.uppercased())
            .font(.caption2)
            .foregroundStyle(.secondary)
            .accessibilityLabel(text)
    }
}

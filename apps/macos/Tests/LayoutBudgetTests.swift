import AppKit
import XCTest
@testable import InfercatMac

/// Width budgets for the two places a string has nowhere to go.
///
/// This is not a layout engine: it measures the rendered width of a string in the
/// font it is drawn in and compares it against the space that place actually has.
/// That is enough to catch the failure that keeps happening — a sentence that fits
/// in English and does not in Chinese — without rendering anything. It cannot catch
/// a layout bug that is not about width, and it does not try to.
final class LayoutBudgetTests: XCTestCase {

    override func setUp() {
        super.setUp()
        // The app registers its bundled mono face at launch, which the test host
        // skips; register it here so these widths are the ones that get drawn.
        Brand.registerFont()
    }

    private func width(_ text: String, font: NSFont) -> CGFloat {
        NSAttributedString(string: text, attributes: [.font: font]).size().width
    }

    private var mono10: NSFont {
        NSFont(name: "IBMPlexMono", size: 10) ?? .monospacedSystemFont(ofSize: 10, weight: .regular)
    }

    /// The sidebar foot at its minimum width: 180 pt, less 12 pt of padding each side,
    /// the 8 pt square, 7 pt of spacing, and roughly 62 pt for the Stop button.
    /// About 79 pt is left for the uptime, and it must be one line.
    private let footBudget: CGFloat = 79

    func testTheUptimeFitsTheSidebarFootInBothLanguages() {
        // Four months of uptime, and a year of it — the second one drops to days
        // alone, which is the point of that rule.
        let longest = 400 * 86_400 + 23 * 3_600
        for language in [Language.en, .zh] {
            let text = Copy.text("sb_up", language: language,
                                 ["t": Copy.duration(seconds: longest, language: language)])
            XCTAssertLessThanOrEqual(width(text, font: mono10), footBudget,
                "\"\(text)\" does not fit the sidebar foot in \(language.rawValue)")
        }
        // And the everyday value, with room to spare.
        for language in [Language.en, .zh] {
            let text = Copy.text("sb_up", language: language,
                                 ["t": Copy.duration(seconds: 273_600, language: language)])
            XCTAssertLessThanOrEqual(width(text, font: mono10), footBudget * 0.8,
                "\"\(text)\" is tight in \(language.rawValue)")
        }
    }

    /// Every shape `duration` can take, at the narrowest place it is used.
    func testEveryDurationShapeFitsTheFoot() {
        let samples = [41, 15_120, 273_600, 86_400, 3_600,
                       99 * 86_400 + 23 * 3_600, 128 * 86_400 + 23 * 3_600,
                       400 * 86_400 + 23 * 3_600, 999 * 86_400]
        for seconds in samples {
            for language in [Language.en, .zh] {
                let text = Copy.text("sb_up", language: language,
                                     ["t": Copy.duration(seconds: seconds, language: language)])
                XCTAssertFalse(text.contains("…"), "a duration must never arrive pre-truncated")
                XCTAssertLessThanOrEqual(width(text, font: mono10), footBudget,
                    "\(seconds)s in \(language.rawValue) reads \"\(text)\"")
            }
        }
    }

    /// The once-card's left column is about 290 pt wide beside the 132 pt QR. The
    /// caption that explains how to copy is allowed two lines and no more.
    func testTheOnceCardCaptionFitsTwoLines() {
        let column: CGFloat = 290
        let caption = NSFont.preferredFont(forTextStyle: .caption2)
        for language in [Language.en, .zh] {
            let text = Copy.text("once_use_buttons", language: language)
            let lines = (width(text, font: caption) / column).rounded(.up)
            XCTAssertLessThanOrEqual(lines, 2,
                "the copy instructions need \(Int(lines)) lines in \(language.rawValue)")
        }
    }

    /// The other sentences on the once-card share that column.
    func testTheOnceCardBodyFitsItsColumn() {
        let card: CGFloat = 416   // 460 pt sheet, less 22 pt of padding each side
        let body = NSFont.preferredFont(forTextStyle: .callout)
        for language in [Language.en, .zh] {
            let text = Copy.text("once_body", language: language)
            XCTAssertLessThanOrEqual((width(text, font: body) / card).rounded(.up), 3,
                "the once-card body needs too many lines in \(language.rawValue)")
        }
    }
}

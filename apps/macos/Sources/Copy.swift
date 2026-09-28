import Foundation

enum Language: String, CaseIterable, Sendable {
    case system, en, zh

    /// Where the choice lives between launches. A language is a preference, not a
    /// secret, so UserDefaults is the right place — and it is read before the model
    /// exists, so nothing ever renders in the other language first.
    private static let defaultsKey = "ai.infercat.mac.language"

    static var remembered: Language {
        guard let raw = UserDefaults.standard.string(forKey: defaultsKey),
              let stored = Language(rawValue: raw) else { return .system }
        return stored
    }

    func remember() {
        UserDefaults.standard.set(rawValue, forKey: Self.defaultsKey)
    }
    /// The table key this language reads.
    var code: String {
        switch self {
        case .en: "en"
        case .zh: "zh"
        case .system: Locale.preferredLanguages.first?.hasPrefix("zh") == true ? "zh" : "en"
        }
    }
}

/// Every visible string, EN and ZH, from `Resources/Copy.json`. A missing key returns
/// the key itself so a gap shows up in a screenshot instead of hiding behind English.
enum Copy {
    nonisolated(unsafe) private static let table: [String: [String: String]] = {
        guard let url = Bundle.main.url(forResource: "Copy", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let decoded = try? JSONDecoder().decode([String: [String: String]].self, from: data)
        else { return [:] }
        return decoded
    }()

    static func text(_ key: String, language: Language, _ values: [String: String] = [:]) -> String {
        guard var text = table[key]?[language.code] else { return key }
        for (name, value) in values { text = text.replacingOccurrences(of: "{\(name)}", with: value) }
        return text
    }

    /// Compact for lists, popovers and fact cells; exact with grouping below 10,000
    /// (design spec §4, "Numbers"). One formatter, so no row ever shows two forms.
    static func compact(_ value: Int) -> String {
        if value < 10_000 { return exact(value) }
        if value < 1_000_000 { return trim(Double(value) / 1_000) + "k" }
        return trim(Double(value) / 1_000_000) + "M"
    }

    static func exact(_ value: Int) -> String {
        value.formatted(.number.grouping(.automatic))
    }

    /// Three significant figures, as the console prints them: 1.56M, 84.2k, 192k —
    /// and 22k rather than 22.0k, because a trailing zero is not information.
    private static func trim(_ value: Double) -> String {
        let places = value < 10 ? 2 : (value < 100 ? 1 : 0)
        return value.formatted(.number.precision(.fractionLength(0...places)).grouping(.never))
    }

    /// `3d 4h`, `4h 12m`, `41s` — two units at most, largest first, and in the app's
    /// own language: English abbreviates, Chinese uses its own words rather than
    /// leaving `d` and `h` sitting inside a Chinese sentence.
    static func duration(seconds: Int, language: Language) -> String {
        let seconds = max(0, seconds)
        let days = seconds / 86_400
        let hours = (seconds % 86_400) / 3_600
        let minutes = (seconds % 3_600) / 60
        func unit(_ value: Int, _ key: String) -> String {
            language.code == "zh" ? "\(value) \(text(key, language: language))" : "\(value)\(text(key, language: language))"
        }
        let joiner = language.code == "zh" ? " " : " "
        if days > 0 {
            // Past a hundred days the hours are noise, and the two units together no
            // longer fit the sidebar foot in Chinese. Drop them.
            guard days < 100, hours > 0 else { return unit(days, "u_day") }
            return unit(days, "u_day") + joiner + unit(hours, "u_hour")
        }
        if hours > 0 {
            return minutes > 0 ? unit(hours, "u_hour") + joiner + unit(minutes, "u_min") : unit(hours, "u_hour")
        }
        if minutes > 0 { return unit(minutes, "u_min") }
        return unit(seconds, "u_sec")
    }
}

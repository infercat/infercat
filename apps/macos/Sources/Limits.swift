import Foundation

/// The limit fields, their CLI flags, and where each one belongs in the form.
///
/// The CLI's own help is the source (`infercat keys add --help`): the four in-place
/// fields are the ones a host actually reasons about; the rest live under "More
/// limits", and the five that depend on a destination are offered only when this
/// machine has that destination (design spec §2, "New invite").
enum LimitField: String, CaseIterable, Identifiable, Sendable {
    case rpm, dailyTokens, maxConcurrent, maxOutputTokens        // in place
    case tpm, maxContext                                          // more limits
    case searchPerDay, dailyImages, maxQueuedImages, dailyAudioSeconds, dailySpeechChars

    var id: String { rawValue }

    /// The flag this field is spelled with on `keys add` and `keys limits`.
    var flag: String {
        switch self {
        case .rpm: "--rpm"
        case .tpm: "--tpm"
        case .maxConcurrent: "--max-concurrent"
        case .maxOutputTokens: "--max-output-tokens"
        case .maxContext: "--max-context"
        case .dailyTokens: "--daily-tokens"
        case .searchPerDay: "--search-per-day"
        case .dailyImages: "--daily-images"
        case .maxQueuedImages: "--max-queued-images"
        case .dailyAudioSeconds: "--daily-audio-seconds"
        case .dailySpeechChars: "--daily-speech-chars"
        }
    }

    /// The copy key for the field's label (design spec §7, `l_*`).
    var copyKey: String {
        switch self {
        case .rpm: "l_rpm"
        case .dailyTokens: "l_daily"
        case .maxConcurrent: "l_conc"
        case .maxOutputTokens: "l_out"
        case .tpm: "l_tpm"
        case .maxContext: "l_ctx"
        case .searchPerDay: "l_search"
        case .dailyImages: "l_images"
        case .maxQueuedImages: "l_queue"
        case .dailyAudioSeconds: "l_audio"
        case .dailySpeechChars: "l_speech"
        }
    }

    /// The four the sheet shows in place before "More limits".
    static let inPlace: [LimitField] = [.rpm, .dailyTokens, .maxConcurrent, .maxOutputTokens]
    /// The rest, in the order the design lists them.
    static let more: [LimitField] = [.tpm, .maxContext, .searchPerDay, .dailyImages,
                                     .maxQueuedImages, .dailyAudioSeconds, .dailySpeechChars]

    /// The `destinations[].id` this limit needs before it is worth offering.
    /// `nil` means the host always enforces it.
    /// `id`, not `kind`: every destination the host builds carries `kind: "engine"`,
    /// so `kind` cannot tell an image engine from a speech one. The five ids are
    /// `text`, `transcribe`, `speech`, `embed`, `images`.
    ///
    /// `searchPerDay` is deliberately not gated. The design asks for it only when the
    /// matching destination exists, but web search is a host tool rather than a
    /// destination: it has no `destinations[]` entry and no field on any machine
    /// payload, so its availability is not discoverable. The limit is always
    /// enforced, so it is always offered. Recorded in the fixtures README.
    var requiresDestination: String? {
        switch self {
        case .dailyImages, .maxQueuedImages: "images"
        case .dailyAudioSeconds: "transcribe"
        case .dailySpeechChars: "speech"
        default: nil
        }
    }

    /// What this field must send to mean "no limit".
    ///
    /// **Not zero.** The host coerces `0`: on `POST /keys` it becomes the default for
    /// rpm, tpm, max_concurrent, max_output_tokens and daily_tokens, and for
    /// search_per_day, daily_images, max_queued_images, daily_audio_seconds and
    /// daily_speech_chars it becomes the default on *every* read of the key file, so
    /// `0` can never mean unlimited for those. A negative survives every coercion
    /// layer and each enforcement site asks `limit > 0`, so `-1` is the only value
    /// that means unlimited on both routes. `max_context` is the exception: `0` is
    /// its documented sentinel for "the engine's own window".
    var unlimitedValue: Int { self == .maxContext ? 0 : -1 }

    /// A ceiling the host will not exceed however large a number it is handed.
    var hostCeiling: Int? { self == .maxQueuedImages ? 16 : nil }

    /// Whether "no limit" is a thing this field can actually be.
    ///
    /// `max_context` cannot: `0` means the engine's own window, and there is no value
    /// that means "larger than the engine can do". `max_queued_images` cannot either:
    /// the host clamps anything negative — or anything over 16 — back to 16. Offering
    /// the choice on those two would be offering something the host would ignore.
    var unlimitedHonoured: Bool { self != .maxContext && self != .maxQueuedImages }

    /// Whether this machine can serve the thing this limit limits.
    func isOffered(by status: HostStatus?) -> Bool {
        guard let needed = requiresDestination else { return true }
        return status?.servedKinds.contains(needed) == true
    }

    func value(in limits: FriendKey.Limits) -> Int? {
        switch self {
        case .rpm: limits.rpm
        case .tpm: limits.tpm
        case .maxConcurrent: limits.max_concurrent
        case .maxOutputTokens: limits.max_output_tokens
        case .maxContext: limits.max_context
        case .dailyTokens: limits.daily_tokens
        case .searchPerDay: limits.search_per_day
        case .dailyImages: limits.daily_images
        case .maxQueuedImages: limits.max_queued_images
        case .dailyAudioSeconds: limits.daily_audio_seconds
        case .dailySpeechChars: limits.daily_speech_chars
        }
    }
}

/// What the invite sheet and the Edit limits sheet edit.
///
/// A field holds the text a person typed, so "cleared" and "0" stay different things
/// right up to the moment a command is built. `models` empty means every model, which
/// is what the CLI means by an empty `--models`.
///
/// **Touched, not just empty.** The CLI is sparse — "only the flags you pass change",
/// and an unpassed flag on `keys add` takes the host's default. The app has no way to
/// read those defaults (see the fixtures README), so a field nobody edited must not be
/// sent at all: an empty box the app could not prefill would otherwise mint an
/// unlimited key. A field the person *cleared* is a different thing, and does send.
struct LimitsDraft: Equatable, Sendable {
    /// What a field is set to. Three states, and the person picks between them —
    /// there is no way to land in one by accident.
    enum Setting: Equatable, Sendable {
        /// Nobody touched it. Nothing is sent, and the host applies its own value.
        case hostDefault
        /// A number the person typed.
        case custom(String)
        /// An explicit "no limit". Sends this field's own unlimited value.
        case unlimited
    }

    private var settings: [String: Setting] = [:]
    var models: String = "" { didSet { if models != oldValue { touchedModels = true } } }
    private(set) var touchedModels = false
    var agent = false

    init() {}

    /// Prefilled from limits the host reported for a real key — never from numbers
    /// baked into the app. An existing key has no "host's default" state: the host has
    /// already told us what it applied, so every field is either a number or unlimited.
    init(from limits: FriendKey.Limits, agent: Bool = false) {
        for field in LimitField.allCases {
            guard let value = field.value(in: limits) else { continue }
            // A stored value at or below zero means unlimited — except on the two
            // fields where the host has no such state, where it means "leave it
            // alone" and the placeholder explains what the host will do instead.
            settings[field.rawValue] = if value > 0 {
                .custom(Copy.exact(value))
            } else {
                field.unlimitedHonoured ? .unlimited : .hostDefault
            }
        }
        models = (limits.models ?? []).joined(separator: ", ")
        touchedModels = true
        self.agent = agent
    }

    subscript(field: LimitField) -> Setting {
        get { settings[field.rawValue] ?? .hostDefault }
        set { settings[field.rawValue] = newValue }
    }

    /// The text in the field's box. Empty for both of the non-numeric states; the
    /// state itself is shown by the control beside it, never by the emptiness.
    func text(_ field: LimitField) -> String {
        if case let .custom(value) = self[field] { return value }
        return ""
    }

    /// Typing a number makes the field custom; clearing it returns the field to the
    /// host's default, **not** to unlimited — that one is only ever chosen.
    mutating func setText(_ value: String, for field: LimitField) {
        let trimmed = value.trimmingCharacters(in: .whitespaces)
        self[field] = trimmed.isEmpty ? .hostDefault : .custom(value)
    }

    func isTouched(_ field: LimitField) -> Bool { self[field] != .hostDefault }

    /// The typed value, or nil when the field holds no number.
    func number(_ field: LimitField) -> Int? {
        let raw = text(field).filter { $0.isNumber }
        return raw.isEmpty ? nil : Int(raw)
    }

    var modelList: [String] {
        models.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    /// Every custom field carries a number the CLI will accept.
    func isWellFormed(offeredBy status: HostStatus?) -> Bool {
        LimitField.allCases.allSatisfy { field in
            guard field.isOffered(by: status), case .custom = self[field] else { return true }
            return number(field) != nil
        }
    }

    /// The limit flags for `keys add` / `keys limits`.
    ///
    /// Only fields the person touched are sent; the rest keep the host's value.
    /// A touched-and-cleared field sends that field's own "no limit" value, which is
    /// `-1` everywhere except `--max-context`, where it is `0` (see `unlimitedValue`).
    /// A field this machine cannot serve is never sent, so a Mac with no image engine
    /// is not handed image limits it would have to invent a meaning for.
    func flags(offeredBy status: HostStatus?) -> [String] {
        var argv: [String] = []
        for field in LimitField.allCases where field.isOffered(by: status) {
            switch self[field] {
            case .hostDefault:
                continue
            case .unlimited:
                argv += [field.flag, String(field.unlimitedValue)]
            case .custom:
                guard let value = number(field) else { continue }
                argv += [field.flag, String(value)]
            }
        }
        // An explicitly empty `--models` sends null, which clears the allowlist back
        // to every model. Not passing it at all leaves the allowlist untouched.
        if touchedModels { argv += ["--models", modelList.joined(separator: ",")] }
        return argv
    }

    /// Nothing was chosen and nothing was prefilled: the host decides everything.
    var isEntirelyTheHosts: Bool {
        !touchedModels && LimitField.allCases.allSatisfy { self[$0] == .hostDefault }
    }

    /// One line for the summary row: "20 requests a minute · 200k tokens a day · 1 at
    /// once", or the honest sentence when the app has no numbers to show.
    func summary(language: Language) -> String {
        guard !isEntirelyTheHosts else { return Copy.text("inv_host_defaults", language: language) }
        return Copy.text("inv_summary", language: language, [
            "rpm": display(.rpm, language: language),
            "day": display(.dailyTokens, language: language),
            "c": display(.maxConcurrent, language: language),
        ])
    }

    private func display(_ field: LimitField, language: Language) -> String {
        switch self[field] {
        case .hostDefault: Copy.text("inv_host_default", language: language)
        case .unlimited: Copy.text("inv_none", language: language)
        case .custom: number(field).map { Copy.compact($0) } ?? Copy.text("inv_host_default", language: language)
        }
    }
}

import Foundation

/// One settled request, as the watch stream's `event` lines carry it.
///
/// **This type is the privacy boundary.** `usage.Event` on the host also has `prompt`
/// and `completion` fields, populated when the host runs with `--log-prompts`. The
/// stream is documented to strip them, but this decoder does not declare them either,
/// so a host that ever regressed could not put request text into the app: there is no
/// property for it to land in. `ActivityTests` asserts that an event line carrying
/// both fields decodes with no trace of them. Design spec §2: "Never: prompt or
/// completion text."
struct UsageEvent: Decodable, Sendable {
    var ts: Date
    var key_id: String
    var endpoint: String
    var status: Int
    var prompt_tokens: Int
    var completion_tokens: Int
    var queued_ms: Int
    var ttft_ms: Int
    var total_ms: Int
    /// Every one of these is `omitempty` on the host and genuinely absent in practice.
    var kind: String?
    var model: String?
    var via: String?
    var code: String?
}

/// What the Activity list holds. A dropped-events marker is a row of its own, because
/// a gap the person cannot see is worse than one they can.
enum ActivityItem: Identifiable, Sendable {
    case request(ActivityRow)
    case gap(id: UUID, count: Int)

    var id: String {
        switch self {
        case .request(let row): row.id
        case .gap(let id, _): id.uuidString
        }
    }
    var row: ActivityRow? {
        if case .request(let row) = self { return row }
        return nil
    }
}

/// One row, already reduced to what the screen shows. Nothing here can carry text a
/// friend typed.
struct ActivityRow: Identifiable, Sendable {
    let id: String
    let at: Date
    let keyID: String
    /// `chat`, `transcribe`, `speech`, `embed`, `image`, `search` — a word, not a path.
    let kind: Kind
    let status: Int
    let code: String?
    let promptTokens: Int
    let completionTokens: Int
    let queuedMS: Int
    let ttftMS: Int
    let totalMS: Int
    let model: String?
    let via: String?

    var failed: Bool { status >= 400 }
    /// The friend cancelled; not a refusal and not a fault.
    var cancelled: Bool { code == "client_closed" }

    enum Kind: String, Sendable {
        case chat, transcribe, speech, embed, image, search, app, other

        /// `kind` when the host said it, otherwise the endpoint path. Old rows carry
        /// no `kind` at all, and the comment on the host's own field says an empty
        /// one means text.
        static func of(_ event: UsageEvent) -> Kind {
            if let named = event.kind, let kind = Kind(rawValue: named) { return kind }
            switch event.endpoint {
            case "/v1/chat/completions", "/v1/responses": return .chat
            case "/v1/audio/transcriptions": return .transcribe
            case "/v1/audio/speech": return .speech
            case "/v1/embeddings": return .embed
            case "/v1/images/generations": return .image
            case "web_search": return .search
            case "/v1/models", "/me": return .app
            default: return event.kind == nil ? .chat : .other
            }
        }
    }

    init(_ event: UsageEvent) {
        // The host has no event id, so one is made from what identifies the request.
        id = "\(event.ts.timeIntervalSince1970)-\(event.key_id)-\(event.endpoint)-\(event.total_ms)"
        at = event.ts
        keyID = event.key_id
        kind = Kind.of(event)
        status = event.status
        code = event.code
        promptTokens = event.prompt_tokens
        completionTokens = event.completion_tokens
        queuedMS = event.queued_ms
        ttftMS = event.ttft_ms
        totalMS = event.total_ms
        model = event.model
        via = event.via
    }
}

/// The copy key for a result, as words. Codes are an open set, so an unknown one
/// still reads as a refusal and the code itself appears only in the selected row
/// (design spec §4, "Words, not codes").
enum ResultWord {
    static func key(status: Int, code: String?) -> String {
        if status < 400 { return "res_done" }
        guard let code, !code.isEmpty else { return "res_refused" }
        switch code {
        case "client_closed": return "res_cancelled"
        case "rate_limited": return "res_rate"
        case "concurrency_limited": return "res_concurrency"
        case "budget_exhausted": return "res_budget"
        case "audio_budget_exhausted": return "res_audio_budget"
        case "speech_budget_exhausted": return "res_speech_budget"
        case "image_budget_exhausted": return "res_image_budget"
        case "image_queue_full": return "res_image_queue"
        case "image_abandoned": return "res_image_abandoned"
        case "key_paused": return "res_paused"
        case "key_revoked": return "res_revoked"
        case "invalid_key": return "res_invalid_key"
        case "model_not_allowed": return "res_model"
        case "context_too_long": return "res_context"
        case "body_too_large": return "res_too_large"
        case "queue_timeout": return "res_queue_timeout"
        case "upstream_down", "upstream_error": return "res_engine"
        case "images_not_supported": return "res_no_images"
        case "agent_unavailable": return "res_agent"
        case "storage_failed": return "res_storage"
        case "not_found": return "res_not_found"
        case "invalid_request": return "res_bad_request"
        default: return "res_refused"
        }
    }
}

/// The live tail of settled requests, from the `event` lines of the one watch stream.
///
/// It owns no subprocess and starts none: every row arrives on a stream that is
/// already running for the Overview's sake, so Activity costs nothing to keep open.
@MainActor
final class ActivityLog: ObservableObject {
    /// The design's buffer: the last 500 items, gap markers included.
    static let capacity = 500

    /// What the screen shows. While paused this does not move.
    @Published private(set) var visible: [ActivityItem] = []
    /// How many have arrived since the pause began.
    @Published private(set) var heldBack = 0
    @Published private(set) var paused = false

    /// Capture-only: the filter the screen opens with, so a screenshot can show one.
    var previewErrorsOnly = false

    private var buffer: [ActivityItem] = []
    /// `dropped` lines arrive separately from the events they stand for, so a run of
    /// them collapses into the one gap row at the tail.
    private var tailGap: UUID?

    func append(_ event: UsageEvent) {
        // App polls are not what "what just happened" means, and an idle browser tab
        // polls every 30 s per friend — they would crowd out the requests. The
        // mockup's own count is the host's `model_calls`, not its `requests`.
        guard ActivityRow.Kind.of(event) != .app else { return }
        tailGap = nil
        push(.request(ActivityRow(event)))
    }

    /// The stream told us it lost some. Say so; never close the gap silently.
    func drop(_ count: Int) {
        guard count > 0 else { return }
        if let tail = tailGap, case let .gap(id, existing)? = buffer.last, id == tail {
            buffer[buffer.count - 1] = .gap(id: id, count: existing + count)
            publish()
            return
        }
        let id = UUID()
        tailGap = id
        push(.gap(id: id, count: count))
    }

    private func push(_ item: ActivityItem) {
        buffer.append(item)
        if buffer.count > Self.capacity { buffer.removeFirst(buffer.count - Self.capacity) }
        publish()
    }

    private func publish() {
        guard !paused else {
            heldBack = max(0, buffer.count - visible.count)
            return
        }
        visible = buffer
        heldBack = 0
    }

    func setPaused(_ value: Bool) {
        paused = value
        // Resuming shows everything that arrived meanwhile; the stream never stopped.
        if !value { publish() }
    }

    func clear() {
        buffer.removeAll()
        visible.removeAll()
        heldBack = 0
        tailGap = nil
    }

    /// The rows the screen draws, after the two filters the design allows.
    func rows(friend: String?, errorsOnly: Bool) -> [ActivityItem] {
        visible.reversed().filter { item in
            switch item {
            case .gap:
                // A gap is about the stream, not about one friend. Under a friend
                // filter it is hidden, because showing it would imply the lost events
                // were that friend's. Under "Errors" it stays, because some of what
                // was lost may have been errors and hiding it would claim otherwise.
                return friend == nil
            case .request(let row):
                if let friend, row.keyID != friend { return false }
                if errorsOnly, !row.failed { return false }
                return true
            }
        }
    }
}

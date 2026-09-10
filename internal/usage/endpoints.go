package usage

// ModelEndpoint classifies actual engine work for accounting and human output.
// An empty class is a poll/control route, not a model call.
func ModelEndpoint(path string) (word, class string) {
	switch path {
	case "/v1/chat/completions":
		return "chat", "tokens"
	case "/v1/responses":
		return "responses", "tokens"
	case "/v1/embeddings":
		return "embed", "tokens"
	case "/v1/audio/transcriptions":
		return "transcribe", "audio"
	case "/v1/images/generations":
		return "images", "images"
	case "/v1/audio/speech":
		return "speech", "speech"
	}
	return "", ""
}

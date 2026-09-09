package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Code is a gateway error code (docs/ARCHITECTURE.md §Gateway HTTP API). The table below is the one
// concept "error code table": every error the friend can see is one row here, with its HTTP status and
// OpenAI-shaped type. Two rows are not in the contract's list because the contract has no 400/404 codes:
// CodeInvalidRequest (malformed JSON) and CodeNotFound (unknown route). CodeClientClosed never reaches a
// friend; it only appears in usage events when the client went away before a response was written.
type Code string

const (
	CodeInvalidKey         Code = "invalid_key"
	CodeKeyPaused          Code = "key_paused"
	CodeKeyRevoked         Code = "key_revoked"
	CodeModelNotAllowed    Code = "model_not_allowed"
	CodeBodyTooLarge       Code = "body_too_large"
	CodeContextTooLong     Code = "context_too_long"
	CodeRateLimited        Code = "rate_limited"
	CodeConcurrencyLimited Code = "concurrency_limited"
	CodeBudgetExhausted    Code = "budget_exhausted"
	CodeQueueTimeout       Code = "queue_timeout"
	CodeUpstreamDown       Code = "upstream_down"
	CodeUpstreamError      Code = "upstream_error"
	CodeImagesNotSupported Code = "images_not_supported"
	CodeInvalidRequest     Code = "invalid_request"
	CodeNotFound           Code = "not_found"
	CodeClientClosed       Code = "client_closed"
)

type codeRow struct {
	status int
	typ    string
}

var codeTable = map[Code]codeRow{
	CodeInvalidKey:         {http.StatusUnauthorized, "authentication_error"},
	CodeKeyPaused:          {http.StatusForbidden, "permission_error"},
	CodeKeyRevoked:         {http.StatusForbidden, "permission_error"},
	CodeModelNotAllowed:    {http.StatusForbidden, "permission_error"},
	CodeBodyTooLarge:       {http.StatusRequestEntityTooLarge, "invalid_request_error"},
	CodeContextTooLong:     {http.StatusUnprocessableEntity, "invalid_request_error"},
	CodeRateLimited:        {http.StatusTooManyRequests, "rate_limit_error"},
	CodeConcurrencyLimited: {http.StatusTooManyRequests, "rate_limit_error"},
	CodeBudgetExhausted:    {http.StatusTooManyRequests, "rate_limit_error"},
	CodeQueueTimeout:       {http.StatusServiceUnavailable, "upstream_error"},
	CodeUpstreamDown:       {http.StatusServiceUnavailable, "upstream_error"},
	CodeUpstreamError:      {http.StatusBadGateway, "upstream_error"},
	CodeImagesNotSupported: {http.StatusBadRequest, "invalid_request_error"},
	CodeInvalidRequest:     {http.StatusBadRequest, "invalid_request_error"},
	CodeNotFound:           {http.StatusNotFound, "invalid_request_error"},
}

// gwError is a gateway-originated failure: a code, a human sentence, and (for 429/503) the number of
// whole seconds the friend should wait. It is the single value every rejection path returns so the
// handler has one place that writes error bodies and one place that records usage.
type gwError struct {
	Code            Code
	Message         string
	RetryAfter      int // seconds; 0 = no header
	Limit, InFlight int // per-key concurrency snapshot; absent for other errors
}

func (e *gwError) Error() string { return string(e.Code) + ": " + e.Message }

func (e *gwError) Status() int {
	if row, ok := codeTable[e.Code]; ok {
		return row.status
	}
	return http.StatusInternalServerError
}

func errf(code Code, retryAfter int, format string, args ...any) *gwError {
	return &gwError{Code: code, Message: fmt.Sprintf(format, args...), RetryAfter: retryAfter}
}

// errorBody is the OpenAI-shaped JSON every error response carries.
type errorBody struct {
	Error struct {
		Message  string `json:"message"`
		Limit    int    `json:"limit,omitempty"`
		InFlight int    `json:"in_flight,omitempty"`
		Type     string `json:"type"`
		Code     Code   `json:"code"`
		// RetryAfter is set only in a stream error event (018): once the head is out the
		// Retry-After header has nowhere to go, so the seconds ride inside the event.
		RetryAfter int `json:"retry_after,omitempty"`
	} `json:"error"`
}

func errorJSON(e *gwError, inStream bool) []byte {
	var b errorBody
	b.Error.Message = e.Message
	b.Error.Limit, b.Error.InFlight = e.Limit, e.InFlight
	b.Error.Type = codeTable[e.Code].typ
	b.Error.Code = e.Code
	if inStream {
		b.Error.RetryAfter = e.RetryAfter
	}
	out, _ := json.Marshal(b)
	return out
}

// writeError writes e as a full HTTP response. Callers must not have written headers yet.
func writeError(w http.ResponseWriter, e *gwError) {
	body := errorJSON(e, false)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if e.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	w.WriteHeader(e.Status())
	_, _ = w.Write(body)
}

// writeStreamError ends a stream whose head is already out with an SSE error event — the friend's
// client sees why the stream stopped instead of a silent truncation (Surfaces tell the truth) — and
// the [DONE] every stream ends with. Retry-After, when the code carries one, rides inside the event.
func writeStreamError(w http.ResponseWriter, e *gwError) {
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", errorJSON(e, true))
	_ = http.NewResponseController(w).Flush()
}

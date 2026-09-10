package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptrace"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

type audioRequest struct {
	raw            []byte
	contentType    string
	dispatched     atomic.Bool
	measured       bool
	duration       *float64
	chars          int
	responseFormat string
}

func (q *request) proxyAudio(kind endpoint) {
	q.kind = kind
	q.audio = &audioRequest{}
	q.ev.Kind = "transcription"
	if kind == speechEndpoint {
		q.ev.Kind = "speech"
		q.ev.Stream = true
	}
	// n.stream stays false: a binary response must never receive SSE queue comments.
	for _, stage := range []func() *gwError{q.audioHealth, q.admitKey, q.readAudio, q.reserveAudio, q.acquireSlot, q.callAudio, q.relayAudio} {
		if err := stage(); err != nil {
			if q.outcome == outcomeNone {
				q.outcome = outcomeRejected
			}
			q.fail(err)
			return
		}
	}
	q.outcome = outcomeServed
}
func (q *request) audioHealth() *gwError {
	if q.g.audioHistoryErr != nil {
		return errf(CodeUpstreamDown, 1, "the host's audio usage history is unavailable")
	}
	if !q.destination.Audio.Info().Health.OK {
		return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's audio engine is not answering")
	}
	return nil
}
func (q *request) readAudio() *gwError {
	limit := q.g.maxBody
	if q.kind == transcribeEndpoint {
		limit = 25 << 20
	}
	if q.r.ContentLength > limit {
		return errf(CodeBodyTooLarge, 0, "request body exceeds %d bytes", limit)
	}
	q.buffered = true
	q.g.bodies.Add(1)
	raw, err := io.ReadAll(http.MaxBytesReader(q.w, q.r.Body, limit))
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			q.stalled = true
		}
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			return errf(CodeBodyTooLarge, 0, "request body exceeds %d bytes", limit)
		}
		return errf(CodeInvalidRequest, 0, "audio body was not received completely")
	}
	_ = q.rc.SetReadDeadline(time.Time{})
	q.audio.raw = raw
	q.audio.contentType = q.r.Header.Get("Content-Type")
	model := ""
	if q.kind == speechEndpoint {
		body, err := decodeObject(raw)
		if err != nil {
			return errf(CodeInvalidRequest, 0, "speech body must be a JSON object")
		}
		model, _ = body["model"].(string)
		model = q.selectAudioModel(model)
		body["model"] = model
		input, ok := body["input"].(string)
		if !ok || input == "" {
			return errf(CodeInvalidRequest, 0, "speech input must be a nonempty string")
		}
		q.audio.chars = utf8.RuneCountInString(input)
		q.audio.raw, _ = json.Marshal(body)
		q.audio.contentType = "application/json"
	} else {
		typ, params, err := mime.ParseMediaType(q.audio.contentType)
		if err != nil || typ != "multipart/form-data" || params["boundary"] == "" {
			return errf(CodeInvalidRequest, 0, "transcription requires multipart/form-data")
		}
		reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		haveFile, haveModel, haveFormat := false, false, false
		var parts []struct {
			part *multipart.Part
			data []byte
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return errf(CodeInvalidRequest, 0, "invalid multipart audio body")
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return errf(CodeInvalidRequest, 0, "invalid multipart audio field")
			}
			parts = append(parts, struct {
				part *multipart.Part
				data []byte
			}{part, data})
			switch part.FormName() {
			case "response_format":
				if haveFormat {
					return errf(CodeInvalidRequest, 0, "duplicate response_format field")
				}
				haveFormat = true
				q.audio.responseFormat = string(data)
			case "model":
				if haveModel {
					return errf(CodeInvalidRequest, 0, "duplicate model field")
				}
				haveModel = true
				model = string(data)
			case "file":
				if haveFile || len(data) == 0 {
					return errf(CodeInvalidRequest, 0, "exactly one nonempty audio file is required")
				}
				haveFile = true
				seconds, known := containerSeconds(data)
				q.audio.measured = known
				if !known {
					seconds = q.g.cfg.MaxTranscriptionSeconds
				}
				if seconds > q.g.cfg.MaxTranscriptionSeconds {
					return errf(CodeInvalidRequest, 0, "audio duration exceeds the host's %.3g second per-request ceiling", q.g.cfg.MaxTranscriptionSeconds)
				}
				q.ev.ReservedSeconds = seconds
			}
		}
		if !haveFile {
			return errf(CodeInvalidRequest, 0, "audio file is required")
		}
		model = q.selectAudioModel(model)
		var rebuilt bytes.Buffer
		writer := multipart.NewWriter(&rebuilt)
		for _, item := range parts {
			if item.part.FormName() == "model" || item.part.FormName() == "response_format" {
				continue
			}
			dst, err := writer.CreatePart(item.part.Header)
			if err != nil {
				return errf(CodeInvalidRequest, 0, "invalid multipart audio field")
			}
			_, _ = dst.Write(item.data)
		}
		_ = writer.WriteField("model", model)
		format := q.audio.responseFormat
		if format == "" || format == "json" {
			format = "verbose_json"
		}
		_ = writer.WriteField("response_format", format)
		_ = writer.Close()
		q.audio.raw, q.audio.contentType = rebuilt.Bytes(), writer.FormDataContentType()
	}
	if model == "" {
		return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host has no audio model configured or reported")
	}
	if err := q.resolveDestination(model); err != nil {
		return err
	}
	q.ev.Model = model
	return nil
}

// Explicit host choice wins the default; a health-only engine needs that choice.
func audioModel(engine upstream.AudioEngine, configured string) *string {
	if engine == nil || !engine.Info().Health.OK {
		return nil
	}
	if configured != "" {
		return &configured
	}
	for _, id := range engine.Info().Models {
		if id != "" {
			return &id
		}
	}
	return nil
}
func (q *request) selectAudioModel(requested string) string {
	if requested != "" && slices.Contains(q.destination.Audio.Info().Models, requested) {
		return requested
	}
	configured := q.destination.model
	if model := audioModel(q.destination.Audio, configured); model != nil {
		return *model
	}
	return ""
}
func (q *request) reserveAudio() *gwError {
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := q.g.lim.now()
	st.prune(now)
	lim := keys.AudioDefaults(q.key.Limits)
	retry := secondsUntil(st.day.Add(24*time.Hour), now)
	if q.kind == transcribeEndpoint {
		need := q.ev.ReservedSeconds
		if lim.DailyAudioSeconds > 0 && st.audioSeconds+st.audioReserved+need > float64(lim.DailyAudioSeconds) {
			return errf(CodeAudioBudgetExhausted, retry, "daily audio budget: %.3g seconds remaining; this request reserves %.3g", math.Max(0, float64(lim.DailyAudioSeconds)-st.audioSeconds-st.audioReserved), need)
		}
		q.adm.audioSeconds = need
		st.audioReserved += need
	} else {
		need := q.audio.chars
		if lim.DailySpeechChars > 0 && st.speechChars+st.speechReserved+need > lim.DailySpeechChars {
			return errf(CodeSpeechBudgetExhausted, retry, "daily speech character budget is exhausted")
		}
		q.adm.speechChars = need
		st.speechReserved += need
	}
	return nil
}
func (q *request) callAudio() *gwError {
	ctx, cancel := context.WithCancel(q.r.Context())
	q.cancelUpstream = cancel
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { q.audio.dispatched.Store(true) }})
	resp, err := q.destination.Audio.AudioDo(ctx, string(q.kind), q.audio.contentType, q.audio.raw)
	if err != nil {
		q.outcome = outcomeEngineErr
		if q.r.Context().Err() != nil {
			q.outcome = outcomeCut
		}
		return q.upstreamErr(err)
	}
	q.resp = resp
	return nil
}
func (q *request) relayAudio() *gwError {
	q.idle = time.AfterFunc(q.g.idleTimeout, func() { q.engineIdle.Store(true); q.cancelUpstream() })
	body := idleReader{r: q.resp.Body, t: q.idle, d: q.g.idleTimeout}
	if q.kind == transcribeEndpoint || q.resp.StatusCode/100 != 2 {
		raw, err := io.ReadAll(io.LimitReader(body, maxUpstreamBody+1))
		if err != nil || len(raw) > maxUpstreamBody {
			q.outcome = outcomeCut
			return errf(CodeUpstreamError, 0, "audio engine response was incomplete or too large")
		}
		var measured struct {
			Duration *float64 `json:"duration"`
		}
		if (q.audio.responseFormat == "" || q.audio.responseFormat == "json" || q.audio.responseFormat == "verbose_json") && json.Unmarshal(raw, &measured) == nil && measured.Duration != nil && *measured.Duration >= 0 && !math.IsNaN(*measured.Duration) && !math.IsInf(*measured.Duration, 0) {
			q.audio.duration = measured.Duration
		}
		if q.resp.StatusCode/100 != 2 {
			q.outcome = outcomeEngineErr
			msg, _ := upstreamMessage(bytes.NewReader(raw))
			if q.resp.StatusCode == 400 || q.resp.StatusCode == 422 {
				return errf(CodeInvalidRequest, 0, "the host's audio engine rejected this request (HTTP %d): %s", q.resp.StatusCode, msg)
			}
			return errf(CodeUpstreamError, 0, "audio engine returned HTTP %d: %s", q.resp.StatusCode, msg)
		}
		if q.audio.responseFormat == "" || q.audio.responseFormat == "json" {
			var result struct {
				Text *string `json:"text"`
			}
			if json.Unmarshal(raw, &result) != nil || result.Text == nil {
				q.outcome = outcomeEngineErr
				return errf(CodeUpstreamError, 0, "audio engine returned invalid transcription JSON")
			}
			raw, _ = json.Marshal(result)
			q.resp.Header.Set("Content-Type", "application/json")
			q.resp.Header.Set("Content-Length", strconv.Itoa(len(raw)))
		}
		q.audioHead()
		q.markTTFT()
		if _, err := q.w.Write(raw); err != nil {
			q.outcome = outcomeCut
			return errf(CodeClientClosed, 0, "client went away")
		}
		return nil
	}
	q.audioHead()
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			q.markTTFT()
			q.armWrite()
			if _, werr := q.w.Write(buf[:n]); werr != nil {
				q.outcome = outcomeCut
				return errf(CodeClientClosed, 0, "client went away")
			}
			if ferr := q.rc.Flush(); ferr != nil {
				q.outcome = outcomeCut
				return errf(CodeClientClosed, 0, "client went away")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			q.outcome = outcomeCut
			return q.upstreamErr(err)
		}
	}
}
func (q *request) audioHead() {
	for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Length"} {
		if v := q.resp.Header.Get(name); v != "" {
			q.w.Header().Set(name, v)
		}
	}
	q.writeHeader(q.resp.StatusCode)
}

// finish calls this exactly once, alongside the existing RPM/concurrency settlement.
func (q *request) settleAudio() {
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := q.g.lim.now()
	st.prune(now)
	st.audioReserved -= q.adm.audioSeconds
	st.speechReserved -= q.adm.speechChars
	if !q.audio.dispatched.Load() {
		return
	}
	q.ev.TS = now // charge and replay belong to the UTC day the call settles on.
	if q.kind == speechEndpoint {
		q.ev.Characters = q.adm.speechChars
		st.speechChars += q.ev.Characters
		return
	}
	q.ev.Seconds = q.adm.audioSeconds
	q.ev.SecondsEstimated = !q.audio.measured
	if q.audio.duration != nil && q.outcome != outcomeCut && q.r.Context().Err() == nil {
		q.ev.Seconds = *q.audio.duration
		q.ev.SecondsEstimated = false
	}
	q.ev.OverrunSeconds = math.Max(0, q.ev.Seconds-q.ev.ReservedSeconds)
	st.audioSeconds += q.ev.Seconds
}

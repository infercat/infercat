// Package speech serves one resident Kokoro model; inference is serialized, never queued here.
package speech

import (
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const SampleRate = 24000

// Pinned model.onnx speaker_names metadata, in native speaker-ID order.
//
//go:embed voices.txt
var voiceNames string
var Voices = strings.Fields(voiceNames)

func inputWeight(text string) (n int) {
	for _, r := range text {
		switch {
		case unicode.IsDigit(r):
			n += 8 // spoken number expansion
		case r < 128:
			n++
		default:
			n += 4 // Han and other scripts
		}
	}
	return
}

type Generate func(context.Context, string, int, float32, func([]float32) error) error

type Server struct {
	Generate   Generate
	mu         sync.Mutex
	batchStart time.Time
	observed   time.Duration
}

func (s *Server) startOrRetry() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batchStart.IsZero() {
		s.batchStart = time.Now()
		s.observed = 0
		return 0
	}
	if s.observed == 0 {
		return 3
	} // Short first retry; later attempts use the observed batch hint.
	return min(30, max(1, int(math.Ceil(max(s.observed, time.Since(s.batchStart)).Seconds()))))
}

func (s *Server) finishBatch(release bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observed = max(s.observed, time.Since(s.batchStart))
	s.batchStart = time.Now()
	if release {
		s.batchStart = time.Time{}
	}
}

func New(generate Generate) http.Handler {
	s := &Server{Generate: generate}
	m := http.NewServeMux()
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ready") })
	m.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"kokoro","object":"model","owned_by":"local"}]}`)
	})
	m.HandleFunc("POST /v1/audio/speech", s.speech)
	return m
}

type request struct {
	Model  string   `json:"model"`
	Input  string   `json:"input"`
	Voice  string   `json:"voice"`
	Format string   `json:"response_format"`
	Speed  *float32 `json:"speed"`
}

func reject(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": detail, "type": "invalid_request_error"}})
}

func (s *Server) speech(w http.ResponseWriter, r *http.Request) {
	if typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || typ != "application/json" {
		reject(w, 415, "Content-Type must be application/json")
		return
	}
	var in request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		detail := err.Error()
		if name, ok := strings.CutPrefix(detail, "json: unknown field "); ok {
			field, _ := strconv.Unquote(name)
			if chars := []rune(field); len(chars) > 64 {
				field = string(chars[:63]) + "…"
			}
			detail = "json: unknown field " + strconv.Quote(field)
		}
		reject(w, 400, "speech JSON: "+detail)
		return
	}
	if d.Decode(new(any)) != io.EOF {
		reject(w, 400, "speech JSON has trailing content")
		return
	}
	if !utf8.ValidString(in.Input) || strings.TrimSpace(in.Input) == "" || strings.ContainsRune(in.Input, 0) {
		reject(w, 400, "input must contain text without NUL")
		return
	}
	if in.Model != "" && in.Model != "kokoro" || in.Format != "" && in.Format != "wav" && in.Format != "pcm" {
		reject(w, 400, "model is kokoro; response_format must be wav or pcm")
		return
	}
	speed := float32(1)
	if in.Speed != nil {
		speed = *in.Speed
	}
	if speed < .5 || speed > 2 {
		reject(w, 400, "speed must be between 0.5 and 2")
		return
	}
	// Use the conservative measured speed curve, not linear compression above 1.
	limit := 600 * min(speed, 1+.3*(speed-1))
	normalized := normalizeHan(in.Input)
	if float32(max(inputWeight(in.Input), inputWeight(normalized))) > limit {
		reject(w, 400, fmt.Sprintf("shorten speech input: limit %d weighted units (ASCII 1, other scripts 4, digits 8), planned for at most 90 seconds", int(limit)))
		return
	}
	if in.Voice == "" {
		in.Voice = "af_maple"
		if IsChinese(in.Input) {
			in.Voice = "zf_001"
		}
	}
	voice := slices.Index(Voices, in.Voice)
	if voice < 0 {
		reject(w, 400, "voice must be one of: "+strings.Join(Voices, ", "))
		return
	}
	if retry := s.startOrRetry(); retry != 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		reject(w, 429, "speech member is busy")
		return
	}
	defer s.finishBatch(true)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	rc := http.NewResponseController(w)
	started, samples := false, 0
	err := s.Generate(ctx, normalized, voice, speed, func(chunk []float32) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(chunk) == 0 {
			return nil
		}
		samples += len(chunk)
		if samples > 120*SampleRate {
			return errors.New("speech exceeds 120 seconds")
		}
		if err := rc.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if !started {
			started = true
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "audio/pcm")
			if in.Format != "pcm" {
				w.Header().Set("Content-Type", "audio/wav")
				if _, err := w.Write(waveHeader()); err != nil {
					return err
				}
			}
		}
		pcm := make([]byte, 2*len(chunk))
		for i, sample := range chunk {
			v := math.Max(-1, math.Min(1, float64(sample)))
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(math.Round(v*32767))))
		}
		if _, err := w.Write(pcm); err != nil {
			return err
		}
		err := rc.Flush()
		s.finishBatch(false)
		return err
	})
	if err != nil || !started {
		if !started {
			reject(w, 500, "speech generation failed")
		} else {
			// Never label a partial generation successful: abort the HTTP stream.
			log.Print("speech stream interrupted")
			panic(http.ErrAbortHandler)
		}
	}
}

func waveHeader() []byte {
	// Unseekable RIFF/WAVE stream: the two unknown lengths use the conventional
	// all-ones sentinel. The connection terminates the mono PCM16 payload.
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], math.MaxUint32)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], math.MaxUint32)
	return b
}

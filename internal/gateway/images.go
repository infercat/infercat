package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/usage"
)

type imageRequest struct {
	runID                       string
	definitiveFailure, measured bool
	charged                     float64
}
type imageOffer struct {
	Model         string `json:"model"`
	RetentionDays int    `json:"retention_days"`
	QueueCap      int    `json:"queue_cap"`
	Queued        int    `json:"queued"`
}

func (g *Gateway) imageOffer(key *keys.Key) *imageOffer {
	d := g.router.route(string(imagesEndpoint))
	if d == nil || !d.Up.Info().Health.OK {
		return nil
	}
	model := d.model
	if model == "" {
		for _, v := range d.Up.Info().Models {
			if v != "" {
				model = v
				break
			}
		}
	}
	if model == "" || !allowsModel(key, g.cfg.ModelsPinned, model) {
		return nil
	}
	queued := 0
	if g.runs != nil {
		rows, _ := g.runs.Store.List(key.ID)
		for _, r := range rows {
			if r.Kind == "image" && r.State == runstate.Queued {
				queued++
			}
		}
	}
	return &imageOffer{model, int(runstate.Retention / (24 * time.Hour)), keys.ImageDefaults(key.Limits).MaxQueuedImages, queued}
}
func isImageRoute(path string) bool {
	return path == string(imagesEndpoint) || path == "/v1/images/jobs" || strings.HasPrefix(path, "/v1/images/outputs/")
}

type imageJob struct {
	runstate.Run
	Position int `json:"position"`
}

func (q *request) imageJobs() ([]imageJob, error) {
	rows, e := q.g.runs.Store.List(q.key.ID)
	if e != nil {
		return nil, e
	}
	out := []imageJob{}
	for _, v := range rows {
		if v.Kind != "image" {
			continue
		}
		r, e := q.g.runs.Store.Get(q.key.ID, v.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, imageJob{r, q.g.runs.Position("image", r.ID)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}
func (q *request) imageRoute() {
	if q.g.runs == nil {
		q.fail(errf(CodeNotFound, 0, "images unavailable"))
		return
	}
	q.w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(q.r.URL.Path, "/v1/images/outputs/") {
		id := strings.TrimPrefix(q.r.URL.Path, "/v1/images/outputs/")
		if q.r.Method == http.MethodDelete {
			if e := q.g.runs.Store.DiscardImage(q.key.ID, id); e != nil {
				q.fail(runError(e))
				return
			}
			q.writeJSON(map[string]bool{"discarded": true})
			return
		}
		if q.r.Method != http.MethodGet {
			q.fail(errf(CodeNotFound, 0, "output not found"))
			return
		}
		raw, o, e := q.g.runs.Store.ReadImage(q.key.ID, id)
		if e != nil {
			q.fail(runError(e))
			return
		}
		q.w.Header().Set("Content-Type", o.MIME)
		q.w.Header().Set("X-Content-Type-Options", "nosniff")
		if q.r.URL.Query().Get("download") == "1" {
			ext := "png"
			if o.MIME == "image/jpeg" {
				ext = "jpg"
			}
			q.w.Header().Set("Content-Disposition", `attachment; filename="image.`+ext+`"`)
		}
		q.writeHeader(200)
		q.armWrite()
		_, _ = q.w.Write(raw)
		return
	}
	if q.r.Method == http.MethodGet && q.r.URL.Path == "/v1/images/jobs" {
		rows, e := q.imageJobs()
		if e != nil {
			q.fail(runError(e))
			return
		}
		q.writeJSON(map[string]any{"jobs": rows})
		return
	}
	if q.r.Method != http.MethodPost {
		q.fail(errf(CodeNotFound, 0, "image route not found"))
		return
	}
	offer := q.g.imageOffer(q.key)
	if offer == nil {
		q.fail(errf(CodeNotFound, 0, "this key has no image model"))
		return
	}
	q.buffered = true
	if q.g.bodies.Add(1) > runstate.MaxLiveHost {
		q.fail(errf(CodeConcurrencyLimited, 1, "image submissions busy"))
		return
	}
	var in struct {
		ClientRequestID string   `json:"client_request_id"`
		Prompts         []string `json:"prompts"`
		Prompt          string   `json:"prompt"`
		Conversation    string   `json:"conversation"`
		N               int      `json:"n"`
		Size            string   `json:"size"`
		Model           string   `json:"model"`
		ResponseFormat  string   `json:"response_format"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(q.w, q.r.Body, runstate.MaxInput))
	err := dec.Decode(&in)
	if err != nil || dec.Decode(new(any)) != io.EOF {
		q.fail(errf(CodeInvalidRequest, 0, "expected one bounded image request"))
		return
	}
	_ = q.rc.SetReadDeadline(time.Time{})
	syncCall := q.r.URL.Path == string(imagesEndpoint)
	if syncCall {
		if in.N == 0 {
			in.N = 1
		}
		if in.N < 1 || in.N > runstate.MaxLiveKey || in.Prompt == "" || (in.Size != "" && in.Size != "1024x1024") || (in.ResponseFormat != "" && in.ResponseFormat != "b64_json") {
			q.fail(errf(CodeInvalidRequest, 0, "images require a prompt, n=1..16, size 1024x1024 and b64_json"))
			return
		}
		in.Prompts = make([]string, in.N)
		for i := range in.Prompts {
			in.Prompts[i] = in.Prompt
		}
	}
	if in.Model != "" && in.Model != offer.Model {
		q.fail(errf(CodeModelNotAllowed, 0, "image model is not shared"))
		return
	}
	inputs := make([]json.RawMessage, len(in.Prompts))
	for i, p := range in.Prompts {
		inputs[i], _ = json.Marshal(runstate.ImageInput{Prompt: p, Conversation: in.Conversation, ClientRequestID: in.ClientRequestID})
	}
	priority := "interactive"
	if len(inputs) > 1 {
		priority = "planted"
	}
	rows, err := q.g.runs.SubmitBatch(q.key.ID, "image", priority, inputs)
	if err != nil {
		e := runError(err)
		if errors.Is(err, runstate.ErrQueueLimit) {
			e.Limit = offer.QueueCap
			e.InFlight = offer.Queued
		}
		q.fail(e)
		return
	}
	if !syncCall {
		q.w.Header().Set("Content-Type", "application/json")
		q.writeHeader(http.StatusAccepted)
		_ = json.NewEncoder(q.w).Encode(map[string]any{"jobs": rows})
		return
	}
	// Losing this HTTP waiter never cancels durable jobs and never causes replay.
	data := []map[string]string{}
	for _, r := range rows {
		tick := time.NewTicker(100 * time.Millisecond)
		for {
			v, e := q.g.runs.Store.Get(q.key.ID, r.ID)
			if e != nil {
				tick.Stop()
				q.fail(runError(e))
				return
			}
			if v.State == runstate.Done {
				raw, _, e := q.g.runs.Store.ReadImage(q.key.ID, r.ID)
				tick.Stop()
				if e != nil {
					q.fail(runError(e))
					return
				}
				data = append(data, map[string]string{"b64_json": base64.StdEncoding.EncodeToString(raw)})
				break
			}
			if v.State == runstate.Failed || v.State == runstate.Cancelled {
				tick.Stop()
				code := CodeUpstreamError
				if len(v.Attempts) > 0 && v.Attempts[len(v.Attempts)-1].Usage.Code == string(CodeImageBudgetExhausted) {
					code = CodeImageBudgetExhausted
				}
				retry := 0
				if code == CodeImageBudgetExhausted {
					retry = secondsUntil(q.g.lim.now().UTC().Truncate(24*time.Hour).Add(24*time.Hour), q.g.lim.now())
				}
				q.fail(errf(code, retry, "image run %s ended: %s", v.ID, v.Reason))
				return
			}
			select {
			case <-q.r.Context().Done():
				tick.Stop()
				return
			case <-q.g.runs.Done():
				tick.Stop()
				return
			case <-tick.C:
			}
		}
	}
	q.writeJSON(map[string]any{"created": time.Now().Unix(), "data": data})
}
func (q *request) proxyImage(rid string, input json.RawMessage) {
	q.kind = imagesEndpoint
	q.image = &imageRequest{runID: rid}
	q.ev.Kind = "image"
	q.ev.Meters = []usage.Meter{{Class: "images", Unit: "images"}}
	var in runstate.ImageInput
	if runstate.ValidateImage(input) != nil || json.Unmarshal(input, &in) != nil {
		q.fail(runError(runstate.ErrInvalid))
		return
	}
	offer := q.g.imageOffer(q.key)
	if offer == nil {
		q.fail(errf(CodeUpstreamDown, 1, "image engine unavailable"))
		return
	}
	if q.g.audioHistoryErr != nil {
		q.fail(errf(CodeUpstreamDown, 1, "resource usage history unavailable"))
		return
	}
	if e := q.resolveDestination(offer.Model); e != nil {
		q.fail(e)
		return
	}
	q.ev.Model = offer.Model
	for _, stage := range []func() *gwError{q.admitImageKey, q.reserveImage, q.acquireSlot} {
		if e := stage(); e != nil {
			q.fail(e)
			return
		}
	}
	body, _ := json.Marshal(map[string]any{"model": offer.Model, "prompt": in.Prompt, "n": 1, "size": "1024x1024", "response_format": "b64_json"})
	ctx, cancel := context.WithCancel(q.r.Context())
	q.cancelUpstream = cancel
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { q.dispatched.Store(true) }})
	resp, err := q.destination.Images.ImageDo(ctx, body)
	if err != nil {
		q.outcome = outcomeEngineErr
		q.fail(q.upstreamErr(err))
		return
	}
	q.resp = resp
	q.idle = time.AfterFunc(q.g.idleTimeout, cancel)
	raw, err := io.ReadAll(io.LimitReader(idleReader{r: resp.Body, t: q.idle, d: q.g.idleTimeout}, 12<<20))
	if err != nil || len(raw) >= 12<<20 {
		q.outcome = outcomeCut
		q.fail(errf(CodeUpstreamError, 0, "image response incomplete or too large"))
		return
	}
	var result struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	parseErr := json.Unmarshal(raw, &result)
	q.image.definitiveFailure = resp.StatusCode/100 != 2 && len(result.Data) == 0
	if parseErr != nil || len(result.Data) != 1 || result.Data[0].Base64 == "" {
		q.outcome = outcomeEngineErr
		detail := "expected one b64_json image; URL-only or malformed image results are unsupported"
		if resp.StatusCode/100 != 2 {
			detail = snippet(bytes.NewReader(raw))
		}
		q.fail(errf(CodeUpstreamError, 0, "image engine HTTP %d: %s", resp.StatusCode, detail))
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Data[0].Base64)
	if err != nil || len(decoded) > runstate.MaxImage {
		q.outcome = outcomeEngineErr
		q.fail(errf(CodeUpstreamError, 0, "image output exceeds 8 MiB or has invalid base64"))
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
		q.outcome = outcomeEngineErr
		q.fail(errf(CodeUpstreamError, 0, "engine output is not a bounded PNG or JPEG"))
		return
	}
	if _, _, err = image.Decode(bytes.NewReader(decoded)); err != nil {
		q.outcome = outcomeEngineErr
		q.fail(errf(CodeUpstreamError, 0, "image output is incomplete"))
		return
	}
	q.image.measured = true
	meta, err := q.g.runs.Store.PutImage(q.key.ID, rid, decoded, "image/"+format, cfg.Width, cfg.Height)
	if err != nil {
		q.outcome = outcomeEngineErr
		q.fail(runError(err))
		return
	}
	q.outcome = outcomeServed
	q.w.Header().Set("Content-Type", "application/json")
	q.writeHeader(200)
	_, _ = q.w.Write(meta)
}
func (q *request) reserveImage() *gwError {
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := q.g.lim.now()
	st.prune(now)
	m := st.meter("images")
	limit := keys.ImageDefaults(q.key.Limits).DailyImages
	if limit > 0 && m.today+m.reserved+1 > float64(limit) {
		return errf(CodeImageBudgetExhausted, secondsUntil(st.day.Add(24*time.Hour), now), "daily image budget exhausted")
	}
	m.reserved++
	q.adm.images = 1
	return nil
}
func (q *request) settleImage() {
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := q.g.lim.now()
	st.prune(now)
	m := st.meter("images")
	m.reserved -= float64(q.adm.images)
	if (q.dispatched.Load() || q.resp != nil) && !q.image.definitiveFailure {
		q.image.charged = 1
		m.today++
	}
	measured := 0.
	if q.image.measured {
		measured = 1
	}
	q.ev.Meters = []usage.Meter{{Class: "images", Unit: "images", Measured: measured, Charged: q.image.charged}}
	q.ev.SettledAt = now
}

func (q *request) admitImageKey() *gwError {
	a, e := q.g.lim.admitResource(q.key.ID, q.key.Limits, true)
	if e == nil {
		q.adm = a
	}
	return e
}

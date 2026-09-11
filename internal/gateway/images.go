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
	"net"
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
	failure                     json.RawMessage
	runID                       string
	definitiveFailure, measured bool
	suspect                     bool
	oversized                   bool
	charged                     float64
	elapsed                     time.Duration
}
type imageOffer struct {
	Model         string     `json:"model"`
	RetentionDays int        `json:"retention_days"`
	QueueCap      int        `json:"queue_cap"`
	Queued        int        `json:"queued"`
	RetryAt       *time.Time `json:"retry_at,omitempty"`
}

// ImageQueueCap is the effective image limit shown by the CLI and /me.
func ImageQueueCap(limits keys.Limits) int {
	cap := keys.ImageDefaults(limits).MaxQueuedImages
	if cap < 0 || cap > runstate.MaxLiveKey {
		return runstate.MaxLiveKey
	}
	return cap
}
func (g *Gateway) imageOffer(key *keys.Key) (*imageOffer, *gwError) {
	d := g.router.route(string(imagesEndpoint))
	missing := errf(CodeNotFound, 0, "this key has no image model")
	if d == nil {
		return nil, missing
	}
	info := d.Up.Info()
	model := d.model
	if model == "" {
		for _, id := range info.Models {
			if id != "" {
				model = id
				break
			}
		}
	}
	if (model == "" && len(key.Limits.Models) > 0) || (model != "" && !allowsModel(key, nil, model)) {
		return nil, missing
	}
	member, managed := g.cfg.Managed["images"]
	if managed && !member.Offered() || !managed && !info.Health.OK {
		return nil, errf(CodeUpstreamDown, 3, "image engine unavailable")
	}
	if model == "" {
		return nil, missing
	}
	queued := 0
	if g.runs != nil {
		rows, _ := g.runs.Store.List(key.ID)
		for _, row := range rows {
			if row.Kind == "image" && row.State == runstate.Queued {
				queued++
			}
		}
	}
	offer := &imageOffer{Model: model, RetentionDays: int(runstate.Retention / (24 * time.Hour)), QueueCap: ImageQueueCap(key.Limits), Queued: queued}
	if next := d.imageRetryAt.Load(); next > time.Now().UnixNano() && d.imageAbandons.Load() >= 2 {
		at := time.Unix(0, next).UTC()
		offer.RetryAt = &at
	}
	return offer, nil
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
	positions := map[string]int{}
	all, e := q.g.runs.Store.List("")
	if e != nil {
		return nil, e
	}
	var queued []runstate.Run
	for _, v := range all {
		if v.Kind == "image" && v.State == runstate.Queued {
			if r, e := q.g.runs.Store.Get(v.KeyID, v.ID); e == nil {
				queued = append(queued, r)
			}
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		a, b := queued[i], queued[j]
		if a.Priority != b.Priority {
			return a.Priority == "interactive"
		}
		if a.Created.Equal(b.Created) {
			return a.ID < b.ID
		}
		return a.Created.Before(b.Created)
	})
	for i, r := range queued {
		positions[r.ID] = i + 1
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
		out = append(out, imageJob{r, positions[r.ID]})
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
	if (q.r.Method == http.MethodGet && !strings.HasPrefix(q.r.URL.Path, "/v1/images/outputs/")) || q.r.Method == http.MethodDelete {
		q.kind = endpoint(q.r.URL.Path)
		if e := q.admitImageHTTP(); e != nil {
			q.fail(e)
			return
		}
		q.outcome = outcomeServed // The admitted list/read itself spends RPM, even if the output is gone.
	}
	if q.r.Method == http.MethodPost {
		q.kind = endpoint(q.r.URL.Path)
		if e := q.admitImageHTTP(); e != nil {
			q.fail(e)
			return
		}
	}
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
		q.kind = endpoint(q.r.URL.Path)
		if e := q.admitImageRead(); e != nil {
			q.fail(e)
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
	offer, unavailable := q.g.imageOffer(q.key)
	if unavailable != nil {
		q.fail(unavailable)
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
		if in.N < 1 || in.Prompt == "" || (in.Size != "" && in.Size != "1024x1024") || (in.ResponseFormat != "" && in.ResponseFormat != "b64_json") {
			q.fail(errf(CodeInvalidRequest, 0, "images require a prompt, n=1..16, size 1024x1024 and b64_json"))
			return
		}
		if in.N > runstate.MaxLiveKey {
			e := runError(runstate.ErrQueueLimit)
			e.Limit, e.InFlight = offer.QueueCap, offer.Queued
			q.fail(e)
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
	q.outcome = outcomeServed
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
				message := "image run " + v.ID + " ended: " + v.Reason
				retry := 0
				if len(v.Attempts) > 0 {
					a := v.Attempts[len(v.Attempts)-1]
					if _, ok := codeTable[Code(a.Usage.Code)]; ok {
						code = Code(a.Usage.Code)
					}
					var detail errorBody
					if json.Unmarshal(a.Output, &detail) == nil && detail.Error.Message != "" {
						message = detail.Error.Message
						retry = detail.Error.RetryAfter
					}
				}
				if code == CodeUpstreamDown {
					retry = 3
				}
				if code == CodeImageBudgetExhausted && retry == 0 {
					retry = secondsUntil(q.g.lim.now().UTC().Truncate(24*time.Hour).Add(24*time.Hour), q.g.lim.now())
				}

				q.fail(errf(code, retry, "%s", message))
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
	if q.g.cfg.LogPrompts {
		q.ev.Prompt = in.Prompt
	}
	if e := q.prepareManaged(); e != nil {
		q.fail(e)
		return
	}
	offer, unavailable := q.g.imageOffer(q.key)
	d := q.g.router.route(string(imagesEndpoint))
	if d != nil && unavailable != nil && unavailable.Code == CodeUpstreamDown {
		// One recovery window for this outage; the admitted job keeps its hold.
		d.imageProbeAfter.CompareAndSwap(0, time.Now().UnixNano())
	}
	if d != nil && d.imageProbeAfter.Load() != 0 {
		after := d.imageProbeAfter.Load()
		limit := q.g.imageRecoveryTimeout
		if limit <= 0 {
			limit = 60 * time.Second
		}
		deadline := time.NewTimer(max(0, limit-time.Since(time.Unix(0, after))))
		defer deadline.Stop()
		tick := time.NewTicker(func() time.Duration {
			if q.g.imageProbeEvery > 0 {
				return q.g.imageProbeEvery
			}
			return 3 * time.Second
		}())
		defer tick.Stop()
		for {
			info := d.Up.Info()
			if info.Health.OK && info.ProbedAt.UnixNano() > after {
				d.imageProbeAfter.CompareAndSwap(after, 0)
				break
			}
			select {
			case <-q.r.Context().Done():
				q.fail(errf(CodeUpstreamDown, 0, "image interrupted before dispatch"))
				return
			case <-deadline.C:
				q.g.logf("image engine recovery exceeded %s", limit)
				q.fail(errf(CodeUpstreamDown, 3, "image engine did not recover in time"))
				return
			case <-tick.C:
				probe, stop := context.WithTimeout(q.r.Context(), min(3*time.Second, time.Until(time.Unix(0, after).Add(limit))))
				_ = d.Images.Refresh(probe)
				stop()
			}
		}
		offer, unavailable = q.g.imageOffer(q.key)
	}
	if unavailable != nil {
		q.fail(unavailable)
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
	if d.imageAbandons.Load() >= 2 {
		wait := time.NewTimer(max(0, time.Until(time.Unix(0, d.imageRetryAt.Load()))))
		defer wait.Stop()
		select {
		case <-q.r.Context().Done():
			q.fail(errf(CodeUpstreamDown, 0, "image interrupted before dispatch"))
			return
		case <-wait.C:
		}
		q.image.suspect = true
		if e := q.checkHealth(); e != nil {
			q.fail(e)
			return
		}
	}
	for _, stage := range []func() *gwError{q.admitImageKey, q.reserveImage, q.acquireSlot} {
		if e := stage(); e != nil {
			q.fail(e)
			return
		}
	}
	body, _ := json.Marshal(map[string]any{"model": offer.Model, "prompt": in.Prompt, "n": 1, "size": "1024x1024", "response_format": "b64_json"})
	ctx, cancel := context.WithCancel(q.r.Context())
	q.cancelUpstream = cancel
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(i httptrace.WroteRequestInfo) { q.dispatched.Store(i.Err == nil) }})
	started := time.Now()
	resp, err := q.destination.Images.ImageDo(ctx, body)
	if err != nil {
		q.outcome = outcomeEngineErr
		q.fail(q.imageTransportError(err))
		return
	}
	q.resp = resp
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
	q.image.elapsed = time.Since(started)
	if len(raw) >= 12<<20 {
		q.image.oversized = true
		q.g.logf("image engine response reached 12 MiB; released and counted toward suspect state")
		q.image.definitiveFailure = true
		q.outcome = outcomeCut
		q.fail(errf(CodeUpstreamError, 0, "image response incomplete or too large"))
		return
	}
	if err != nil {
		q.outcome = outcomeCut
		q.fail(q.imageTransportError(err))
		return
	}
	var result struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	parseErr := json.Unmarshal(raw, &result)
	q.image.definitiveFailure = true
	for _, candidate := range result.Data {
		if candidate.Base64 != "" {
			q.image.definitiveFailure = false
		}
	}
	if parseErr != nil || len(result.Data) != 1 || result.Data[0].Base64 == "" {
		q.outcome = outcomeEngineErr
		detail := "expected one b64_json image; URL-only or malformed image results are unsupported"
		if resp.StatusCode/100 != 2 {
			detail = http.StatusText(resp.StatusCode)
		}
		failure := errf(CodeUpstreamError, 0, "image engine HTTP %d: %s", resp.StatusCode, detail)
		q.fail(failure)
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
	meta, err := q.g.runs.Store.PutImage(q.key.ID, rid, decoded, "image/"+format, cfg.Width, cfg.Height)
	if err != nil {
		q.outcome = outcomeEngineErr
		q.image.definitiveFailure = true
		q.g.logf("image storage failed: %v", err)
		q.fail(errf(CodeStorageFailed, 0, "the host could not store this image; it did not count"))
		return
	}
	q.image.measured = true
	q.outcome = outcomeServed
	q.w.Header().Set("Content-Type", "application/json")
	q.writeHeader(200)
	_, _ = q.w.Write(meta)
}
func (g *Gateway) prepareImageBatch(ctx context.Context, key string) (runstate.BatchAdmission, error) {
	all, err := g.store.List(ctx)
	if err != nil {
		return runstate.BatchAdmission{}, err
	}
	for _, k := range all {
		if k.ID != key {
			continue
		}
		if k.Status != keys.Active {
			code := CodeKeyPaused
			if k.Status == keys.Revoked {
				code = CodeKeyRevoked
			}
			return runstate.BatchAdmission{}, errf(code, 0, "key is not active")
		}
		if g.audioHistoryErr != nil {
			return runstate.BatchAdmission{}, errf(CodeUpstreamDown, 1, "resource usage history unavailable")
		}
		if _, e := g.imageOffer(k); e != nil {
			return runstate.BatchAdmission{}, e
		}
		limits := keys.ImageDefaults(k.Limits)
		return runstate.BatchAdmission{QueueLimit: ImageQueueCap(limits), Reserve: func(rows []runstate.Run) (func(), error) {
			st := g.lim.state(key)
			st.mu.Lock()
			now := g.lim.now()
			st.prune(now)
			meter := st.meter("images")
			if limits.DailyImages > 0 && meter.today+meter.reserved+float64(len(rows)) > float64(limits.DailyImages) {
				st.mu.Unlock()
				return nil, errf(CodeImageBudgetExhausted, secondsUntil(now.UTC().Truncate(24*time.Hour).Add(24*time.Hour), now), "daily image budget exhausted")
			}
			if st.imageHolds == nil {
				st.imageHolds = map[string]bool{}
			}
			for _, r := range rows {
				st.imageHolds[r.ID] = true
			}
			meter.reserved += float64(len(rows))
			st.mu.Unlock()
			return func() {
				for _, r := range rows {
					g.releaseImageReservation(key, r.ID)
				}
			}, nil
		}}, nil
	}
	return runstate.BatchAdmission{}, runstate.ErrNotFound
}
func (g *Gateway) releaseImageReservation(key, rid string) {
	st := g.lim.state(key)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.imageHolds[rid] {
		delete(st.imageHolds, rid)
		st.meter("images").reserved--
	}
}
func (q *request) reserveImage() *gwError {
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.imageHolds[q.image.runID] {
		return errf(CodeInvalidRequest, 0, "image job has no reservation")
	}
	return nil
}
func (q *request) settleImage() {
	q.recordImageResult()
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := q.g.lim.now()
	st.prune(now)
	m := st.meter("images")
	if st.imageHolds[q.image.runID] {
		delete(st.imageHolds, q.image.runID)
		m.reserved--
	}
	if q.dispatched.Load() && !q.image.definitiveFailure && !(q.image.suspect && !q.image.measured) {
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
	if q.adm != nil {
		return nil
	}
	q.adm = &admission{key: q.key.ID, detached: true}
	return nil
}

func (q *request) imageTransportError(err error) *gwError {
	if !q.dispatched.Load() {
		return q.upstreamErr(err)
	}
	q.destination.imageProbeAfter.Store(time.Now().UnixNano())
	q.g.logf("image request abandoned: %v", err)
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return q.imageAbandoned("the engine did not finish in time")
	}
	return q.imageAbandoned("the connection to the engine was lost before the image finished")
}

func (q *request) admitImageHTTP() *gwError {
	a, e := q.g.lim.admitResource(q.key.ID, q.key.Limits, true)
	if e == nil {
		q.adm = a
	}
	return e
}

func (q *request) imageAbandoned(cause string) *gwError {
	charge := "; this one counted"
	if q.image.suspect {
		charge = "; the engine is suspect, so this one did not count"
	}
	return errf(CodeImageAbandoned, 0, "%s%s", cause, charge)
}
func (q *request) recordImageResult() {
	d := q.destination
	if d == nil {
		return
	}
	if q.image.measured {
		d.Images.RecordSuccess(q.image.elapsed)
		if d.imageAbandons.Swap(0) >= 2 {
			q.g.logf("image engine recovered; suspect state cleared")
		}
		d.imageRetryAt.Store(0)
		d.imageBackoff.Store(0)
	} else {
		n := d.imageAbandons.Load()
		counted := q.dispatched.Load() && (!q.image.definitiveFailure || q.image.oversized)
		if counted {
			n = d.imageAbandons.Add(1)
		}
		if n >= 2 && counted {
			base := q.g.imageBackoffBase
			if base <= 0 {
				base = time.Minute
			}
			delay := min(max(base, 2*time.Duration(d.imageBackoff.Load())), 15*base)
			d.imageBackoff.Store(int64(delay))
			d.imageRetryAt.Store(time.Now().Add(delay).UnixNano())
			q.g.logf("image engine suspect after %d failed generations; backoff %s; further charge-eligible failures are released", n, delay)
		}
	}
}

// A read owns its dedicated slot through the request owner's single finish path.
func (q *request) admitImageRead() *gwError {
	st := q.g.lim.state(q.key.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.imageReads >= 4 {
		e := errf(CodeConcurrencyLimited, 1, "too many images loading at once; retry in a second")
		e.Limit, e.InFlight = 4, st.imageReads
		return e
	}
	st.imageReads++
	q.readRelease = func() { st.mu.Lock(); st.imageReads--; st.mu.Unlock() }
	return nil
}

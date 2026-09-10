package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/infercat/infercat/internal/bridge"
)

const previewOrigin = "https://infercat-bridge-preview.yuanping-song.workers.dev"

type previewOptions struct {
	hostDir, bin, model, out, name string
	pid, n                         int
	duration                       time.Duration
	earlyClose                     bool
}

func (o previewOptions) validate(c bridge.Config, h *hostSample) error {
	if o.hostDir == "" || o.pid <= 0 || o.model == "" || o.duration <= 0 || o.duration > 2*time.Minute || o.n < 1 || o.n > 60 || (o.earlyClose && o.n != 1) {
		return errors.New("preview requires a host dir/pid, model, 1–60 clients, and at most two minutes; early-close uses one client")
	}
	if c.Endpoint != previewOrigin || c.Disabled || h.Err != "" || h.Bridge == nil || !h.Bridge.Enabled || !h.Bridge.Connected || h.Bridge.URL != c.URL() {
		return errors.New("host must be connected with its own saved registration on infercat-bridge-preview")
	}
	return nil
}

func previewRun(o previewOptions) (err error) {
	ctx, interrupt := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer interrupt()
	c, err := bridge.Load(o.hostDir)
	if err != nil {
		return err
	}
	if err := o.validate(c, sampleHost(ctx, o.hostDir, o.pid)); err != nil {
		return err
	}
	if o.name == "" {
		o.name = fmt.Sprintf("preview-n%d-%s", o.n, time.Now().UTC().Format("20060102T150405"))
	}
	dir := filepath.Join(o.out, o.name)
	if err := os.MkdirAll(o.out, 0700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	reqW, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer reqW.Close()
	sampW, err := os.OpenFile(filepath.Join(dir, "samples.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer sampW.Close()
	r := &run{name: o.name, model: o.model, hostDir: o.hostDir, reqW: reqW, sampW: sampW}
	id, secret, _, err := mintKey(o.bin, o.hostDir, "load-"+o.name, o.n)
	if err != nil {
		return err
	}
	defer func() {
		if out, e := exec.Command(o.bin, "keys", "revoke", id, "--yes", "--data-dir", o.hostDir).CombinedOutput(); e != nil {
			err = errors.Join(err, fmt.Errorf("revoke benchmark key: %w: %s", e, out))
		}
	}()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = o.n
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sessions := make([]*session, o.n)
	for i := range sessions {
		sessions[i] = &session{friend: i, keyID: id, secret: secret, http: hc, baseURL: c.Endpoint + "/h/" + c.Host, path: "bridge"}
	}
	// Wait for this valid key's asynchronous snapshot; these readiness requests are excluded.
	ready := false
	for range 10 {
		rec, _ := sessions[0].get(ctx, "/v1/models")
		if rec.Status == 200 {
			ready = true
			break
		}
		if rec.Status != 401 {
			return fmt.Errorf("preview readiness: %d %s %s", rec.Status, rec.Code, rec.Err)
		}
		time.Sleep(time.Second)
	}
	if !ready {
		return errors.New("benchmark key snapshot did not become ready")
	}
	// Each measured level starts in a fresh fixed-minute edge window.
	wait := time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute + time.Second))
	log.Printf("%s ready; waiting %s for a clean edge window", o.name, wait.Round(time.Second))
	sleepCtx(ctx, wait)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	before := sampleHost(ctx, o.hostDir, o.pid)
	if err := o.validate(c, before); err != nil {
		return err
	}
	start := time.Now()
	log.Printf("MEASURE START %s %s: clients=%d window=%s", o.name, start.UTC().Format(time.RFC3339Nano), o.n, o.duration)
	loop, stop := context.WithTimeout(ctx, o.duration)
	defer stop()
	grace, cancel := context.WithTimeout(ctx, o.duration+45*time.Second)
	defer cancel()
	stopSampling, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-tick.C:
			}
			h := sampleHost(ctx, o.hostDir, o.pid)
			r.mu.Lock()
			s := sample{TS: time.Now(), Load: r.counters, Host: h}
			r.samples = append(r.samples, s)
			_ = json.NewEncoder(r.sampW).Encode(s)
			r.mu.Unlock()
			if h.Err != "" || h.Bridge == nil || !h.Bridge.Connected || h.Bridge.Since != before.Bridge.Since {
				stop()
			}
		}
	}()
	if o.earlyClose {
		s := sessions[0]
		s.closeAfterToken = true
		rec, _, _ := s.chat(grace, o.model, []msg{{"user", "Repeat ALPHA 500 times."}}, 1024, true)
		r.record(rec)
		s.closeAfterToken = false
		if rec.Status != 200 || !rec.Aborted || rec.TTFTMS == 0 {
			err = errors.New("early-close did not reach a first token")
		} else {
			rec, _, _ = s.chat(grace, o.model, []msg{{"user", "Reply with exactly NEXT_OK."}}, 256, true)
			r.record(rec)
			if rec.Status != 200 || !rec.Done || rec.Err != "" || rec.Code != "" {
				err = fmt.Errorf("request after early-close: %d %s %s", rec.Status, rec.Code, rec.Err)
			}
		}
	} else {
		// At most N requests in the opening burst, then five starts per second globally.
		starts := time.NewTicker(200 * time.Millisecond)
		defer starts.Stop()
		var wg sync.WaitGroup
		for _, s := range sessions {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for first := true; loop.Err() == nil; first = false {
					if !first {
						select {
						case <-loop.Done():
							return
						case <-starts.C:
						}
					}
					if loop.Err() != nil {
						return
					}
					r.mu.Lock()
					r.counters.Active++
					r.mu.Unlock()
					rec, _, retry := s.chat(grace, o.model, []msg{{"user", "Write a short paragraph about a cat watching rain."}}, 256, true)
					r.record(rec)
					r.mu.Lock()
					r.counters.Active--
					r.mu.Unlock()
					if rec.Status == 0 || rec.Code == "bridge_disconnected" {
						stop()
						return
					}
					if rec.Status >= 400 {
						sleepCtx(loop, time.Duration(max(5, retry))*time.Second)
					}
				}
			}()
		}
		<-loop.Done()
		wg.Wait()
		if loop.Err() != context.DeadlineExceeded {
			err = errors.New("measurement stopped early after a transport or host-socket failure")
		}
	}
	close(stopSampling)
	<-sampled
	after := sampleHost(ctx, o.hostDir, o.pid)
	if after.Err != "" || after.Bridge == nil || !after.Bridge.Connected || after.Bridge.Since != before.Bridge.Since {
		err = errors.Join(err, errors.New("host socket continuity changed"))
	}
	events := hostEvents(o.hostDir, start, map[string]bool{id: true})
	f, openErr := os.OpenFile(filepath.Join(dir, "host.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if openErr != nil {
		return errors.Join(err, openErr)
	}
	for _, e := range events {
		_ = json.NewEncoder(f).Encode(e)
	}
	f.Close()
	md := previewSummary(r, before, after, start, time.Now(), o)
	if err != nil {
		md += "\nRun error: " + err.Error() + "\n"
	}
	if e := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(md), 0600); e != nil {
		return errors.Join(err, e)
	}
	log.Printf("MEASURE END %s %s", o.name, time.Now().UTC().Format(time.RFC3339Nano))
	fmt.Print(md)
	return err
}

func previewPercentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	return v[max(0, min(len(v)-1, int(math.Ceil(p*float64(len(v))))-1))]
}

func previewSummary(r *run, before, after *hostSample, start, end time.Time, o previewOptions) string {
	var ttft, done, cpu []float64
	codes := map[string]int{}
	for _, x := range r.recs {
		key := fmt.Sprintf("%d %s", x.Status, x.Code)
		if x.Status == 200 && x.Done && x.Code == "" && x.Err == "" {
			key = "200 done"
		}
		if x.Err != "" {
			key += " " + firstLine(x.Err)
		}
		codes[key]++
		if x.Status == 200 && x.Done && x.Code == "" && x.Err == "" {
			ttft = append(ttft, float64(x.TTFTMS))
			done = append(done, float64(x.TotalMS))
		}
	}
	continuity := after.Err == "" && after.Bridge != nil && after.Bridge.Connected && after.Bridge.Since == before.Bridge.Since
	for _, s := range r.samples {
		if s.Host != nil {
			cpu = append(cpu, s.Host.CPUPercent)
			if s.Host.Err != "" || s.Host.Bridge == nil || !s.Host.Bridge.Connected || s.Host.Bridge.Since != before.Bridge.Since {
				continuity = false
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\nUTC: %s to %s. Clients: %d; requested window: %s; elapsed including drain: %.2f s. Early-close: %v.\n\n", o.name, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), o.n, o.duration, end.Sub(start).Seconds(), o.earlyClose)
	latency := "not available (no completed streams)"
	if len(done) > 0 {
		latency = fmt.Sprintf("TTFT p50/p95: %.0f / %.0f ms. Done p50/p95: %.0f / %.0f ms", previewPercentile(ttft, .5), previewPercentile(ttft, .95), previewPercentile(done, .5), previewPercentile(done, .95))
	}
	cpuText := "not available (no periodic samples)"
	if len(cpu) > 0 {
		cpuText = fmt.Sprintf("p50/p95/max: %.1f / %.1f / %.1f %% (ps, 100%% = one core)", previewPercentile(cpu, .5), previewPercentile(cpu, .95), previewPercentile(cpu, 1))
	}
	fmt.Fprintf(&b, "Completed streams: %d. %s.\n\nOutcomes: %v. Host CPU %s. Host socket unchanged: %v (%d one-second samples plus final state).\n\n", len(done), latency, codes, cpuText, continuity, len(r.samples))
	fmt.Fprintln(&b, "Authenticated key, limits above edge caps. Opening burst up to client count, then at most 5 starts/s; refusals honor Retry-After with a 5 s minimum. Client deadline 45 s. Readiness excluded; clean fixed-minute window. TTFT is first nonempty content or reasoning delta; latency percentiles use completed streams and nearest rank. Raw requests and host samples are retained alongside this report.")
	return b.String()
}

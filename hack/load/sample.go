package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
)

// One line of samples.jsonl per second: the instrument's own counters, the host (admin socket +
// ps), the engine (its /metrics, llama.cpp's /slots, ps) and the relay (varz + top over one ssh
// session that streams a sample a second). Every reader is read-only.
type sample struct {
	TS     time.Time          `json:"ts"`
	Load   loadCounters       `json:"load"`
	Host   *hostSample        `json:"host,omitempty"`
	Engine map[string]float64 `json:"engine,omitempty"`
	Relay  map[string]float64 `json:"relay,omitempty"`
}

type loadCounters struct {
	Active, Done, Errors int
	BytesUp, BytesDown   int64
}

type hostSample struct {
	InFlight, Waiting, Clients, Goroutines, Sessions int
	HeapBytes, Sys                                   uint64
	RSSKB                                            int64
	Err                                              string `json:",omitempty"`
}

// sampleHost reads the host's own /status (ticket 029: queue counts, tunnel clients/sessions, and
// the process block — goroutines, heap, sys — that this ticket asked for and 029 landed). RSS still
// comes from ps because 029's process.rss_bytes is 0 on macOS (no cgo).
func sampleHost(ctx context.Context, dir string, pid int) *hostSample {
	h := &hostSample{}
	st, err := admin.Fetch(ctx, dir)
	if err != nil {
		h.Err = err.Error()
	}
	h.InFlight, h.Waiting, h.Clients, h.Sessions = st.Queue.InFlight, st.Queue.Waiting, st.Tunnel.Clients, len(st.Tunnel.Sessions)
	h.Goroutines, h.HeapBytes, h.Sys = st.Process.Goroutines, st.Process.HeapBytes, st.Process.SysBytes
	h.RSSKB = rssKB(pid)
	return h
}

// rssKB is a process's resident set from ps, in KB; 0 when there is no such process.
func rssKB(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return n
}

// sampleEngine reads the engine's Prometheus text (llamacpp:* or vllm:* scalars; labels dropped,
// histograms skipped) plus, on llama.cpp, /slots — where n_prompt_tokens_cache is the slot cache
// reuse the founder asked about — and the engine's RSS when its pid is local.
func sampleEngine(ctx context.Context, base string, pid int) map[string]float64 {
	m := map[string]float64{}
	if base == "" {
		return m
	}
	if body, err := httpGet(ctx, base+"/metrics"); err == nil {
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, "llamacpp:") && !strings.HasPrefix(line, "vllm:") {
				continue
			}
			name, val, ok := metricLine(line)
			if ok && !strings.Contains(name, "_bucket") && !strings.HasSuffix(name, "_created") {
				m[strings.TrimPrefix(strings.TrimPrefix(name, "llamacpp:"), "vllm:")] = val
			}
		}
	}
	if body, err := httpGet(ctx, base+"/slots"); err == nil {
		var slots []struct {
			Processing bool `json:"is_processing"`
			NPrompt    int  `json:"n_prompt_tokens"`
			NProcessed int  `json:"n_prompt_tokens_processed"`
			NCache     int  `json:"n_prompt_tokens_cache"`
		}
		if json.Unmarshal([]byte(body), &slots) == nil {
			for _, s := range slots {
				if s.Processing {
					m["slots_processing"]++
				}
				m["slot_prompt_tokens"] += float64(s.NPrompt)
				m["slot_prompt_cache"] += float64(s.NCache)
			}
		}
	}
	if kb := rssKB(pid); kb > 0 {
		m["rss_kb"] = float64(kb)
	}
	return m
}

// metricLine parses `name{labels} value`; labels are folded into the name so distinct series stay
// distinct (derp_packets_dropped_reason{reason="x"} → derp_packets_dropped_reason:reason=x).
func metricLine(line string) (string, float64, bool) {
	sp := strings.LastIndexByte(line, ' ')
	if sp < 0 {
		return "", 0, false
	}
	val, err := strconv.ParseFloat(line[sp+1:], 64)
	if err != nil {
		return "", 0, false
	}
	name := strings.NewReplacer("{", ":", "}", "", `"`, "").Replace(line[:sp])
	return name, val, true
}

func httpGet(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var b strings.Builder
	_, err = bufio.NewReader(resp.Body).WriteTo(&b)
	return b.String(), err
}

// relaySampler holds one ssh session to the relay box that prints, once a second, the derper varz
// scalars we care about and the box's CPU line from top. It reads; it never changes anything.
type relaySampler struct {
	cmd    *exec.Cmd
	latest chan map[string]float64
}

const relayScript = `while :; do echo "T $(date +%s)"; curl -s -m 2 http://127.0.0.1:80/debug/varz | grep -E '^(derp_|process_cpu|process_resident|go_goroutines)' | grep -v -E '_bucket|_sum |_count '; top -bn1 | sed -n '3p'; echo E; sleep 1; done`

func startRelaySampler(target string) (*relaySampler, error) {
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target, relayScript)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	rs := &relaySampler{cmd: cmd, latest: make(chan map[string]float64, 1)}
	go func() {
		sc := bufio.NewScanner(out)
		cur := map[string]float64{}
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "T "):
				cur = map[string]float64{}
				cur["t"], _ = strconv.ParseFloat(line[2:], 64)
			case line == "E":
				select {
				case <-rs.latest:
				default:
				}
				rs.latest <- cur
			case strings.HasPrefix(line, "%Cpu"):
				for _, f := range strings.Split(line[strings.IndexByte(line, ':')+1:], ",") {
					if p := strings.Fields(f); len(p) == 2 && p[1] == "id" {
						idle, _ := strconv.ParseFloat(p[0], 64)
						cur["cpu_pct"] = 100 - idle
					}
				}
			default:
				if name, val, ok := metricLine(line); ok {
					cur[name] = val
				}
			}
		}
	}()
	return rs, nil
}

// read returns the newest relay sample, or nil when none arrived since the last read.
func (rs *relaySampler) read() map[string]float64 {
	if rs == nil {
		return nil
	}
	select {
	case m := <-rs.latest:
		return m
	default:
		return nil
	}
}

func (rs *relaySampler) stop() {
	if rs != nil && rs.cmd.Process != nil {
		rs.cmd.Process.Kill()
		rs.cmd.Wait()
	}
}

// relayDropped is the packets the relay dropped between two samples, by reason (derper's
// derp_packets_dropped{reason,kind}; the bytes series is left out so a drop counts once).
func relayDropped(from, to map[string]float64) (total float64, why string) {
	var parts []string
	for k, v := range to {
		if d := v - from[k]; strings.HasPrefix(k, "derp_packets_dropped:") && d > 0 {
			total += d
			parts = append(parts, fmt.Sprintf("%s +%.0f", strings.TrimPrefix(k, "derp_packets_dropped:"), d))
		}
	}
	return total, strings.Join(parts, ", ")
}

func (s hostSample) String() string {
	return fmt.Sprintf("in_flight=%d waiting=%d clients=%d sessions=%d goroutines=%d rss=%dMB", s.InFlight, s.Waiting, s.Clients, s.Sessions, s.Goroutines, s.RSSKB/1024)
}

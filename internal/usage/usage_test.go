package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderAppendsOneJSONObjectPerLine(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		r.Record(ctx, Event{KeyID: "k_1", Endpoint: "/v1/chat/completions", Status: 200, PromptTokens: i})
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lines := readLines(t, filepath.Join(dir, FileName))
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}
	for i, l := range lines {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("line %d is not JSON: %v", i, err)
		}
		if e.KeyID != "k_1" || e.PromptTokens != i {
			t.Errorf("line %d = %+v", i, e)
		}
		if e.TS.IsZero() {
			t.Errorf("line %d has no timestamp", i)
		}
	}
	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("usage.jsonl mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestRecorderReopensAndAppends(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		r, err := NewFileRecorder(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Record(context.Background(), Event{KeyID: "k_1", Status: 200})
		r.Close()
	}
	if got := len(readLines(t, filepath.Join(dir, FileName))); got != 2 {
		t.Errorf("%d lines after a restart, want 2 (append, not truncate)", got)
	}
}

// Record must never block the request path: past the buffer it drops, counts, and warns.
func TestRecorderDropsRatherThanBlocks(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Fill the channel behind the writer's back so the buffer is provably full.
	for i := 0; i < bufferedEvents; i++ {
		select {
		case r.ch <- Event{KeyID: "k_1"}:
		default:
		}
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			r.Record(context.Background(), Event{KeyID: "k_1"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked when the buffer was full")
	}
	if r.Dropped() == 0 {
		t.Error("nothing was counted as dropped")
	}
}

// 005 fix 10i: a Record that races Close (the gateway's last usage event landing after the CLI
// closed the recorder) is dropped and counted, never a send on a closed channel.
func TestRecordAfterCloseDropsAndCounts(t *testing.T) {
	r, err := NewFileRecorder(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Record(context.Background(), Event{KeyID: "k1"})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r.Record(context.Background(), Event{KeyID: "late"})
	if got := r.Dropped(); got != 1 {
		t.Fatalf("Dropped() after a late Record = %d, want 1", got)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// 005 fix 10j: one over-long line (a corrupt write) is skipped and counted; the lines around it
// still aggregate, and a long-but-valid line (a --log-prompts event) still parses.
func TestAggregateSkipsAnOverlongLine(t *testing.T) {
	good := `{"key_id":"k1","status":200,"prompt_tokens":1,"completion_tokens":2}` + "\n"
	long := `{"key_id":"k1","status":200,"prompt":"` + strings.Repeat("p", 100*1024) + `"}` + "\n"
	huge := `{"key_id":"k1","status":200,"prompt":"` + strings.Repeat("x", 9*1024*1024) + `"}` + "\n"
	rep, err := Aggregate(strings.NewReader(good+long+huge+good), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Malformed != 1 || rep.Total.Requests != 3 {
		t.Fatalf("malformed %d, requests %d; want 1 skipped line and 3 requests", rep.Malformed, rep.Total.Requests)
	}
	// No trailing newline on the last line, and a CRLF line, both still count.
	rep, err = Aggregate(strings.NewReader(strings.TrimSuffix(good, "\n")+"\r\n"+strings.TrimSuffix(good, "\n")), Filter{})
	if err != nil || rep.Total.Requests != 2 || rep.Malformed != 0 {
		t.Fatalf("unterminated/CRLF lines: %+v %v", rep, err)
	}
}

func TestAggregatePercentilesAndErrorCodes(t *testing.T) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	base := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	for i, ttft := range []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		enc.Encode(Event{
			TS: base.Add(time.Duration(i) * time.Minute), KeyID: "k_1", Endpoint: "/v1/chat/completions", Status: 200,
			PromptTokens: 10, CompletionTokens: 5, TTFTMS: ttft, TotalMS: ttft * 10,
		})
	}
	// Two polls from a tab left open an hour later, and a failed model call: neither may move the
	// percentiles or "last seen", and the polls are not requests the friend made (009 promise 6).
	enc.Encode(Event{TS: base.Add(time.Hour), KeyID: "k_1", Endpoint: "/me", Status: 200, TTFTMS: 3, TotalMS: 3})
	enc.Encode(Event{TS: base.Add(2 * time.Hour), KeyID: "k_1", Endpoint: "/v1/models", Status: 200, TTFTMS: 4, TotalMS: 4})
	enc.Encode(Event{TS: base.Add(3 * time.Hour), KeyID: "k_1", Endpoint: "/v1/chat/completions", Status: 503, Code: "upstream_down", TTFTMS: 9999, TotalMS: 9999})
	enc.Encode(Event{TS: base, KeyID: "k_2", Endpoint: "/v1/chat/completions", Status: 429, Code: "rate_limited"})
	enc.Encode(Event{TS: base, KeyID: "k_2", Endpoint: "/v1/chat/completions", Status: 429, Code: "rate_limited"})
	enc.Encode(Event{TS: base, KeyID: "k_2", Endpoint: "/v1/chat/completions", Status: 503, Code: "upstream_down"})
	b.WriteString("{ this is not json\n")

	rep, err := Aggregate(strings.NewReader(b.String()), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Malformed != 1 {
		t.Errorf("malformed = %d, want 1 (a truncated line must not fail the whole read)", rep.Malformed)
	}
	if rep.Total.Requests != 16 || rep.Total.Errors != 4 {
		t.Errorf("total = %d requests, %d errors", rep.Total.Requests, rep.Total.Errors)
	}
	if rep.Total.ModelCalls != 14 || rep.Total.AppPolls != 2 {
		t.Errorf("split = %d model calls, %d app polls; want 14 and 2", rep.Total.ModelCalls, rep.Total.AppPolls)
	}
	if got := rep.Total.ErrorsByCode["rate_limited"]; got != 2 {
		t.Errorf("rate_limited = %d, want 2", got)
	}
	if rep.Total.PromptTokens != 100 || rep.Total.CompletionTokens != 50 {
		t.Errorf("tokens = %d/%d", rep.Total.PromptTokens, rep.Total.CompletionTokens)
	}
	// Nearest-rank over 10 samples: p50 is the 5th, p95 is the 10th.
	if rep.Total.TTFTMedianMS != 50 || rep.Total.TTFTP95MS != 100 {
		t.Errorf("ttft p50/p95 = %d/%d, want 50/100", rep.Total.TTFTMedianMS, rep.Total.TTFTP95MS)
	}
	if rep.Total.TotalMedianMS != 500 || rep.Total.TotalP95MS != 1000 {
		t.Errorf("total p50/p95 = %d/%d, want 500/1000", rep.Total.TotalMedianMS, rep.Total.TotalP95MS)
	}
	if len(rep.Keys) != 2 || rep.Keys[0].KeyID != "k_1" || rep.Keys[1].KeyID != "k_2" {
		t.Fatalf("per-key breakdown = %v", rep.Keys)
	}
	if rep.Keys[1].Requests != 3 || rep.Keys[1].ModelCalls != 3 || rep.Keys[1].Errors != 3 {
		t.Errorf("k_2 = %+v", rep.Keys[1])
	}
	// "Last seen" is the last model call, not the last poll and not the failed call three hours later.
	seen := rep.LastSeen()
	if !seen["k_1"].Equal(base.Add(3 * time.Hour)) {
		t.Errorf("last seen k_1 = %v; want the last model call", seen["k_1"])
	}
	if !rep.Keys[0].LastSeen.Equal(base.Add(3 * time.Hour)) {
		t.Errorf("k_1 LastSeen = %v", rep.Keys[0].LastSeen)
	}
}

func TestAggregateFilters(t *testing.T) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	now := time.Now().UTC()
	enc.Encode(Event{TS: now.Add(-48 * time.Hour), KeyID: "k_1", Status: 200, PromptTokens: 1})
	enc.Encode(Event{TS: now.Add(-time.Hour), KeyID: "k_1", Status: 200, PromptTokens: 2})
	enc.Encode(Event{TS: now.Add(-time.Hour), KeyID: "k_2", Status: 200, PromptTokens: 4})

	if rep, _ := Aggregate(strings.NewReader(b.String()), Filter{Since: now.Add(-24 * time.Hour)}); rep.Total.PromptTokens != 6 {
		t.Errorf("since 24h = %d prompt tokens, want 6", rep.Total.PromptTokens)
	}
	if rep, _ := Aggregate(strings.NewReader(b.String()), Filter{KeyID: "k_1"}); rep.Total.PromptTokens != 3 {
		t.Errorf("key filter = %d prompt tokens, want 3", rep.Total.PromptTokens)
	}
	rep, _ := Aggregate(strings.NewReader(b.String()), Filter{KeyID: "k_1", Since: now.Add(-24 * time.Hour)})
	if rep.Total.PromptTokens != 2 || rep.Total.Requests != 1 {
		t.Errorf("both filters = %+v", rep.Total)
	}
}

// `usage` before the first request is a legitimate thing to run.
func TestAggregateFileWithNoLog(t *testing.T) {
	rep, err := AggregateFile(t.TempDir(), Filter{})
	if err != nil {
		t.Fatalf("AggregateFile on a missing log: %v", err)
	}
	if rep.Total.Requests != 0 || len(rep.Keys) != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestPercentileEdges(t *testing.T) {
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("percentile of nothing = %d, want 0", got)
	}
	if got := percentile([]int64{7}, 95); got != 7 {
		t.Errorf("percentile of one sample = %d, want 7", got)
	}
	if got := percentile([]int64{3, 1, 2}, 50); got != 2 {
		t.Errorf("percentile sorts its input: got %d, want 2", got)
	}
}

// A recorded event round-trips through the file and back into the aggregate.
func TestRecordThenAggregate(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Record(context.Background(), Event{KeyID: "k_1", Endpoint: "/v1/chat/completions", Status: 200,
		Stream: true, PromptTokens: 12, CompletionTokens: 34, TTFTMS: 120, TotalMS: 3100})
	r.Close()
	rep, err := AggregateFile(dir, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total.Requests != 1 || rep.Total.CompletionTokens != 34 || rep.Total.TTFTMedianMS != 120 {
		t.Errorf("report = %+v", rep.Total)
	}
}

func readLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

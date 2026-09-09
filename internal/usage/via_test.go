package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestViaAggregatesKeepTotalsAndComputeOwnPercentiles(t *testing.T) {
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	var log strings.Builder
	enc := json.NewEncoder(&log)
	for _, e := range []Event{
		{TS: day, KeyID: "a", Endpoint: "/v1/chat/completions", Status: 200, TTFTMS: 10, TotalMS: 100, PromptTokens: 2},
		{TS: day, KeyID: "a", Via: "bridge", Endpoint: "/v1/chat/completions", Status: 200, TTFTMS: 90, TotalMS: 900, PromptTokens: 3},
		{TS: day, KeyID: "a", Via: "bridge", Endpoint: "/v1/chat/completions", Status: 429, Code: "rate_limited", TTFTMS: 9999},
		{TS: day, KeyID: "a", Via: "direct", Endpoint: "/me", Status: 200, TTFTMS: 1},
		{TS: day.Add(24 * time.Hour), KeyID: "b", Via: "bridge", Endpoint: "/v1/chat/completions", Status: 200, TTFTMS: 50, TotalMS: 500, PromptTokens: 4},
	} {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Aggregate(strings.NewReader(log.String()), Filter{Since: day, Days: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total.Requests != 5 || rep.Total.PromptTokens != 9 || rep.Total.TTFTMedianMS != 50 || rep.Total.TTFTP95MS != 90 {
		t.Fatalf("whole-log totals changed: %+v", rep.Total)
	}
	if rep.ByVia["direct"].Requests != 2 || rep.ByVia["direct"].TTFTMedianMS != 10 || rep.ByVia["bridge"].Requests != 3 || rep.ByVia["bridge"].TTFTMedianMS != 50 {
		t.Fatalf("via buckets: %+v", rep.ByVia)
	}
	for _, by := range []map[string]*Stats{rep.Daily[0].ByVia, rep.Keys[0].ByVia, rep.Daily[0].Keys[0].ByVia} {
		if len(by) != 2 || by["bridge"].Requests != 2 || by["bridge"].ErrorsByCode["rate_limited"] != 1 || by["bridge"].TTFTMedianMS != 90 || by["direct"].TTFTMedianMS != 10 {
			t.Fatalf("day/key split lost samples or refusals: %+v", by)
		}
	}
	filtered, err := Aggregate(strings.NewReader(log.String()), Filter{KeyID: "a", Since: day, Until: day.Add(24 * time.Hour), Days: 2})
	if err != nil || filtered.Total.Requests != 4 || filtered.ByVia["bridge"].TTFTMedianMS != 90 || len(filtered.Daily[1].ByVia) != 0 {
		t.Fatalf("via ignored filter: %+v, %v", filtered, err)
	}
}

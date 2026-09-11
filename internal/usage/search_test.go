package usage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSearchMetersDoNotBecomePollsOrModelLatency(t *testing.T) {
	day := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	rows := []Event{
		{TS: day, KeyID: "a", Via: "bridge", Endpoint: "/v1/chat/completions", Model: "m", Status: 200, TTFTMS: 10, TotalMS: 100, Meters: []Meter{{"tokens", "tokens", 7, 7}}},
		{TS: day.Add(time.Second), KeyID: "a", Via: "bridge", Endpoint: "/me", Status: 200},
		{TS: day.Add(2 * time.Second), SettledAt: day.Add(3 * time.Second), KeyID: "a", Via: "bridge", Kind: "search", Endpoint: "web_search", Status: 200, TotalMS: 99999, Meters: []Meter{{"search", "requests", 1, 1}}},
		{TS: day.Add(-time.Second), SettledAt: day.Add(4 * time.Second), KeyID: "a", Via: "bridge", Kind: "search", Endpoint: "web_search", Status: 502, Code: "upstream_error", TotalMS: 99999, Meters: []Meter{{"search", "requests", 0, 1}}},
	}
	var log strings.Builder
	for _, e := range rows {
		if err := json.NewEncoder(&log).Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Aggregate(strings.NewReader(log.String()), Filter{Since: day, Days: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Stats{&rep.Total, rep.Keys[0], &rep.Daily[0].Total, rep.Daily[0].Keys[0], rep.ByVia["bridge"], rep.Keys[0].ByVia["bridge"], rep.Daily[0].ByVia["bridge"], rep.Daily[0].Keys[0].ByVia["bridge"]} {
		if s.Requests != 2 || s.ModelCalls != 1 || s.AppPolls != 1 || s.Errors != 0 || s.TTFTMedianMS != 10 || s.TotalMedianMS != 100 || !s.LastCall.Equal(day) {
			t.Fatalf("search polluted request counters: %+v", s)
		}
		if !reflect.DeepEqual(s.Meters, []Meter{{"search", "requests", 1, 2}, {"tokens", "tokens", 7, 7}}) {
			t.Fatal(s.Meters)
		}
	}
	for _, e := range rows[2:] {
		if ModelCall(&e) {
			t.Fatal("search is a model call")
		}
	}
}

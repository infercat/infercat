package usage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMetersAggregateEveryBucketWithoutReplacingTelemetry(t *testing.T) {
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	rows := []Event{
		{TS: day, KeyID: "a", Endpoint: "/v1/chat/completions", PromptTokens: 10,
			Meters: []Meter{{"tokens", "tokens", 10, 0}}},
		{TS: day, KeyID: "a", Via: "bridge", Endpoint: "/v1/chat/completions", PromptTokens: 10,
			Meters: []Meter{{"tokens", "tokens", 10, 100}}},
		{TS: day, KeyID: "a", Endpoint: "/v1/audio/transcriptions", Seconds: 1.25,
			Meters: []Meter{{"audio", "seconds", 1.25, 1.25}}},
		{TS: day, KeyID: "a", Endpoint: "/v1/audio/speech", Characters: 7,
			Meters: []Meter{{"speech", "characters", 7, 7}}},
		{TS: day, KeyID: "a", Meters: []Meter{{"future", "units", 3, 2}}},
	}
	var log strings.Builder
	for _, row := range rows {
		if err := json.NewEncoder(&log).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Aggregate(strings.NewReader(log.String()), Filter{Since: day, Days: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := []Meter{{"audio", "seconds", 1.25, 1.25}, {"future", "units", 3, 2}, {"speech", "characters", 7, 7}, {"tokens", "tokens", 20, 100}}
	for _, s := range []*Stats{&rep.Total, rep.Keys[0], &rep.Daily[0].Total, rep.Daily[0].Keys[0]} {
		if !reflect.DeepEqual(s.Meters, want) {
			t.Fatalf("meters = %+v", s.Meters)
		}
		if s.PromptTokens != 20 || s.CompletionTokens != 0 || s.Seconds != 1.25 || s.Characters != 7 {
			t.Fatalf("telemetry = %+v", s)
		}
	}
	for _, by := range []map[string]*Stats{rep.ByVia, rep.Keys[0].ByVia, rep.Daily[0].ByVia, rep.Daily[0].Keys[0].ByVia} {
		if !reflect.DeepEqual(by["bridge"].Meters, []Meter{{"tokens", "tokens", 10, 100}}) {
			t.Fatalf("bridge = %+v", by["bridge"])
		}
		direct := by["direct"].Meters
		if direct[len(direct)-1] != (Meter{"tokens", "tokens", 10, 0}) {
			t.Fatalf("direct = %+v", direct)
		}
	}
}

func TestLegacyMetersAndExplicitZeroCharge(t *testing.T) {
	// Literal pre-108 JSONL: absent meters retains historical measured=charged totals.
	old := `{"key_id":"old","endpoint":"/v1/chat/completions","prompt_tokens":10,"completion_tokens":3}
{"key_id":"old","kind":"transcription","seconds":1.125,"reserved_seconds":5,"seconds_estimated":true}
{"key_id":"old","kind":"speech","characters":7}
`
	rep, err := Aggregate(strings.NewReader(old), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Meter{{"audio", "seconds", 1.125, 1.125}, {"speech", "characters", 7, 7}, {"tokens", "tokens", 13, 13}}
	if !reflect.DeepEqual(rep.Total.Meters, want) || rep.Total.PromptTokens != 10 || rep.Total.CompletionTokens != 3 || rep.Total.Seconds != 1.125 || rep.Total.Characters != 7 {
		t.Fatalf("legacy totals = %+v", rep.Total)
	}
	var e Event
	if err := json.Unmarshal([]byte(`{"prompt_tokens":10,"meters":[{"class":"tokens","unit":"tokens","measured":10,"charged":0}]}`), &e); err != nil {
		t.Fatal(err)
	}
	if got := e.ResourceMeters(); len(got) != 1 || got[0].Charged != 0 {
		t.Fatalf("explicit zero fell back: %+v", got)
	}
}

func TestSettlementBucketsKeepRequestTime(t *testing.T) {
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	e := Event{TS: day.Add(23 * time.Hour), SettledAt: day.Add(25 * time.Hour), KeyID: "overnight", Via: "bridge", Endpoint: "/v1/chat/completions", PromptTokens: 10, Meters: []Meter{{"tokens", "tokens", 10, 100}}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	read := func(f Filter) *Report {
		t.Helper()
		rep, err := Aggregate(strings.NewReader(string(raw)), f)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	both := read(Filter{Since: day, Until: day.Add(48 * time.Hour), Days: 2})
	for i, d := range both.Daily {
		for _, s := range []*Stats{&d.Total, d.Keys[0], d.ByVia["bridge"], d.Keys[0].ByVia["bridge"]} {
			if i == 0 {
				if s.ModelCalls != 1 || s.PromptTokens != 10 || len(s.Meters) != 0 || !s.LastCall.Equal(e.TS) {
					t.Fatalf("start day: %+v", s)
				}
			} else if s.ModelCalls != 0 || s.PromptTokens != 0 || !reflect.DeepEqual(s.Meters, e.Meters) {
				t.Fatalf("settlement day: %+v", s)
			}
		}
	}
	for i := 0; i < 2; i++ {
		since := day.Add(time.Duration(i) * 24 * time.Hour)
		rep := read(Filter{Since: since, Until: since.Add(24 * time.Hour), KeyID: e.KeyID})
		want := both.Daily[i].Total
		if rep.Total.ModelCalls != want.ModelCalls || rep.Total.PromptTokens != want.PromptTokens || !reflect.DeepEqual(rep.Total.Meters, want.Meters) {
			t.Fatalf("filtered day %d: %+v", i, rep.Total)
		}
	}
	if got := read(Filter{KeyID: "someone-else"}); len(got.Keys) != 0 || len(got.Total.Meters) != 0 {
		t.Fatal("meter filter leaked a different key")
	}
	// Old rows can have meters but predate the explicit settlement stamp.
	e.SettledAt = time.Time{}
	raw, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	old := read(Filter{Since: day, Until: day.Add(24 * time.Hour)})
	if !reflect.DeepEqual(old.Total.Meters, e.Meters) {
		t.Fatal("missing settled_at did not fall back to TS")
	}
}

package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/usage"
)

// The recorder must preserve the settlement, not recompute it from observed tokens.
// These cases deliberately use distinct observed and reserved amounts.
func TestMeterSettlementSurvivesRecorderAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		outcome    outcome
		stream     bool
		completion int
		status     int
		want       int
	}{
		{"rejected_after_count", outcomeRejected, false, 0, 429, 0},
		{"cut_nonstream", outcomeCut, false, 0, 499, 100},
		{"cut_stream", outcomeCut, true, 7, 499, 17},
		{"served", outcomeServed, false, 7, 200, 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
			day := now.Truncate(24 * time.Hour)
			lim := newLimiter()
			lim.now = func() time.Time { return now }
			limits := keys.DefaultLimits()
			adm, admissionErr := lim.admit("meter-key", limits)
			if admissionErr != nil {
				t.Fatal(admissionErr)
			}
			if _, reserveErr := lim.reserve(adm, limits, 10, 90); reserveErr != nil {
				t.Fatal(reserveErr)
			}
			dir := t.TempDir()
			rec, err := usage.NewFileRecorder(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rec.Close() })
			q := request{
				g:     &Gateway{lim: lim, rec: rec},
				r:     httptest.NewRequest("POST", string(chatEndpoint), nil),
				start: time.Now(), kind: chatEndpoint, adm: adm, outcome: tc.outcome,
				ev: usage.Event{
					TS: now, KeyID: "meter-key", Endpoint: string(chatEndpoint),
					Status: tc.status, Stream: tc.stream,
					PromptTokens: 10, CompletionTokens: tc.completion,
				},
			}
			// A served request can have begun on the previous UTC day. The record
			// must follow settlement, not make its charge disappear on restart.
			if tc.outcome == outcomeServed {
				q.ev.TS = day.Add(-time.Second)
			}
			startedAt := q.ev.TS
			q.finish()
			if !q.ev.TS.Equal(startedAt) || !q.ev.SettledAt.Equal(now) {
				t.Fatalf("timestamps: start %v settled %v", q.ev.TS, q.ev.SettledAt)
			}
			before := lim.counters("meter-key")
			if before.TodayTokens != tc.want || before.InFlight != 0 {
				t.Fatalf("live settlement: %+v; want charge %d and no admission", before, tc.want)
			}
			if err := rec.Close(); err != nil {
				t.Fatal(err)
			}
			if rec.Dropped() != 0 {
				t.Fatal("recorder dropped the settlement")
			}
			report, err := usage.AggregateFile(dir, usage.Filter{Since: day, Until: day.Add(24 * time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			wantRequests := 1
			if tc.outcome == outcomeServed {
				wantRequests = 0
			}
			if report.Malformed != 0 || report.Total.Requests != wantRequests {
				t.Fatalf("recorded row missing or malformed: %+v", report)
			}
			telemetry, err := usage.AggregateFile(dir, usage.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if telemetry.Total.PromptTokens != 10 || telemetry.Total.CompletionTokens != tc.completion {
				t.Fatalf("observed telemetry changed: %+v", report.Total)
			}
			restarted := newLimiter()
			restarted.now = func() time.Time { return now }
			restarted.seedToday(report, day)
			after := restarted.counters("meter-key")
			t.Logf("measured=%d live charged=%d restored=%d; start=%s settled=%s", 10+tc.completion, before.TodayTokens, after.TodayTokens, q.ev.TS.Format(time.RFC3339), q.ev.SettledAt.Format(time.RFC3339))
			if after.TodayTokens != before.TodayTokens {
				t.Errorf("restart charge = %d; live charge = %d", after.TodayTokens, before.TodayTokens)
			}
			if after.RPMUsed != 0 || after.TPMUsed != 0 || after.InFlight != 0 {
				t.Errorf("restart restored transient admissions: %+v", after)
			}
		})
	}
}

func TestAudioAndSpeechMetersSurviveRecorderAndRestart(t *testing.T) {
	engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == string(transcribeEndpoint) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"text":"fixture","duration":1.125}`)
		} else {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(wave(1))
		}
	})
	dir := t.TempDir()
	h := newHarness(t, Config{DataDir: dir, Transcribe: engine, Speech: engine}, nil)
	raw, ct := multipartAudio(t, wave(1))
	if r := postAudio(t, h, string(transcribeEndpoint), ct, raw); r.status != 200 {
		t.Fatalf("transcribe: %+v", r)
	}
	if r := postAudio(t, h, string(speechEndpoint), "application/json", []byte(`{"model":"m1","input":"你好!"}`)); r.status != 200 {
		t.Fatalf("speech: %+v", r)
	}
	events := h.rec.waitFor(t, 2)
	want := [][]usage.Meter{{{Class: "audio", Unit: "seconds", Measured: 1.125, Charged: 1.125}}, {{Class: "speech", Unit: "characters", Measured: 3, Charged: 3}}}
	for i, event := range events {
		if !reflect.DeepEqual(event.Meters, want[i]) {
			t.Fatalf("event %d: %+v", i, event)
		}
	}
	if events[0].Seconds != 1.125 || events[0].ReservedSeconds != 1 || events[0].OverrunSeconds != .125 || events[0].SecondsEstimated {
		t.Fatalf("audio provenance: %+v", events[0])
	}
	before := h.gw.Counters(h.key.ID)
	h.srv.Close()
	if err := h.file.Close(); err != nil {
		t.Fatal(err)
	}
	restored := New(Config{DataDir: dir}, h.up, h.store, nil, nil).Counters(h.key.ID)
	if restored.TodayAudioSeconds != 1.125 || restored.TodaySpeechChars != 3 || restored.TodayAudioSeconds != before.TodayAudioSeconds || restored.TodaySpeechChars != before.TodaySpeechChars {
		t.Fatalf("before %+v after %+v", before, restored)
	}
}

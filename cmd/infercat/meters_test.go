package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/usage"
)

func TestUsagePrintsChargedMetersIncludingUnknownClasses(t *testing.T) {
	dir := t.TempDir()
	rec, err := usage.NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rec.Close() })
	for _, event := range []usage.Event{
		{PromptTokens: 10, Meters: []usage.Meter{{Class: "tokens", Unit: "tokens", Measured: 10, Charged: 100}}},
		{PromptTokens: 5, Meters: []usage.Meter{{Class: "tokens", Unit: "tokens", Measured: 5, Charged: 0}}},
		{Seconds: 1.125, Meters: []usage.Meter{{Class: "audio", Unit: "seconds", Measured: 1.125, Charged: 1.125}}},
		{Meters: []usage.Meter{{Class: "future", Unit: "units", Measured: 9, Charged: 0}}},
	} {
		event.TS, event.KeyID, event.Via = time.Now(), "meter-key", "bridge"
		rec.Record(context.Background(), event)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	r := exec(t, testPlatform(fakeAddr, nil), "usage", "--data-dir", dir)
	if r.code != 0 {
		t.Fatal(r.err)
	}
	for _, want := range []string{"100 tokens (tokens)", "1.125 seconds (audio)", "0 units (future)"} {
		if strings.Count(r.out, want) != 3 {
			t.Fatalf("total, via and key must each show %q:\n%s", want, r.out)
		}
	}
	if !strings.Contains(r.out, "15 prompt") {
		t.Fatalf("measured telemetry missing:\n%s", r.out)
	}
}

func TestUsageShowsSettlementWhenRequestPredatesSince(t *testing.T) {
	dir := t.TempDir()
	rec, err := usage.NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rec.Close() })
	rec.Record(context.Background(), usage.Event{TS: time.Now().Add(-48 * time.Hour), SettledAt: time.Now(), KeyID: "old-start", Endpoint: "/v1/chat/completions", PromptTokens: 10, Meters: []usage.Meter{{Class: "tokens", Unit: "tokens", Measured: 10, Charged: 100}}})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	r := exec(t, testPlatform(fakeAddr, nil), "usage", "--data-dir", dir, "--since", "24h")
	if r.code != 0 || strings.Contains(r.out, "nothing yet") || !strings.Contains(r.out, "100 tokens (tokens)") || !strings.Contains(r.out, "0 model calls") || strings.Contains(r.out, "10 prompt") {
		t.Fatalf("settlement-only window: %s %s", r.out, r.err)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/gateway"
)

func TestStatusUpstreamLineNamesTextModelsAndPins(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		models, pins []string
		want         string
	}{
		{name: "none", want: "models (no models reported)"},
		{name: "one", models: []string{"alpha"}, want: "models alpha"},
		{name: "several", models: []string{"alpha", "beta"}, want: "models alpha, beta"},
		{name: "pinned", models: []string{"alpha"}, pins: []string{"alpha", "offline"}, want: "models alpha  context 4096  slots 2  alpha, offline (pinned)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := admin.Status{Mode: "host", Upstream: admin.Upstream{Kind: "llama.cpp", URL: "http://engine", Healthy: true, ModelContext: 4096, Slots: 2}, ModelsPinned: tc.pins,
				Destinations: []gateway.DestinationStatus{{ID: "images", Models: []string{"not-the-text-model"}}, {ID: "text", Models: tc.models}}}
			var out bytes.Buffer
			writeStatus(&out, st)
			line := ""
			for _, s := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(s, "upstream  ") {
					line = s
				}
			}
			if !strings.Contains(line, tc.want) || !strings.Contains(line, "llama.cpp  http://engine  healthy") || strings.Contains(line, "not-the-text-model") {
				t.Fatal(line)
			}
			if len(tc.pins) == 0 && strings.Contains(line, "pinned") {
				t.Fatal(line)
			}
			if strings.Contains(out.String(), "\nmodels    ") {
				t.Fatal("pins were not moved beside the upstream models")
			}
		})
	}
}

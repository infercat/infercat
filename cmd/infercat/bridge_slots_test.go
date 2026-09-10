package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/bridge"
)

func TestPublicBridgeSlotsStatus(t *testing.T) {
	for _, tc := range []struct {
		slots     int
		connected bool
		want      string
	}{{48, true, "48 slots"}, {1, true, "1 slot"}, {0, true, ""}, {48, false, ""}} {
		st := admin.Status{Bridge: &bridge.Status{Enabled: true, Connected: tc.connected, Slots: tc.slots}}
		raw, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		var decoded admin.Status
		if err = json.Unmarshal(raw, &decoded); err != nil || decoded.Bridge.Slots != tc.slots {
			t.Fatal("admin wire shape", string(raw), err)
		}
		var b bytes.Buffer
		writeStatus(&b, decoded)
		var line string
		for _, v := range strings.Split(b.String(), "\n") {
			if strings.HasPrefix(v, "bridge    ") {
				line = v
			}
		}
		if line == "" || (tc.want != "" && !strings.Contains(line, " · "+tc.want)) || (tc.want == "" && strings.Contains(line, "slot")) {
			t.Fatal(tc, line)
		}
	}
}

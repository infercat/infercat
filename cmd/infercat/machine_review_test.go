package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestUsageKeyFilterIsLocalAtTheRoute(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	for _, query := range []string{"key_id=" + f.id, "key_id=", "%6bey_id=" + f.id, "key_id=a&key_id=b"} {
		for _, remote := range []bool{false, true} {
			r := httptest.NewRequest("GET", "/usage?"+query, nil)
			if remote {
				r.Header.Set("X-Infercat-Remote", "true")
			}
			out := httptest.NewRecorder()
			f.api.ServeHTTP(out, r)
			want := 200
			if remote {
				want = 400
			}
			if out.Code != want {
				t.Fatal(query, remote, out.Code, out.Body.String())
			}
		}
	}
}

func TestKeyResponsesExposeAgentCapability(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	k, err := f.store.Find(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []bool{false, true} {
		if err := f.store.SetLimitsAndAgent(context.Background(), f.id, k.Limits, &agent); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"keys", "list", "--json"}, {"keys", "show", f.id, "--json"}} {
			code, result, raw := machineCall(t, context.Background(), append([]string{"--data-dir", f.dir}, args...)...)
			if code != 0 {
				t.Fatal(code, raw)
			}
			var item map[string]json.RawMessage
			if args[1] == "list" {
				var items []map[string]json.RawMessage
				if err := json.Unmarshal(result["data"], &items); err != nil || len(items) != 1 {
					t.Fatal(err)
				}
				item = items[0]
			} else if err := json.Unmarshal(result["data"], &item); err != nil {
				t.Fatal(err)
			}
			want := "false"
			if agent {
				want = "true"
			}
			if string(item["agent"]) != want {
				t.Fatal("agent presence/value", string(item["agent"]))
			}
		}
	}
}

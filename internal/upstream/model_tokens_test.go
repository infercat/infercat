package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTokenizerReceivesSelectedModel(t *testing.T) {
	for _, kind := range []Kind{VLLM, LlamaCPP, LlamaSwap} {
		for _, chat := range []bool{false, true} {
			t.Run(string(kind)+map[bool]string{false: "/text", true: "/chat"}[chat], func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					var model string
					_ = json.Unmarshal(body["model"], &model)
					if model != "second" {
						t.Errorf("%s model=%q", r.URL.Path, model)
					}
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/apply-template":
						io.WriteString(w, `{"prompt":"rendered"}`)
					case "/tokenize":
						io.WriteString(w, `{"count":3,"tokens":[1,2,3]}`)
					default:
						t.Errorf("unexpected path %s", r.URL.Path)
					}
				}))
				defer server.Close()
				base, err := normalize(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				c := newClient(base, "")
				c.info = Info{Kind: kind, Models: []string{"first", "second"}}
				var messages []byte
				if chat {
					messages = []byte(`[{"role":"user","content":"hello"}]`)
				}
				n, exact, err := c.CountTokens(context.Background(), "second", "hello", messages)
				if err != nil || !exact || n != 3 {
					t.Fatalf("count=%d exact=%v err=%v", n, exact, err)
				}
				want := 1
				if chat && kind != VLLM {
					want = 2
				}
				if calls.Load() != int32(want) {
					t.Fatalf("calls=%d want=%d", calls.Load(), want)
				}
			})
		}
	}
}

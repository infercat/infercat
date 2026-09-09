package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestVisionProbes(t *testing.T) {
	for _, kind := range []Kind{LlamaCPP, Ollama, LMStudio, VLLM, Generic} {
		t.Run(string(kind), func(t *testing.T) {
			var stage atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := stage.Load()
				if n == 4 {
					http.Error(w, "offline", 503)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/props":
					if kind != LlamaCPP {
						http.NotFound(w, r)
						return
					}
					switch n {
					case 0:
						w.Write([]byte(`{"total_slots":1,"modalities":{"vision":true}}`))
					case 1:
						w.Write([]byte(`{"total_slots":1,"modalities":{"vision":false}}`))
					default:
						w.Write([]byte(`{"total_slots":1}`))
					}
				case "/v1/models":
					if n == 3 {
						w.Write([]byte(`{"data":[{"id":"replacement"}]}`))
					} else {
						w.Write([]byte(`{"data":[{"id":"m"},{"id":"text"}]}`))
					}
				case "/api/tags":
					if n == 3 {
						w.Write([]byte(`{"models":[{"name":"replacement"}]}`))
					} else {
						w.Write([]byte(`{"models":[{"name":"m"},{"name":"text"}]}`))
					}
				case "/api/show":
					var body struct {
						Model string `json:"model"`
					}
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Model == "" {
						t.Error("show must POST model id")
					}
					if n >= 2 {
						w.Write([]byte(`{}`))
					} else if n == 1 || body.Model == "text" {
						w.Write([]byte(`{"capabilities":["completion"]}`))
					} else {
						w.Write([]byte(`{"capabilities":["completion","vision"]}`))
					}
				case "/api/v0/models":
					switch n {
					case 0:
						w.Write([]byte(`{"data":[{"id":"m","type":"vlm"},{"id":"text","type":"llm"},{"id":"unserved","type":"vlm"}]}`))
					case 1:
						w.Write([]byte(`{"data":[{"id":"m","type":"llm"}]}`))
					default:
						w.Write([]byte(`{"data":[]}`))
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			c := newClient(u, "")
			if c.Info().Vision != nil {
				t.Fatal("unprobed capability is unknown")
			}
			c.info.Kind = kind
			for n := int32(0); n < 4; n++ {
				stage.Store(n)
				if err := c.Refresh(context.Background()); err != nil {
					t.Fatal(err)
				}
				info := c.Info()
				id := "m"
				if n == 3 {
					id = "replacement"
					if _, ok := info.Vision["m"]; ok {
						t.Fatal("old model survived swap")
					}
				}
				got, ok := info.Vision[id]
				if !ok {
					t.Fatalf("missing entry: %+v", info)
				}
				var want *bool
				if kind == VLLM {
					v := true
					want = &v
				} else if kind != Generic && n < 2 {
					v := n == 0
					want = &v
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("stage %d: capability %v, want %v", n, got, want)
				}
				if (kind == Ollama || kind == LMStudio) && n == 0 && (info.Vision["text"] == nil || *info.Vision["text"]) {
					t.Fatal("mixed text model must be false")
				}
				if _, ok := info.Vision["unserved"]; ok {
					t.Fatal("unserved native model leaked")
				}
				if got != nil {
					*got = !*got
					if reflect.DeepEqual(c.Info().Vision[id], got) {
						t.Fatal("Info pointer aliases state")
					}
				}
				delete(info.Vision, id)
				if _, ok := c.Info().Vision[id]; !ok {
					t.Fatal("Info map aliases state")
				}
			}
			before := c.Info().Vision
			stage.Store(4)
			if err := c.Refresh(context.Background()); err == nil {
				t.Fatal("offline refresh succeeded")
			}
			if !reflect.DeepEqual(before, c.Info().Vision) || c.Info().Health.OK {
				t.Fatal("failed refresh changed capabilities or stayed healthy")
			}
		})
	}
}

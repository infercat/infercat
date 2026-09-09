package gateway

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

func TestMeVisionPerVisibleModel(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	yes, no := true, false
	h.up.setInfo(func(i *upstream.Info) {
		i.Models = []string{"m1", "m2", "unknown", "hidden"}
		i.Vision = map[string]*bool{"m1": &yes, "m2": &no, "hidden": &yes}
	})
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m1", "m2", "unknown"} })
	r := h.get("/me")
	var me meResponse
	if err := json.Unmarshal(r.body, &me); err != nil {
		t.Fatal(err)
	}
	want := map[string]*bool{"m1": &yes, "m2": &no, "unknown": nil}
	if r.status != 200 || !reflect.DeepEqual(me.Host.Vision, want) || len(me.Host.Models) != 3 {
		t.Fatalf("/me: %d %s", r.status, r.body)
	}
	t.Logf("host.vision=%s", mustVisionJSON(t, me.Host.Vision))
}
func mustVisionJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestImageRefusalMapping(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		message string
		image   bool
		want    Code
	}{
		{"image", 400, "this model does not support image input", true, CodeImagesNotSupported},
		{"multimodal", 422, "multimodal input is not supported", true, CodeImagesNotSupported},
		{"other4xx", 415, "unsupported image input", true, CodeImagesNotSupported},
		{"textOnly", 400, "this model does not support image input", false, CodeInvalidRequest},
		{"otherError", 400, "invalid grammar", true, CodeInvalidRequest},
		{"serverError", 500, "image decoder failed", true, CodeUpstreamError},
		{"auth", 401, "invalid api key", true, CodeUpstreamError},
		{"context", 400, "This model's maximum context length is 100 tokens, including images", true, CodeContextTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{}, nil)
			b, _ := json.Marshal(map[string]any{"error": map[string]string{"message": tc.message}})
			if tc.name == "multimodal" {
				b, _ = json.Marshal(map[string]string{"error": tc.message})
			}
			h.up.set(fmt.Sprintf("status:%d", tc.status), string(b))
			body := chatBody("m1", 1, "")
			if tc.image {
				body = `{"model":"m1","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,AA=="}},{"type":"text","text":"describe"}]}],"max_tokens":1}`
			}
			r := h.post("/v1/chat/completions", body)
			h.expectErr(r, tc.want)
			if tc.want == CodeImagesNotSupported && (r.status != 400 || r.message != tc.message || strings.Contains(string(r.body), "retry_after")) {
				t.Fatalf("refusal: %d %s", r.status, r.body)
			}
		})
	}
}

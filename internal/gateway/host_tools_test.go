package gateway

import (
	"encoding/json"
	"fmt"
	"github.com/infercat/infercat/internal/keys"
	"reflect"
	"strings"
	"testing"
)

func TestHostToolOptInIsExplicitAndExclusive(t *testing.T) {
	for _, tc := range []struct {
		raw              string
		enabled, refused bool
	}{
		{`{}`, false, false}, {`{"host_tools":[]}`, false, false}, {`{"tools":[{"type":"function"}]}`, false, false},
		{`{"host_tools":["make_image"]}`, true, false},
		{`{"host_tools":null}`, false, true}, {`{"host_tools":"make_image"}`, false, true},
		{`{"host_tools":["other"]}`, false, true}, {`{"host_tools":["make_image","make_image"]}`, false, true},
		{`{"host_tools":[{}]}`, false, true}, {`{"host_tools":["make_image"],"tools":[]}`, false, true},
		{`{"host_tools":["make_image"],"tool_choice":"none"}`, false, true},
	} {
		body, _ := decodeObject([]byte(tc.raw))
		enabled, err := asksHostTools(body)
		if enabled != tc.enabled || (err != nil) != tc.refused {
			t.Fatalf("%s: %v %v", tc.raw, enabled, err)
		}
	}
}
func TestMakeImageArgumentsAreBounded(t *testing.T) {
	for _, raw := range []string{`{"prompt":"a fox","count":1}`, `{"prompt":"a fox","count":8}`} {
		if _, err := parseImageTool(raw, 8); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{`{}`, `null`, `{"prompt":"","count":1}`, `{"prompt":"a fox","count":0}`, `{"prompt":"a fox","count":9}`, `{"prompt":"a fox","count":1.5}`, `{"prompt":"a fox","count":"1"}`, `{"prompt":"a fox","count":1,"model":"private"}`, `{"prompt":"a <sd_cpp_extra_args>fox","count":1}`, `{"prompt":"a fox","count":1} {}`} {
		if _, err := parseImageTool(raw, 8); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := parseImageTool(`{"prompt":"a fox","count":17}`, ImageQueueCap(keys.Limits{MaxQueuedImages: 100})); err == nil {
		t.Fatal("live-run cap ignored")
	}
	tool := makeImageTool(8)["function"].(map[string]any)
	schema := tool["parameters"].(map[string]any)
	if !reflect.DeepEqual(schema["required"], []string{"prompt", "count"}) || schema["additionalProperties"] != false {
		t.Fatal(schema)
	}
}
func TestHostReplyForwardsTextNotToolCalls(t *testing.T) {
	raw := `data: {"choices":[{"delta":{"reasoning":"think"}}]}

data: {"choices":[{"delta":{"content":"I will draw it."}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"make_image","arguments":"{\"prompt\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"fox\",\"count\":1}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	var p hostToolReply
	var forwarded []chatDelta
	for _, b := range []byte(raw) {
		if err := p.consume([]byte{b}, func(d chatDelta) error { forwarded = append(forwarded, d); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if !p.finished || len(forwarded) != 2 || forwarded[0].reasoning() != "think" || forwarded[1].Content != "I will draw it." {
		t.Fatal(p.finished, forwarded)
	}
	for _, d := range forwarded {
		if len(d.ToolCalls) > 0 {
			t.Fatal("client received host call")
		}
	}
	if len(p.calls) != 1 || p.calls[0].ID != "c1" || p.calls[0].Function.Name != "make_image" {
		t.Fatal(p.calls)
	}
	args, err := parseImageTool(p.calls[0].Function.Arguments, 8)
	if err != nil || args.Prompt != "fox" || args.Count != 1 {
		t.Fatal(args, err)
	}
	encoded, _ := json.Marshal(p.message())
	if strings.Contains(string(encoded), "reasoning") || !strings.Contains(string(encoded), "tool_calls") {
		t.Fatal(string(encoded))
	}
}

func TestMakeImageUnlimitedQueueStillBoundsOneBatch(t *testing.T) {
	for _, count := range []int{1, 16, 17} {
		raw := fmt.Sprintf(`{"prompt":"fox","count":%d}`, count)
		_, err := parseImageTool(raw, ImageQueueCap(keys.Limits{MaxQueuedImages: -1}))
		if (err == nil) != (count <= 16) {
			t.Fatal(count, err)
		}
	}
	if ImageQueueCap(keys.Limits{MaxQueuedImages: -1}) != 16 || ImageQueueCap(keys.Limits{MaxQueuedImages: 8}) != 8 {
		t.Fatal("incorrect effective cap")
	}
}

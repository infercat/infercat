package gateway

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
)

func responseBody(extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return `{"model":"m1","input":"hello","store":false` + extra + `}`
}
func fastResponses(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, Config{}, nil)
	h.up.mu.Lock()
	h.up.gap = 0
	h.up.mu.Unlock()
	return h
}
func responseEvents(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data:") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "data:"))), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}
func finalResponse(t *testing.T, events []map[string]any) map[string]any {
	t.Helper()
	last := events[len(events)-1]
	if last["type"] != "response.completed" {
		t.Fatalf("not completed: %v", last)
	}
	return last["response"].(map[string]any)
}

func TestResponsesCapturedRefusalsAndDerivedInput(t *testing.T) {
	hashes := []string{"823ccd1a9510d102236df0857f4016a28e7d609e883d1505d042346e95034941", "87f3b21650da393797b539d78159656d62786cdb3ecacd14bc8320fbc9c0f330"}
	for i, hash := range hashes {
		t.Run(fmt.Sprint(i+1), func(t *testing.T) {
			raw, err := os.ReadFile(fmt.Sprintf("testdata/responses/request-%d.json", i+1))
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != hash {
				t.Fatalf("133 fixture changed: %s", got)
			}
			h := fastResponses(t)
			r := h.post(string(responsesEndpoint), string(raw))
			h.expectErr(r, CodeInvalidRequest)
			if !strings.Contains(string(r.body), `web_search = \"disabled\"`) || h.up.requests.Load() != 0 {
				t.Fatalf("hosted tool refusal: %s", r.body)
			}
			// Derived from the 133 capture: remove only the hosted tool. Not a capture.
			input, _ := decodeObject(raw)
			var supported []any
			for _, v := range input["tools"].([]any) {
				if v.(map[string]any)["type"] != "web_search" {
					supported = append(supported, v)
				}
			}
			input["tools"] = supported
			projected, _ := json.Marshal(input)
			h.up.set("sse", `{"choices":[{"delta":{"reasoning":"real engine thought"}}]}`, `{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`)
			r = h.post(string(responsesEndpoint), string(projected))
			if r.status != 200 {
				t.Fatalf("projection: %d %s", r.status, r.body)
			}
			finalResponse(t, responseEvents(t, r.body))
			engine := h.up.body(t)
			serialized, _ := json.Marshal(engine)
			for _, secret := range []string{"spike-133-opaque-fixture", "Synthetic fixture summary", "encrypted_content", "reasoning_content"} {
				if bytes.Contains(serialized, []byte(secret)) {
					t.Fatalf("client reasoning leaked to engine: %s", secret)
				}
			}
			if engine["messages"] == nil || engine["tools"] == nil || engine["input"] != nil {
				t.Fatal("projection not translated")
			}
			if ev := h.rec.waitFor(t, 2)[1]; ev.Endpoint != string(responsesEndpoint) || ev.Destination != "text" || ev.Model != "spike-133" {
				t.Fatalf("route accounting: %+v", ev)
			}
		})
	}
}

// Mirrors the two observations in 133 and Codex 0.154.0's SSE consumer:
// output_item.done owns history; response.completed owns successful completion.
func codexHistory(events []map[string]any) ([]any, error) {
	var history []any
	for _, event := range events {
		switch event["type"] {
		case "response.output_item.done":
			history = append(history, event["item"])
		case "response.completed":
			return history, nil
		}
	}
	return history, fmt.Errorf("stream closed before response.completed")
}
func TestResponsesLifecycleAndOmissions(t *testing.T) {
	h := fastResponses(t)
	h.up.set("sse", `{"choices":[{"delta":{"reasoning_content":"thought"}}]}`, `{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}}`)
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
	events := responseEvents(t, r.body)
	final := finalResponse(t, events)
	var done []any
	for i, event := range events {
		if event["sequence_number"] != float64(i) || event["response_id"] != final["id"] {
			t.Fatalf("sequence/id changed: %v", event)
		}
		if event["type"] == "response.output_item.done" {
			done = append(done, event["item"])
		}
		if event["type"] == "response.output_item.added" {
			item := event["item"].(map[string]any)
			if item["type"] == "message" && item["content"] == nil || item["type"] == "reasoning" && item["summary"] == nil {
				t.Fatal("missing required empty array")
			}
		}
	}
	if len(done) != 2 || !reflect.DeepEqual(done, final["output"]) {
		t.Fatalf("final items differ from done: %v", final)
	}
	usage := final["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) || usage["total_tokens"] != float64(15) || usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(1) {
		t.Fatal(usage)
	}
	for _, source := range []string{"generated", "events-1.json", "events-2.json"} {
		testEvents := events
		if source != "generated" {
			raw, err := os.ReadFile("testdata/responses/" + source)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &testEvents); err != nil {
				t.Fatal(err)
			}
		}
		history, err := codexHistory(testEvents)
		if err != nil || len(history) != 2 {
			t.Fatalf("%s normal history: %v %v", source, history, err)
		}
		for _, omit := range []string{"response.completed", "response.output_item.done"} {
			var filtered []map[string]any
			for _, e := range testEvents {
				if e["type"] != omit {
					filtered = append(filtered, e)
				}
			}
			history, err := codexHistory(filtered)
			if omit == "response.completed" && (err == nil || len(history) != 2) {
				t.Fatalf("%s completion omission: %v %v", source, history, err)
			}
			if omit == "response.output_item.done" && (err != nil || len(history) != 0) {
				t.Fatalf("%s history omission: %v %v", source, history, err)
			}
		}
	}
	// A second request resends completed items; only the assistant text is prompt.
	input := []any{map[string]any{"role": "user", "content": "hello"}}
	input = append(input, done...)
	input = append(input, map[string]any{"role": "user", "content": "continue"})
	body, _ := json.Marshal(map[string]any{"model": "m1", "input": input, "stream": true, "store": false})
	h.post(string(responsesEndpoint), string(body))
	encoded, _ := json.Marshal(h.up.body(t)["messages"])
	if strings.Contains(string(encoded), "thought") || !strings.Contains(string(encoded), "answer") {
		t.Fatalf("second turn: %s", encoded)
	}
}

func TestResponsesFunctionRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, namespace := range []string{"", "multi_agent_v1"} {
			t.Run(fmt.Sprintf("stream=%v/namespace=%s", stream, namespace), func(t *testing.T) {
				h := fastResponses(t)
				alias, _ := chatToolName(namespace, "lookup")
				tool := map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}}
				var definition any = tool
				if namespace != "" {
					definition = map[string]any{"type": "namespace", "name": namespace, "tools": []any{tool}}
				}
				body := map[string]any{"model": "m1", "input": "lookup 1", "tools": []any{definition}, "stream": stream, "max_output_tokens": 128}
				if stream {
					// Constructed from the official function-call streaming contract; not 133 capture data.
					h.up.set("sse", fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":%q,"arguments":""}}]}}]}`, alias), `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"id\":"}}]}}]}`, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`, `{"usage":{"prompt_tokens":3,"completion_tokens":2},"choices":[]}`)
				} else {
					h.up.set("json", fmt.Sprintf(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":%q,"arguments":"{\"id\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`, alias))
				}
				raw, _ := json.Marshal(body)
				r := h.post(string(responsesEndpoint), string(raw))
				if r.status != 200 {
					t.Fatalf("%d %s", r.status, r.body)
				}
				var final map[string]any
				if stream {
					events := responseEvents(t, r.body)
					final = finalResponse(t, events)
					kinds := []string{}
					for _, e := range events {
						kinds = append(kinds, str(e, "type"))
						if e["type"] == "response.output_item.added" && e["item"].(map[string]any)["arguments"] != "" {
							t.Fatal("missing empty arguments")
						}
					}
					want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.function_call_arguments.delta", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.done", "response.completed"}
					if !reflect.DeepEqual(kinds, want) {
						t.Fatalf("function lifecycle: %v", kinds)
					}
				} else if err := json.Unmarshal(r.body, &final); err != nil {
					t.Fatal(err)
				}
				call := final["output"].([]any)[0].(map[string]any)
				if call["name"] != "lookup" || call["call_id"] != "call_1" || call["arguments"] != `{"id":1}` || str(call, "namespace") != namespace {
					t.Fatalf("call: %v", call)
				}
				body["input"] = []any{map[string]any{"role": "user", "content": "lookup 1"}, call, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "found"}}
				raw, _ = json.Marshal(body)
				h.post(string(responsesEndpoint), string(raw))
				msgs := h.up.body(t)["messages"].([]any)
				fn := msgs[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
				if fn["name"] != alias || fn["arguments"] != `{"id":1}` || msgs[2].(map[string]any)["tool_call_id"] != "call_1" {
					t.Fatalf("round trip: %v", msgs)
				}
				if ev := h.rec.last(t); ev.PromptTokens != 3 || ev.CompletionTokens != 2 || ev.Meters[0].Charged != 5 {
					t.Fatalf("original chat usage: %+v", ev)
				}
			})
		}
	}
}

func TestResponsesRefusalsBeforeEngine(t *testing.T) {
	for _, extra := range []string{`"previous_response_id":"resp_old"`, `"store":true`, `"background":true`, `"stream":"yes"`, `"max_output_tokens":0`, `"tools":[{"type":"code_interpreter"}]`, `"tools":[{"type":"namespace","name":"ns","tools":[{"type":"web_search"}]}]`, `"tool_choice":{"type":"web_search"}`, `"tools":[{"type":"function","name":"x"},{"type":"function","name":"x"}]`, `"input":[{"type":"item_reference","id":"old"}]`, `"input":[{"role":"user","content":[{"type":"input_file","file_id":"f"}]}]`} {
		t.Run(extra, func(t *testing.T) {
			h := fastResponses(t)
			h.expectErr(h.post(string(responsesEndpoint), responseBody(extra)), CodeInvalidRequest)
			if h.up.requests.Load() != 0 {
				t.Fatal("refusal dispatched")
			}
			for _, meter := range h.rec.last(t).Meters {
				if meter.Charged != 0 {
					t.Fatal(meter)
				}
			}
		})
	}
	alias, _ := chatToolName("ns", "x")
	h := fastResponses(t)
	h.expectErr(h.post(string(responsesEndpoint), responseBody(fmt.Sprintf(`"tools":[{"type":"function","name":%q},{"type":"namespace","name":"ns","tools":[{"type":"function","name":"x"}]}]`, alias))), CodeInvalidRequest)
	h.expectErr(h.do(http.MethodGet, string(responsesEndpoint), "bearer", ""), CodeInvalidRequest)
	h.expectErr(h.do(http.MethodPost, string(responsesEndpoint), "", responseBody("")), CodeInvalidKey)
}

func TestResponsesCompatibilityAndAdmission(t *testing.T) {
	// Existing clients still use chat unchanged. Responses is an additive text route,
	// sharing clamps, queue, auth and q.finish; /me gains no capability field.
	for _, path := range []endpoint{chatEndpoint, responsesEndpoint} {
		t.Run(string(path), func(t *testing.T) {
			h := fastResponses(t)
			h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 32 })
			body := chatBody("m1", 1, `"max_tokens":1000`)
			if path == responsesEndpoint {
				body = responseBody(`"max_output_tokens":1000`)
			}
			if r := h.post(string(path), body); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			if h.up.body(t)["max_tokens"] != json.Number("32") {
				t.Fatal(h.up.body(t))
			}
			if ev := h.rec.last(t); ev.Endpoint != string(path) || ev.Meters[0].Charged != 8 {
				t.Fatal(ev)
			}
			h.up.mu.Lock()
			enginePath := h.up.lastPath
			h.up.mu.Unlock()
			if enginePath != string(chatEndpoint) {
				t.Fatal(enginePath)
			}
			h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"other"} })
			h.expectErr(h.post(string(path), body), CodeModelNotAllowed)
		})
	}
	h := fastResponses(t)
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1 })
	adm, err := h.gw.lim.admit("k_alice1", h.key.Limits)
	if err != nil {
		t.Fatal(err)
	}
	defer h.gw.lim.settle(adm, false, 0)
	h.expectErr(h.post(string(responsesEndpoint), `not json`), CodeConcurrencyLimited)
}

func TestResponsesCutNeverCompletes(t *testing.T) {
	h := fastResponses(t)
	h.gw.idleTimeout = 100 * time.Millisecond
	h.up.set("sse", `{"choices":[{"delta":{"content":"partial"}}]}`, `{"usage":{"prompt_tokens":99,"completion_tokens":99},"choices":[]}`)
	h.up.mu.Lock()
	h.up.stallAfter = 1
	h.up.mu.Unlock()
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
	if bytes.Contains(r.body, []byte("response.completed")) || !bytes.Contains(r.body, []byte("upstream_error")) {
		t.Fatalf("cut reported success: %s", r.body)
	}
	ev := h.rec.last(t)
	if ev.CompletionTokens != 1 || ev.PromptTokens != 1 || ev.Meters[0].Charged != 2 {
		t.Fatalf("envelopes charged or usage fabricated: %+v", ev)
	}
}

func TestResponsesMalformedStreamAndLength(t *testing.T) {
	for _, payload := range []string{`not json`, `{"error":{"message":"failed"}}`, `{"choices":[{"delta":{"content":"x"},"finish_reason":"unknown"}]}`} {
		h := fastResponses(t)
		h.up.set("sse", payload)
		r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
		if bytes.Contains(r.body, []byte("response.completed")) {
			t.Fatalf("bad engine response completed: %s", r.body)
		}
	}
	h := fastResponses(t)
	h.up.set("sse", `{"choices":[{"delta":{"content":"truncated"},"finish_reason":"length"}]}`)
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
	events := responseEvents(t, r.body)
	if events[len(events)-1]["type"] != "response.incomplete" || bytes.Contains(r.body, []byte("response.completed")) {
		t.Fatalf("length became success: %s", r.body)
	}
}

func TestResponsesImageAndTextFormat(t *testing.T) {
	raw := `{"input":[{"role":"user","content":[{"type":"input_text","text":"describe"},{"type":"input_image","image_url":"data:image/png;base64,aA==","detail":"low"}]}],"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"},"strict":true}},"reasoning":{"effort":"low"}}`
	body, _ := decodeObject([]byte(raw))
	chat, _, err := translateResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	if chat["reasoning_effort"] != "low" || !hasImageParts(chat["messages"]) || chat["response_format"].(map[string]any)["json_schema"].(map[string]any)["name"] != "answer" {
		t.Fatal(chat)
	}
}

func TestResponsesSSEFramingAndFailures(t *testing.T) {
	cases := []struct {
		name, wire string
		cut        bool
		completed  bool
	}{
		{"CRLF and multiline", "event: message\r\ndata: {\"choices\":\r\ndata: [{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n", false, true},
		{"finish at EOF", "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n", false, true},
		{"early EOF", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", false, false},
		{"read failure after finish", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"stop\"}]}\n\n", true, false},
		{"empty DONE", "data: [DONE]\n\n", false, false},
		{"unknown JSON", "data: {\"other\":1}\n\ndata: [DONE]\n\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fastResponses(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, string(responsesEndpoint), nil)
			q := h.gw.newRequest(w, r)
			q.responses = &responsesAdapter{tools: map[string]responseToolName{}}
			q.n.model = "m1"
			var body io.Reader = strings.NewReader(tc.wire)
			if tc.cut {
				body = io.MultiReader(body, failReader{})
			}
			err := q.pipeResponsesStream(body)
			completed := bytes.Contains(w.Body.Bytes(), []byte("response.completed"))
			if completed != tc.completed || (err == nil) != tc.completed {
				t.Fatalf("completed=%v err=%v wire=%s", completed, err, w.Body.String())
			}
			if !tc.completed && q.outcome != outcomeCut {
				t.Fatalf("lost cut outcome: %v", q.outcome)
			}
		})
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestResponsesInterleavedFunctions(t *testing.T) {
	h := fastResponses(t)
	h.up.set("sse",
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"b"}},{"index":0,"id":"call_a","function":{"name":"a"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"n\":"}},{"index":1,"function":{"arguments":"{\"n\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}},{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`)
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true,"tools":[{"type":"function","name":"a"},{"type":"function","name":"b"}]`))
	events := responseEvents(t, r.body)
	final := finalResponse(t, events)
	items := final["output"].([]any)
	if len(items) != 2 {
		t.Fatal(items)
	}
	for i, name := range []string{"a", "b"} {
		item := items[i].(map[string]any)
		if item["name"] != name || item["call_id"] != "call_"+name || item["arguments"] != fmt.Sprintf(`{"n":%d}`, i+1) {
			t.Fatal(item)
		}
		for _, e := range events {
			if e["output_index"] == float64(i) {
				if id, ok := e["item_id"]; ok && id != item["id"] {
					t.Fatal("item id drift")
				}
			}
		}
	}
}

func TestResponsesToolOnlyCutChargesOriginalDeltas(t *testing.T) {
	h := fastResponses(t)
	h.gw.idleTimeout = 100 * time.Millisecond
	h.up.set("sse", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"lookup","arguments":"{\"n\":"}}]}}]}`, `{"usage":{"prompt_tokens":99,"completion_tokens":99},"choices":[]}`)
	h.up.mu.Lock()
	h.up.stallAfter = 1
	h.up.mu.Unlock()
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true,"tools":[{"type":"function","name":"lookup"}]`))
	if bytes.Contains(r.body, []byte("response.completed")) {
		t.Fatal("cut completed")
	}
	ev := h.rec.last(t)
	if ev.CompletionTokens != 1 || ev.Meters[0].Charged != 2 {
		t.Fatalf("tool arguments not charged: %+v", ev)
	}
}

func TestResponsesNonStreamFilteredUsage(t *testing.T) {
	h := fastResponses(t)
	h.up.set("json", `{"choices":[{"message":{"content":""},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	r := h.post(string(responsesEndpoint), responseBody(""))
	var response map[string]any
	if r.status != 200 || json.Unmarshal(r.body, &response) != nil || response["status"] != "incomplete" {
		t.Fatalf("filtered result: %d %s", r.status, r.body)
	}
	if response["incomplete_details"].(map[string]any)["reason"] != "content_filter" {
		t.Fatal(response)
	}
	if ev := h.rec.last(t); ev.Meters[0].Charged != 6 {
		t.Fatalf("filter lost engine usage: %+v", ev)
	}
}

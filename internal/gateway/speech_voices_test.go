package gateway

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestSpeechScriptShare(t *testing.T) {
	for _, tc := range []struct {
		text string
		zh   bool
	}{
		{"你好，今天过得怎么样？", true}, {"Hello, how are you?", false},
		{"中文好abcdefg", true}, {"中文abcdefgh", false},
		{"123 中文好!!! abcdefg 🙂", true}, {"1234 !? 🙂", false},
		{"㐀ab", true}, {"豈ab", true}, {"𠀀ab", true}, {"", false},
	} {
		if got := speechIsChinese(tc.text); got != tc.zh {
			t.Errorf("%q: Chinese=%v, want %v", tc.text, got, tc.zh)
		}
	}
}

func TestSpeechVoiceOnlyFillsAbsentField(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		voices      map[string]string
		field       string
		want        any
		present     bool
	}{
		{"Chinese", "你好", map[string]string{"zh": "zf_xiaoxiao", "default": "af_heart"}, "", "zf_xiaoxiao", true},
		{"English mapping first", "Hello", map[string]string{"en": "af_heart", "default": "bf_emma"}, "", "af_heart", true},
		{"Chinese does not use English mapping", "你好", map[string]string{"en": "af_heart"}, "", nil, false},
		{"other script", "Hello", map[string]string{"zh": "zf_xiaoxiao", "default": "af_heart"}, "", "af_heart", true},
		{"missing Chinese", "你好", map[string]string{"default": "af_heart"}, "", "af_heart", true},
		{"unmapped", "Hello", map[string]string{"zh": "zf_xiaoxiao"}, "", nil, false},
		{"disabled", "你好", nil, "", nil, false},
		{"named", "你好", map[string]string{"zh": "zf_xiaoxiao"}, `,"voice":"named-voice"`, "named-voice", true},
		{"empty", "你好", map[string]string{"zh": "zf_xiaoxiao"}, `,"voice":""`, "", true},
		{"null", "你好", map[string]string{"zh": "zf_xiaoxiao"}, `,"voice":null`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, _ := audioEngine(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				voice, present := body["voice"]
				if present != tc.present || !reflect.DeepEqual(voice, tc.want) {
					t.Errorf("voice=%#v present=%v; want %#v/%v", voice, present, tc.want, tc.present)
				}
				if body["input"] != tc.input || body["model"] != "m1" || body["speed"] != 1.25 {
					t.Errorf("other fields changed: %v", body)
				}
				w.Header().Set("Content-Type", "audio/wav")
				_, _ = w.Write(wave(1))
			})
			h := newHarness(t, Config{Speech: engine, SpeechVoices: tc.voices}, nil)
			input, _ := json.Marshal(tc.input)
			raw := []byte(`{"model":"m1","input":` + string(input) + `,"speed":1.25` + tc.field + `}`)
			if r := postAudio(t, h, string(speechEndpoint), "application/json", raw); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
}

package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSpeechVoiceFlagParsing(t *testing.T) {
	for _, raw := range []string{"", "  "} {
		got, err := parseSpeechVoices(raw)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty map: %v %v", got, err)
		}
	}
	got, err := parseSpeechVoices(" zh=zf_xiaoxiao, en=af_heart, default=bf_emma ")
	if err != nil || !reflect.DeepEqual(got, map[string]string{"zh": "zf_xiaoxiao", "en": "af_heart", "default": "bf_emma"}) {
		t.Fatalf("map: %v %v", got, err)
	}
	for _, raw := range []string{"zh", "=voice", "zh=", "zh=a,", "zh=a,,en=b", "zh=a,zh=b", "unknown=a"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			r := exec(t, testPlatform(fakeAddr, nil), "serve", "--data-dir", dir, "--upstream-speech-voices", raw)
			if r.code == 0 || !strings.Contains(r.err, "--upstream-speech-voices") {
				t.Fatalf("exit %d: %s", r.code, r.err)
			}
			if _, err := os.Stat(configPath(dir)); !os.IsNotExist(err) {
				t.Fatalf("invalid mapping wrote configuration: %v", err)
			}
		})
	}
}

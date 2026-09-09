package keys

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyAudioDefaultsAreReadOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.Add(context.Background(), "legacy", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	for _, entry := range doc["keys"].([]any) {
		l := entry.(map[string]any)["limits"].(map[string]any)
		delete(l, "daily_audio_seconds")
		delete(l, "daily_speech_chars")
	}
	before, _ := json.Marshal(doc)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	k, ok, err := s.Lookup(context.Background(), secret)
	if err != nil || !ok || k.Limits.DailyAudioSeconds != 3600 || k.Limits.DailySpeechChars != 200000 {
		t.Fatal("legacy defaults not applied")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("read rewrote keys")
	}
}

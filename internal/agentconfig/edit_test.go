package agentconfig

import (
	"bytes"
	"strings"
	"testing"
)

func TestDSHSpliceRoundTrip(t *testing.T) {
	for _, original := range []string{
		"", "# a person's comment\n", "other:\n  enabled: true\n",
		"llm-pi-ai:\n  other: true\n",
		"llm-pi-ai:\n  providers:\n    existing:\n      api: openai-completions\nother: true\n",
		"'llm-pi-ai':\n    providers:\n        'existing': {api: openai-completions}\n",
		"llm-pi-ai:\n  providers:",
		"llm-pi-ai:\r\n  providers:\r\n    existing: {}\r\n# tail\r\n",
	} {
		t.Run(strings.ReplaceAll(original, "\n", "/"), func(t *testing.T) {
			block, at, err := dshBlock([]byte(original), []byte(`{"api":"openai-completions","models":[{"id":"a:b/# quote\""}]}`))
			if err != nil {
				t.Fatal(err)
			}
			got := append(append(append([]byte{}, original[:at]...), block...), original[at:]...)
			if err := validYAML(got); err != nil {
				t.Fatalf("invalid inserted document: %v\n%s", err, got)
			}
			if !bytes.Contains(got, []byte("infercat:")) {
				t.Fatalf("no provider: %s", got)
			}
			restored := bytes.Replace(got, block, nil, 1)
			if !bytes.Equal(restored, []byte(original)) {
				t.Fatalf("outside bytes changed: %q", restored)
			}
		})
	}
}

func TestDSHRefusesAmbiguousStructures(t *testing.T) {
	for _, original := range []string{
		"llm-pi-ai: {}\n", "llm-pi-ai:\n  providers: {}\n", "{\"llm-pi-ai\":{\"providers\":{}}}",
		"llm-pi-ai:\n  providers:\n    infercat: {}\n",
		"llm-pi-ai:\n  providers: []\n", "llm-pi-ai: nope\n",
		"llm-pi-ai:\n  providers: null\n", "llm-pi-ai:\n  providers: &shared\n    existing: {}\n",
		"llm-pi-ai:\n  providers:\n    <<: {infercat: {}}\n",
		"llm-pi-ai:\n  providers:\n    x: {}\n    x: {}\n",
		"llm-pi-ai:\n  providers:\n---\nother: true\n", "- sequence\n",
	} {
		t.Run(original, func(t *testing.T) {
			if _, _, err := dshBlock([]byte(original), []byte(`{}`)); err == nil {
				t.Fatal("unsafe shape accepted")
			}
		})
	}
}

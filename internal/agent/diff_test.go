package agent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test116EUnifiedDiff(t *testing.T) {
	for _, tt := range []struct{ name, before, after, want string }{
		{"existing", "first\nold\nlast\n", "first\nnew\nlast\n", "--- before\n+++ after\n@@ -1,3 +1,3 @@\n first\n-old\n+new\n last\n"},
		{"new", "", "hello\nworld\n", "--- before\n+++ after\n@@ -0,0 +1,2 @@\n+hello\n+world\n"},
		{"empty", "old\n", "", "--- before\n+++ after\n@@ -1,1 +0,0 @@\n-old\n"},
		{"unterminated", "old", "new", "--- before\n+++ after\n@@ -1,1 +1,1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(writeDiff([]byte(tt.before), []byte(tt.after))); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func Test116EDiffBoundsAndContext(t *testing.T) {
	for _, data := range [][]byte{[]byte("binary\x00data"), {0xff}, bytes.Repeat([]byte("x"), diffLimit+1)} {
		if writeDiff(data, []byte("new")) != nil || writeDiff([]byte("old"), data) != nil {
			t.Fatal("unsupported version received diff")
		}
	}
	if writeDiff([]byte("same"), []byte("same")) != nil {
		t.Fatal("unchanged received diff")
	}
	before := strings.Repeat("same\n", 10) + "old\n" + strings.Repeat("same\n", 10)
	after := strings.Replace(before, "old\n", "new\n", 1)
	got := string(writeDiff([]byte(before), []byte(after)))
	if !strings.Contains(got, "@@ -8,7 +8,7 @@") || strings.Count(got, " same\n") != 6 {
		t.Fatal(got)
	}
	large := writeDiff(bytes.Repeat([]byte("old\n"), 16000), bytes.Repeat([]byte("new\n"), 16000))
	if len(large) > diffLimit || !bytes.HasSuffix(large, []byte("\\ Diff truncated at 64 KiB; display only\n")) {
		t.Fatal("missing bounded truncation", len(large))
	}
}

func Test116EPriorCaptureConfinedAndBounded(t *testing.T) {
	root := t.TempDir()
	if _, err := captureLimit(root, "absent", diffLimit); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureLimit(root, outside, diffLimit); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("outside path treated as empty", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := captureLimit(root, "link", diffLimit); err == nil {
		t.Fatal("outside symlink read")
	}
	if err := os.WriteFile(filepath.Join(root, "large"), bytes.Repeat([]byte("x"), diffLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureLimit(root, "large", diffLimit); err == nil {
		t.Fatal("oversized prior read")
	}
}

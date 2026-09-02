//go:build !js

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestBuildTags checks that build-tags.txt is one sorted comma-separated line of plain tags
// (what build.sh passes to -tags) and, unless -short, that the bridge compiles for js/wasm with
// exactly those tags. The compile is cached by the Go build cache after the first run.
func TestBuildTags(t *testing.T) {
	b, err := os.ReadFile("build-tags.txt")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(string(b), "\n")
	if strings.ContainsAny(line, " \t\r\n") || line == "" {
		t.Fatalf("build-tags.txt must be one line with no whitespace: %q", line)
	}
	tags := strings.Split(line, ",")
	if !slices.IsSorted(tags) || len(slices.Compact(slices.Clone(tags))) != len(tags) {
		t.Fatal("build-tags.txt must be sorted and unique")
	}
	tagRx := regexp.MustCompile(`^[a-z0-9_]+$`)
	for _, tag := range tags {
		if !tagRx.MatchString(tag) {
			t.Fatalf("bad tag %q", tag)
		}
	}
	for _, must := range []string{"netgo", "osusergo"} {
		if !slices.Contains(tags, must) {
			t.Errorf("missing base tag %q", must)
		}
	}
	if slices.Contains(tags, "ts_omit_netstack") {
		t.Error("netstack must not be omitted: the browser has no kernel TCP stack")
	}
	if testing.Short() {
		t.Skip("-short: skipping the js/wasm compile")
	}
	out := filepath.Join(t.TempDir(), "bunny.wasm")
	cmd := exec.Command("go", "build", "-tags", line, "-o", out, ".")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("js/wasm build with pinned tags failed: %v\n%s", err, msg)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bunny.wasm: %d bytes (unstripped, uncompressed)", fi.Size())
}

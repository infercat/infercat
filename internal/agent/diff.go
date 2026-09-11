package agent

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

const diffLimit = 64 << 10

// One contextual unified hunk; bounded inputs avoid an unbounded edit matrix.
func writeDiff(before, after []byte) []byte {
	for _, data := range [][]byte{before, after} {
		if len(data) > diffLimit || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return nil
		}
	}
	if bytes.Equal(before, after) {
		return nil
	}
	lines := func(data []byte) []string {
		parts := strings.SplitAfter(string(data), "\n")
		if parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		return parts
	}
	old, next := lines(before), lines(after)
	prefix := 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(next)-prefix && old[len(old)-1-suffix] == next[len(next)-1-suffix] {
		suffix++
	}
	start := max(0, prefix-3)
	oldEnd, newEnd := min(len(old), len(old)-suffix+3), min(len(next), len(next)-suffix+3)
	oldStart, newStart := start+1, start+1
	if oldEnd == start {
		oldStart = start
	}
	if newEnd == start {
		newStart = start
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- before\n+++ after\n@@ -%d,%d +%d,%d @@\n", oldStart, oldEnd-start, newStart, newEnd-start)
	marker := "\\ Diff truncated at 64 KiB; display only\n"
	add := func(sign string, line string) bool {
		tail := ""
		if !strings.HasSuffix(line, "\n") {
			tail = "\n\\ No newline at end of file\n"
		}
		if out.Len()+len(sign)+len(line)+len(tail) > diffLimit-len(marker) {
			out.WriteString(marker)
			return false
		}
		out.WriteString(sign)
		out.WriteString(line)
		out.WriteString(tail)
		return true
	}
	for _, line := range old[start:prefix] {
		if !add(" ", line) {
			return []byte(out.String())
		}
	}
	for _, line := range old[prefix : len(old)-suffix] {
		if !add("-", line) {
			return []byte(out.String())
		}
	}
	for _, line := range next[prefix : len(next)-suffix] {
		if !add("+", line) {
			return []byte(out.String())
		}
	}
	for _, line := range old[len(old)-suffix : oldEnd] {
		if !add(" ", line) {
			break
		}
	}
	return []byte(out.String())
}

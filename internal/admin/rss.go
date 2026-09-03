package admin

import (
	"os"
	"strconv"
	"strings"
)

// rss is the process's resident set size where the kernel publishes it without a system call
// the Go runtime lacks: /proc/self/statm on Linux. Elsewhere it is 0 and the surfaces say so.
func rss() int64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.ParseInt(f[1], 10, 64)
	return pages * int64(os.Getpagesize())
}

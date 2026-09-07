package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/usage"
)

func (e *env) cmdUsage(ctx context.Context, pre string, args []string) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	key := fs.String("key", "", "only this key id or name")
	since := fs.String("since", "24h", "how far back to look: 30m, 24h, 7d, or all")
	if err := e.parse(fs, usageHelp, args); err != nil {
		return err
	}
	dataDir, err := resolveDataDir(*dd)
	if err != nil {
		return err
	}
	window, err := parseSince(*since)
	if err != nil {
		return err
	}

	names := map[string]string{}
	filter := usage.Filter{}
	if window > 0 {
		filter.Since = time.Now().Add(-window)
	}
	store, serr := keys.NewFileStore(dataDir)
	if serr == nil {
		if list, err := store.List(ctx); err == nil {
			for _, k := range list {
				names[k.ID] = k.Name
			}
		}
		if *key != "" {
			k, err := store.Find(ctx, *key)
			if err != nil {
				return err
			}
			filter.KeyID = k.ID
		}
	} else if *key != "" {
		filter.KeyID = *key
	}

	rep, err := usage.AggregateFile(dataDir, filter)
	if err != nil {
		return err
	}
	e.writeUsage(rep, *since, filter.KeyID, names)
	return nil
}

func (e *env) writeUsage(rep *usage.Report, since, keyID string, names map[string]string) {
	scope := "all keys"
	if keyID != "" {
		scope = keyID
		if n := names[keyID]; n != "" {
			scope += " (" + n + ")"
		}
	}
	t := rep.Total
	fmt.Fprintf(e.out, "usage · last %s · %s\n\n", since, scope)
	if t.Requests == 0 {
		fmt.Fprintln(e.out, "nothing yet")
		return
	}
	fmt.Fprintf(e.out, "requests  %s%s%s\n", calls(t.ModelCalls), polls(t.AppPolls), errs(t.Errors, t.ErrorsByCode))
	fmt.Fprintf(e.out, "tokens    %s prompt  %s completion\n", comma(t.PromptTokens), comma(t.CompletionTokens))
	fmt.Fprintf(e.out, "ttft      median %s  p95 %s\n", ms(t.TTFTMedianMS), ms(t.TTFTP95MS))
	fmt.Fprintf(e.out, "total     median %s  p95 %s   (successful model calls only)\n", ms(t.TotalMedianMS), ms(t.TotalP95MS))
	fmt.Fprintf(e.out, "counts    %s\n", countsLine)
	if rep.Malformed > 0 {
		fmt.Fprintf(e.out, "\n%d unreadable line(s) in the log were skipped\n", rep.Malformed)
	}
	if keyID != "" || len(rep.Keys) == 0 {
		return
	}
	fmt.Fprintln(e.out)
	tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCALLS\tPOLLS\tERR\tPROMPT\tCOMPLETION\tTTFT p50\tTOTAL p50\tLAST SEEN")
	for _, k := range rep.Keys {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			orDash(k.KeyID), orDash(names[k.KeyID]), k.ModelCalls, k.AppPolls, k.Errors,
			comma(k.PromptTokens), comma(k.CompletionTokens),
			ms(k.TTFTMedianMS), ms(k.TotalMedianMS), ago(k.LastSeen))
	}
	tw.Flush()
}

// calls, polls and errs write the headline the way a host reads it: what the friend did, then what
// their browser did on its own, then what went wrong. A web app polls /me every 30 s, and counting
// those as requests is what made one conversation read as 58 (ticket 009 promise 6).
func calls(n int) string {
	if n == 1 {
		return "1 model call"
	}
	return fmt.Sprintf("%d model calls", n)
}

func polls(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (+%d app polls)", n)
}

func errs(n int, byCode map[string]int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" — %s%s", plural(n, "error"), errCodes(byCode))
}

func errCodes(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	codes := make([]string, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool {
		if m[codes[i]] != m[codes[j]] {
			return m[codes[i]] > m[codes[j]]
		}
		return codes[i] < codes[j]
	})
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, fmt.Sprintf("%s %d", c, m[c]))
	}
	return ": " + strings.Join(parts, ", ")
}

// parseSince accepts a Go duration, a day count like 7d, or "all" (0 = no lower bound).
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "all" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err == nil && n >= 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("--since %q: want a duration like 30m, 24h, 7d, or all", s)
	}
	return d, nil
}

func ms(v int64) string {
	if v <= 0 {
		return "—"
	}
	if v < 1000 {
		return fmt.Sprintf("%d ms", v)
	}
	return fmt.Sprintf("%.1f s", float64(v)/1000)
}

// comma groups thousands so a six-digit token count is readable at a glance.
func comma(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

const usageHelp = `Usage: infercat usage [--key ID] [--since 24h] [--data-dir DIR]

Reads usage.jsonl and totals it: model calls (what your friends asked the engine for) and app
polls (what their browser did on its own), errors by code, tokens, and the median and p95 of
time-to-first-token and total time over successful model calls. Works whether or not the host is
running.

  infercat usage
  infercat usage --since 7d
  infercat usage --key alice --since all

Flags:
  --key ID       one key id or name
  --since D      30m, 24h, 7d, or all   (default 24h)
`

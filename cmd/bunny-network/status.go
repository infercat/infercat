package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
	"github.com/2185Lab/bunny-network/internal/product"
)

func (e *env) cmdStatus(ctx context.Context, pre string, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	if err := e.parse(fs, statusHelp, args); err != nil {
		return err
	}
	dataDir, err := resolveDataDir(*dd)
	if err != nil {
		return err
	}
	st, err := admin.Fetch(ctx, dataDir)
	if errors.Is(err, admin.ErrNoDaemon) {
		return fmt.Errorf("no host is running for %s — start one with `%s serve`", dataDir, product.CLIName)
	}
	if err != nil {
		return err
	}
	writeStatus(e.out, st)
	return nil
}

func writeStatus(w io.Writer, st admin.Status) {
	fmt.Fprintf(w, "%s %s — up %s\n", st.Product, st.Version, shortDur(time.Duration(st.UptimeS)*time.Second))
	fmt.Fprintf(w, "upstream  %s  %s  %s  context %s  slots %d\n",
		st.Upstream.Kind, st.Upstream.URL, healthWord(st.Upstream.Healthy),
		contextStr(st.Upstream.ModelContext), st.Upstream.Slots)
	fmt.Fprintf(w, "tunnel    %s  relay %s  %s\n", orDash(st.Tunnel.Addr), orDash(st.Tunnel.Region), plural(st.Tunnel.Clients, "client"))
	fmt.Fprintf(w, "queue     %d in flight, %d waiting\n", st.Queue.InFlight, st.Queue.Waiting)
	if len(st.Keys) == 0 {
		fmt.Fprintf(w, "\nno keys yet — `%s keys add <name>` mints one\n", product.CLIName)
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tIN FLIGHT\tRPM\tTODAY\tLAST SEEN")
	for _, k := range st.Keys {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n", k.ID, k.Name, k.Status, k.InFlight, k.RPMUsed, k.TodayTokens, ago(k.LastSeen))
	}
	tw.Flush()
}

func healthWord(ok bool) string {
	if ok {
		return "healthy"
	}
	return "NOT ANSWERING"
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// shortDur renders an uptime the way a person reads it.
func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

const statusHelp = `Usage: bunny-network status [--data-dir DIR]

What the running host is doing right now: the upstream, the tunnel address and relay, the queue,
and every key with its live counters. Reads the admin socket in the data dir; exits non-zero when
no host is running.

  bunny-network status
`

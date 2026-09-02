package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/product"
	"github.com/2185Lab/bunny-network/internal/usage"
)

func (e *env) cmdKeys(ctx context.Context, pre string, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(e.errw, keysHelp)
		return errUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return e.keysAdd(ctx, pre, rest)
	case "list", "ls":
		return e.keysList(ctx, pre, rest)
	case "pause":
		return e.keysStatus(ctx, pre, rest, keys.Paused, "paused")
	case "resume":
		return e.keysStatus(ctx, pre, rest, keys.Active, "active")
	case "revoke":
		return e.keysStatus(ctx, pre, rest, keys.Revoked, "revoked")
	case "rotate":
		return e.keysRotate(ctx, pre, rest)
	case "limits":
		return e.keysLimits(ctx, pre, rest)
	case "-h", "--help", "help":
		fmt.Fprint(e.out, keysHelp)
		return errDone
	default:
		fmt.Fprintf(e.errw, "%s: unknown keys subcommand %q\n\n", product.CLIName, sub)
		fmt.Fprint(e.errw, keysHelp)
		return errUsage
	}
}

// limitFlags registers the seven limit flags and returns a reader that applies only the ones the
// host actually typed, so `keys limits ID --rpm 60` leaves everything else alone.
func limitFlags(fs *flag.FlagSet) func(base keys.Limits) keys.Limits {
	rpm := fs.Int("rpm", 0, "requests per minute")
	tpm := fs.Int("tpm", 0, "tokens per minute (prompt + completion)")
	conc := fs.Int("max-concurrent", 0, "requests this friend may have in flight")
	outTok := fs.Int("max-output-tokens", 0, "clamp on max_tokens")
	maxCtx := fs.Int("max-context", 0, "context ceiling; 0 = the upstream's")
	daily := fs.Int("daily-tokens", 0, "tokens per UTC day")
	models := fs.String("models", "", "comma-separated model allowlist; empty = every model")
	return func(l keys.Limits) keys.Limits {
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "rpm":
				l.RPM = *rpm
			case "tpm":
				l.TPM = *tpm
			case "max-concurrent":
				l.MaxConcurrent = *conc
			case "max-output-tokens":
				l.MaxOutputTokens = *outTok
			case "max-context":
				l.MaxContext = *maxCtx
			case "daily-tokens":
				l.DailyTokens = *daily
			case "models":
				l.Models = splitModels(*models)
			}
		})
		return l
	}
}

func splitModels(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// open parses a keys subcommand's flags, resolves the data dir, and opens the store. reg
// registers any extra flags before parsing. want is how many positional arguments the verb
// takes; a mismatch prints help and is a usage error.
func (e *env) open(verb, help, pre string, args []string, want int, reg func(*flag.FlagSet)) (*keys.FileStore, string, []string, error) {
	fs := flag.NewFlagSet("keys "+verb, flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	if reg != nil {
		reg(fs)
	}
	if err := e.parse(fs, help, args); err != nil {
		return nil, "", nil, err
	}
	if fs.NArg() != want {
		fmt.Fprint(e.errw, help)
		return nil, "", nil, errUsage
	}
	dataDir, err := resolveDataDir(*dd)
	if err != nil {
		return nil, "", nil, err
	}
	store, err := keys.NewFileStore(dataDir)
	if err != nil {
		return nil, "", nil, err
	}
	return store, dataDir, fs.Args(), nil
}

func (e *env) keysAdd(ctx context.Context, pre string, args []string) error {
	var apply func(keys.Limits) keys.Limits
	store, dataDir, pos, err := e.open("add", keysAddHelp, pre, args, 1, func(fs *flag.FlagSet) { apply = limitFlags(fs) })
	if err != nil {
		return err
	}
	// Resolve the address before minting: a key whose invite cannot be printed is worse than no
	// key at all, because the secret is only ever shown here.
	addr, err := e.hostAddr(ctx, dataDir)
	if err != nil {
		return err
	}
	k, secret, err := store.Add(ctx, pos[0], apply(keys.Limits{}))
	if err != nil {
		return err
	}
	inv := e.plat.encodeInvite(addr, secret)
	fmt.Fprintf(e.out, "key %s  %s\n%s\n\n", k.ID, k.Name, limitsLine(k.Limits))
	fmt.Fprintf(e.out, "Invite for %s — it is shown once and stored only as a hash:\n\n  %s\n\n", k.Name, inv)
	writeQR(e.out, inv)
	fmt.Fprintf(e.out, "\nLost it? `%s keys rotate %s` issues a new one and retires this.\n", product.CLIName, k.ID)
	return nil
}

// hostAddr asks the running host first, then the saved host key.
func (e *env) hostAddr(ctx context.Context, dataDir string) (string, error) {
	if st, err := admin.Fetch(ctx, dataDir); err == nil && st.Tunnel.Addr != "" {
		return st.Tunnel.Addr, nil
	}
	if addr, err := e.plat.savedAddr(dataDir); err == nil && addr != "" {
		return addr, nil
	}
	return "", fmt.Errorf("this host has no address yet — run `%s serve` once first", product.CLIName)
}

func (e *env) keysList(ctx context.Context, pre string, args []string) error {
	store, dataDir, _, err := e.open("list", keysHelp, pre, args, 0, nil)
	if err != nil {
		return err
	}
	list, err := store.List(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(e.out, "no keys yet — `%s keys add <name>` mints one\n", product.CLIName)
		return nil
	}
	rep, err := usage.AggregateFile(dataDir, usage.Filter{})
	if err != nil {
		return err
	}
	seen := rep.LastSeen()
	tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tRPM\tTPM\tDAILY\tCREATED\tLAST SEEN")
	for _, k := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			k.ID, k.Name, k.Status,
			limitNum(k.Limits.RPM), limitNum(k.Limits.TPM), limitNum(k.Limits.DailyTokens),
			k.CreatedAt.Local().Format("2006-01-02"), ago(seen[k.ID]))
	}
	return tw.Flush()
}

func (e *env) keysStatus(ctx context.Context, pre string, args []string, st keys.Status, word string) error {
	store, _, pos, err := e.open(word, keysHelp, pre, args, 1, nil)
	if err != nil {
		return err
	}
	k, err := store.Find(ctx, pos[0])
	if err != nil {
		return err
	}
	if err := store.SetStatus(ctx, k.ID, st); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "%s (%s) is now %s\n", k.ID, k.Name, word)
	return nil
}

func (e *env) keysRotate(ctx context.Context, pre string, args []string) error {
	store, dataDir, pos, err := e.open("rotate", keysHelp, pre, args, 1, nil)
	if err != nil {
		return err
	}
	addr, err := e.hostAddr(ctx, dataDir)
	if err != nil {
		return err
	}
	k, err := store.Find(ctx, pos[0])
	if err != nil {
		return err
	}
	secret, err := store.Rotate(ctx, k.ID)
	if err != nil {
		return err
	}
	inv := e.plat.encodeInvite(addr, secret)
	fmt.Fprintf(e.out, "key %s  %s — the previous invite no longer works.\n\n  %s\n\n", k.ID, k.Name, inv)
	writeQR(e.out, inv)
	return nil
}

func (e *env) keysLimits(ctx context.Context, pre string, args []string) error {
	var apply func(keys.Limits) keys.Limits
	store, _, pos, err := e.open("limits", keysLimitsHelp, pre, args, 1, func(fs *flag.FlagSet) { apply = limitFlags(fs) })
	if err != nil {
		return err
	}
	k, err := store.Find(ctx, pos[0])
	if err != nil {
		return err
	}
	next := apply(k.Limits)
	if err := store.SetLimits(ctx, k.ID, next); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "key %s  %s\n%s\n", k.ID, k.Name, limitsLine(next))
	return nil
}

func limitsLine(l keys.Limits) string {
	models := "all models"
	if len(l.Models) > 0 {
		models = strings.Join(l.Models, ", ")
	}
	ctx := "the upstream's context" // MaxContext 0 means "whatever the engine allows"
	if l.MaxContext > 0 {
		ctx = fmt.Sprintf("%d context", l.MaxContext)
	}
	return fmt.Sprintf("limits: %s rpm · %s tpm · %s concurrent · %s max output · %s · %s tokens/day · %s",
		limitNum(l.RPM), limitNum(l.TPM), limitNum(l.MaxConcurrent), limitNum(l.MaxOutputTokens),
		ctx, limitNum(l.DailyTokens), models)
}

// limitNum renders 0 as the word the host means by it.
func limitNum(n int) string {
	if n <= 0 {
		return "unlimited"
	}
	return fmt.Sprint(n)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Local().Format("2006-01-02")
	}
}

const keysHelp = `Usage: bunny-network keys <subcommand>

One key is one person. Limits live on the key.

  bunny-network keys add alice              # mint alice and print her invite, once
  bunny-network keys list
  bunny-network keys pause alice            # she gets 403 until you resume
  bunny-network keys rotate alice           # new invite; the old one stops working
  bunny-network keys limits alice --rpm 60

Subcommands:
  add NAME [limits]   mint a key and print the invite and its QR code
  list                every key with its limits and when it was last seen
  pause ID            stop a key answering, keep it
  resume ID           undo pause
  revoke ID           stop it for good
  rotate ID           issue a new secret, keeping id, name, and limits
  limits ID [limits]  change limits; only the flags you pass change

ID is a key id (k_7f3a2b) or a key name when that name is unique.
Limit flags: --rpm --tpm --max-concurrent --max-output-tokens --max-context --daily-tokens --models
`

const keysAddHelp = `Usage: bunny-network keys add NAME [limit flags]

Mints a key for one person and prints their invite — the whole thing they need to paste.
The secret is shown here and never again; only its hash is stored.

  bunny-network keys add alice
  bunny-network keys add bob --rpm 60 --daily-tokens 500000
  bunny-network keys add carol --models gemma-4-E2B-it-Q4_K_M.gguf

Defaults: 20 rpm · 20000 tpm · 1 concurrent · 2048 max output · the upstream's context ·
200000 tokens/day · every model.

Limit flags:
  --rpm N                 requests per minute
  --tpm N                 tokens per minute (prompt + completion)
  --max-concurrent N      requests in flight at once
  --max-output-tokens N   clamp on max_tokens
  --max-context N         context ceiling (0 = the upstream's)
  --daily-tokens N        tokens per UTC day
  --models a,b            model allowlist (empty = every model)
  --data-dir DIR          where keys, usage, and the host key live
`

const keysLimitsHelp = `Usage: bunny-network keys limits ID [limit flags]

Changes limits on an existing key. Only the flags you pass change; 0 means "no limit".

  bunny-network keys limits alice --rpm 60 --daily-tokens 1000000
  bunny-network keys limits k_7f3a2b --models gemma-4-E2B-it-Q4_K_M.gguf

Limit flags: --rpm --tpm --max-concurrent --max-output-tokens --max-context --daily-tokens --models
`

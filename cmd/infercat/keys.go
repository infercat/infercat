package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/product"
	"github.com/infercat/infercat/internal/usage"
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

// limitFlags registers the nine limit flags and returns a reader that applies only the ones the
// host actually typed, so `keys limits ID --rpm 60` leaves everything else alone.
func limitFlags(fs *flag.FlagSet) func(base keys.Limits) keys.Limits {
	rpm := fs.Int("rpm", 0, "requests per minute")
	tpm := fs.Int("tpm", 0, "tokens per minute (prompt + completion)")
	conc := fs.Int("max-concurrent", 0, "requests this friend may have in flight")
	outTok := fs.Int("max-output-tokens", 0, "clamp on max_tokens")
	maxCtx := fs.Int("max-context", 0, "context ceiling; 0 = the upstream's")
	daily := fs.Int("daily-tokens", 0, "tokens per UTC day")
	audio := fs.Int("daily-audio-seconds", 0, "transcription seconds per UTC day")
	images := fs.Int("daily-images", 0, "images per UTC day (default 20)")
	queuedImages := fs.Int("max-queued-images", 0, "queued images per key (default 8)")
	speech := fs.Int("daily-speech-chars", 0, "speech Unicode code points per UTC day")
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
			case "daily-audio-seconds":
				l.DailyAudioSeconds = *audio
			case "daily-images":
				l.DailyImages = *images
			case "max-queued-images":
				l.MaxQueuedImages = *queuedImages
			case "daily-speech-chars":
				l.DailySpeechChars = *speech
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
	var force, asJSON, noQR *bool
	store, dataDir, pos, err := e.open("add", keysAddHelp, pre, args, 1, func(fs *flag.FlagSet) {
		apply = limitFlags(fs)
		force = fs.Bool("force", false, "mint a second key for a name that already has one")
		asJSON = fs.Bool("json", false, "print only the machine-readable invite object")
		noQR = fs.Bool("no-qr", false, "do not draw the QR code")
	})
	if err != nil {
		return err
	}
	if !*force {
		if err := e.refuseDuplicate(ctx, store, dataDir, pos[0]); err != nil {
			return err
		}
	}
	// Resolve the address before minting: a key whose invite cannot be printed is worse than no
	// key at all, because the secret is only ever shown here.
	addr, err := e.hostAddr(ctx, dataDir)
	if err != nil {
		return err
	}
	k, inv, err := e.mintKey(ctx, store, addr, pos[0], apply(keys.Limits{}))
	if err != nil {
		return err
	}
	e.reloadHost(ctx, dataDir)
	if *asJSON {
		return e.printInviteJSON(dataDir, k, inv)
	}
	fmt.Fprintf(e.out, "key %s  %s\n%s\n\n", k.ID, k.Name, limitsLine(k.Limits))
	fmt.Fprintf(e.out, "Invite for %s — it is shown once and stored only as a hash:\n\n  %s\n\n", k.Name, inv)
	e.printDestination(dataDir, k.Name, inv, *noQR)
	fmt.Fprintf(e.out, "\nLost it? `%s keys rotate %s` issues a new one and retires this.\n", product.CLIName, k.ID)
	return nil
}

// refuseDuplicate stops the obvious mistake after losing an invite in scrollback: `keys add alice`
// a second time used to mint a second alice, and from then on every `keys pause alice` refused as
// ambiguous. A revoked namesake is not in the way (ticket 009 promise 3).
func (e *env) refuseDuplicate(ctx context.Context, store *keys.FileStore, dataDir, name string) error {
	list, err := store.List(ctx)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	for _, k := range list {
		if k.Name != name || k.Status == keys.Revoked {
			continue
		}
		seen := "never used"
		if rep, err := usage.AggregateFile(dataDir, usage.Filter{KeyID: k.ID}); err == nil {
			if t := rep.LastSeen()[k.ID]; !t.IsZero() {
				seen = "last seen " + ago(t)
			}
		}
		return fmt.Errorf("%s already has %s key (%s, %s).\nLost the invite? `%s keys rotate %s`. Second device? `%s keys add %s-laptop`.\n(--force mints a second key for this name anyway.)",
			name, article(string(k.Status)), k.ID, seen, product.CLIName, name, product.CLIName, name)
	}
	return nil
}

func article(word string) string {
	if strings.ContainsRune("aeiou", rune(word[0])) {
		return "an " + word
	}
	return "a " + word
}

// printDestination answers the question the invite creates — where does my friend paste this? —
// with a link when the host has a web app to point at, and with the honest alternative when
// nobody has hosted one yet (ticket 009 promise 2).
func (e *env) printDestination(dataDir, name, inv string, noQR bool) {
	code := inv
	if link := inviteLink(dataDir, inv); link != "" {
		fmt.Fprintf(e.out, "Send %s this link:\n\n  %s\n\n", name, link)
		code = link // the QR carries the link, so a phone camera finishes the job
	} else {
		fmt.Fprintf(e.out, "%s pastes this code into the web app (serve web/dist yourself for now — see README).\n\n", name)
	}
	if noQR || !e.tty {
		return
	}
	writeQR(e.out, code)
	// The QR is ~30 rows tall, so on a short terminal it pushes the line above it off the screen —
	// a host persona lost the invite that way. Print it once more underneath, in the same shape, so
	// the last thing left on screen is the thing to copy. Nothing is repeated when there is no QR.
	fmt.Fprintf(e.out, "\n  %s\n", code)
}

// inviteLink is the one thing a friend can be sent: the web app plus the invite in the fragment,
// which never reaches a server. Empty when this host has no web app to name.
func inviteLink(dataDir, inv string) string {
	cfg, err := loadConfig(dataDir)
	if err != nil {
		cfg = config{}
	}
	base := webURL(cfg)
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "#") + "#" + inv
}

// printInviteJSON is `keys add --json`: the four fields a script needs and nothing else, so the
// invite can be handed to a chat bot or a provisioning script without scraping the human output.
func (e *env) printInviteJSON(dataDir string, k *keys.Key, inv string) error {
	enc := json.NewEncoder(e.out)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		KeyID  string `json:"key_id"`
		Name   string `json:"name"`
		Invite string `json:"invite"`
		Link   string `json:"link"`
	}{k.ID, k.Name, inv, inviteLink(dataDir, inv)})
}

// reloadHost pushes a key change to the running host over the admin socket so it is in force
// before the command returns, instead of within the store's once-per-second re-read. No running
// host is not a problem: keys.json is the truth either way (ticket 009 promise 9).
func (e *env) reloadHost(ctx context.Context, dataDir string) {
	if err := admin.Reload(ctx, dataDir); err != nil && !errors.Is(err, admin.ErrNoDaemon) {
		e.logf("the running host did not accept the change yet (%v); it will re-read within a second", err)
	}
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
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tRPM\tTPM\tDAILY\tAUDIO S/DAY\tSPEECH CHARS/DAY\tIMAGES/DAY\tIMAGE QUEUE\tCREATED\tLAST SEEN")
	for _, k := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			k.ID, k.Name, k.Status,
			limitNum(k.Limits.RPM), limitNum(k.Limits.TPM), limitNum(k.Limits.DailyTokens), limitNum(k.Limits.DailyAudioSeconds), limitNum(k.Limits.DailySpeechChars), limitNum(k.Limits.DailyImages), limitNum(k.Limits.MaxQueuedImages),
			k.CreatedAt.Local().Format("2006-01-02"), ago(seen[k.ID]))
	}
	return tw.Flush()
}

func (e *env) keysStatus(ctx context.Context, pre string, args []string, st keys.Status, word string) error {
	var yes *bool
	reg := func(fs *flag.FlagSet) {}
	if st == keys.Revoked {
		reg = func(fs *flag.FlagSet) { yes = fs.Bool("yes", false, "revoke without asking") }
	}
	store, dataDir, pos, err := e.open(word, keysHelp, pre, args, 1, reg)
	if err != nil {
		return err
	}
	k, err := store.Find(ctx, pos[0])
	if err != nil {
		return err
	}
	// Revoke is permanent and sits one word from pause in the same help block, so it asks —
	// naming the reversible alternative in the question (ticket 009 promise 9).
	if st == keys.Revoked && !*yes {
		fmt.Fprintf(e.out, "Revoking is permanent — %s would need a new invite.\n", k.Name)
		fmt.Fprintf(e.out, "`%s keys pause %s` stops them temporarily instead.\n", product.CLIName, k.Name)
		if !e.confirm(fmt.Sprintf("Revoke %s (%s)? [y/N] ", k.Name, k.ID)) {
			fmt.Fprintf(e.out, "nothing changed. Pass --yes to revoke without being asked.\n")
			return nil
		}
	}
	if err := store.SetStatus(ctx, k.ID, st); err != nil {
		return err
	}
	e.reloadHost(ctx, dataDir)
	fmt.Fprintf(e.out, "%s (%s) is now %s\n", k.ID, k.Name, word)
	return nil
}

// confirm asks a yes/no question. Anything but y/yes is no, and so is a stdin that cannot be read
// (a pipe, a script, a killed terminal): a permanent change is never made by default.
func (e *env) confirm(question string) bool {
	if e.in == nil {
		return false
	}
	fmt.Fprint(e.out, question)
	line, err := bufio.NewReader(e.in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(e.out)
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

func (e *env) keysRotate(ctx context.Context, pre string, args []string) error {
	var asJSON, noQR *bool
	store, dataDir, pos, err := e.open("rotate", keysHelp, pre, args, 1, func(fs *flag.FlagSet) {
		asJSON = fs.Bool("json", false, "print only the machine-readable invite object")
		noQR = fs.Bool("no-qr", false, "do not draw the QR code")
	})
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
	inv, err := e.rotateKey(ctx, store, addr, k.ID)
	if err != nil {
		return err
	}
	e.reloadHost(ctx, dataDir)
	if *asJSON {
		return e.printInviteJSON(dataDir, k, inv)
	}
	fmt.Fprintf(e.out, "key %s  %s — the previous invite no longer works.\n\n  %s\n\n", k.ID, k.Name, inv)
	e.printDestination(dataDir, k.Name, inv, *noQR)
	return nil
}

func (e *env) keysLimits(ctx context.Context, pre string, args []string) error {
	var apply func(keys.Limits) keys.Limits
	store, dataDir, pos, err := e.open("limits", keysLimitsHelp, pre, args, 1, func(fs *flag.FlagSet) { apply = limitFlags(fs) })
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
	e.reloadHost(ctx, dataDir)
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
	return fmt.Sprintf("limits: %s rpm · %s tpm · %s concurrent · %s max output · %s · %s tokens/day · %s\n        %s",
		limitNum(l.RPM), limitNum(l.TPM), limitNum(l.MaxConcurrent), limitNum(l.MaxOutputTokens),
		ctx, limitNum(l.DailyTokens), models, countsLine)
}

// countsLine is the one sentence that says what a key's numbers count, printed wherever a limit
// or a counter is shown so `keys`, `status`, and `usage` cannot drift apart. Checked against
// internal/gateway rather than assumed: models() takes the RPM entry (006 promise 5), /me takes
// nothing, and only a completion carries tokens.
const countsLine = "rpm counts model calls and the app's /v1/models lookup; tpm and tokens/day count model calls only"

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

const keysHelp = `Usage: infercat keys <subcommand>

One key is one person. Limits live on the key.

  infercat keys add alice              # mint alice and print her invite, once
  infercat keys list
  infercat keys pause alice            # she gets 403 until you resume
  infercat keys rotate alice           # new invite; the old one stops working
  infercat keys limits alice --rpm 60

Subcommands:
  add NAME [limits]   mint a key and print the invite and its QR code
  list                every key with its limits and when it last used the engine
  pause ID            stop a key answering, keep it
  resume ID           undo pause
  revoke ID           stop it for good (asks first; --yes skips the question)
  rotate ID           issue a new secret, keeping id, name, and limits
  limits ID [limits]  change limits; only the flags you pass change

An invite is ic1.<host address>.<secret>; the web app sends the secret as "Bearer <secret>".
ID is a key id (k_7f3a2b) or a key name when that name is unique.
Limit flags: --rpm --tpm --max-concurrent --max-output-tokens --max-context --daily-tokens --models
`

const keysAddHelp = `Usage: infercat keys add NAME [limit flags]

Mints a key for one person and prints their invite — the whole thing they need to paste.
The secret is shown here and never again; only its hash is stored.

  infercat keys add alice
  infercat keys add bob --rpm 60 --daily-tokens 500000
  infercat keys add carol --models gemma-4-E2B-it-Q4_K_M.gguf

Defaults: 20 rpm · 20000 tpm · 1 concurrent · 2048 max output · the upstream's context ·
200000 tokens/day · every model.

Limit flags:
  --rpm N                 requests per minute
  --tpm N                 tokens per minute (prompt + completion)
  --max-concurrent N      requests in flight at once
  --max-output-tokens N   clamp on max_tokens
  --max-context N         context ceiling (0 = the upstream's)
  --daily-tokens N        tokens per UTC day
  --daily-images N       images per UTC day (default 20)
  --max-queued-images N queued images per key (default 8)
  --daily-audio-seconds N transcription seconds per UTC day (default 3600)
  --daily-speech-chars N  speech code points per UTC day (default 200000)
  --models a,b            model allowlist (empty = every model); the host pin still applies

Other flags:
  --force                 mint a second key for a name that already has an active one
  --json                  print {"key_id","name","invite","link"} and nothing else
  --no-qr                 skip the QR code (it is also skipped when stdout is not a terminal)
  --data-dir DIR          where keys, usage, and the host key live
`

const keysLimitsHelp = `Usage: infercat keys limits ID [limit flags]

Changes limits on an existing key. Only the flags you pass change; 0 means "no limit".

  infercat keys limits alice --rpm 60 --daily-tokens 1000000
  infercat keys limits k_7f3a2b --models gemma-4-E2B-it-Q4_K_M.gguf

Limit flags: --rpm --tpm --max-concurrent --max-output-tokens --max-context --daily-tokens --models
`

// Mint and rotation share the exact secret-to-invite path with the admin API.
func (e *env) mintKey(ctx context.Context, store *keys.FileStore, addr, name string, limits keys.Limits) (*keys.Key, string, error) {
	k, secret, err := store.Add(ctx, name, limits)
	if err != nil {
		return nil, "", err
	}
	return k, e.plat.encodeInvite(addr, secret), nil
}
func (e *env) rotateKey(ctx context.Context, store *keys.FileStore, addr, id string) (string, error) {
	secret, err := store.Rotate(ctx, id)
	if err != nil {
		return "", err
	}
	return e.plat.encodeInvite(addr, secret), nil
}

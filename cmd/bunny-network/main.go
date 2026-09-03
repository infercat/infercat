// Command bunny-network is the host side: one command to serve, one to mint a friend, one to see
// what is happening.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/product"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// The tunnel (ticket 001) and the gateway (ticket 002) land in parallel with this CLI, so the
// command talks to them through these two small interfaces. wire.go binds them to the real
// packages; wire_stub.go stands in until those tickets land.

type tunnelStatus struct {
	Addr    string
	Region  string
	Started time.Time
	Clients int
}

type tunnelServer interface {
	Listener() net.Listener
	Addr() string
	Status() tunnelStatus
	Close() error
}

type tunnelOptions struct {
	DataDir    string
	Ephemeral  bool
	DERPMapURL string
	Region     string
	Logf       func(string, ...any)
}

type gatewayServer interface {
	usage.Snapshot
	Serve(l net.Listener) error
	ServeDev(addr string) error
	Shutdown(ctx context.Context) error
}

type gatewayOptions struct {
	LogPrompts  bool
	HostName    string
	RelayRegion func() string
	DataDir     string // usage.jsonl, for the daily counters the gateway seeds at start
}

// platform is everything this command cannot build for itself.
type platform struct {
	startTunnel  func(ctx context.Context, o tunnelOptions) (tunnelServer, error)
	savedAddr    func(dataDir string) (string, error)
	encodeInvite func(addr, secret string) string
	newGateway   func(o gatewayOptions, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) (gatewayServer, error)
	// warn is printed once at the top of `serve` when this is not a real build.
	warn string
}

// errDone means the command finished and printed everything it had to say (exit 0).
// errUsage means the command was invoked wrongly and the message is already printed (exit 2).
var (
	errDone  = errors.New("done")
	errUsage = errors.New("usage")
)

type env struct {
	out  io.Writer
	errw io.Writer
	in   io.Reader // nil means "nothing to read": a confirmation prompt then answers no
	plat platform
	// tty is whether out is a terminal. Only decoration depends on it: the QR code is drawn for a
	// person and would be noise in a pipe (ticket 009 promise 11).
	tty bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Stdin, isTerminal(os.Stdout), newPlatform()))
}

// isTerminal reports whether f is a character device, which is what "someone is watching" means
// here. A redirected or piped stdout is not.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func run(ctx context.Context, args []string, out, errw io.Writer, in io.Reader, tty bool, plat platform) int {
	e := &env{out: out, errw: errw, in: in, plat: plat, tty: tty}
	dataDir, rest, err := splitGlobal(args)
	if err != nil {
		fmt.Fprintf(errw, "%s: %v\n\n", product.CLIName, err)
		fmt.Fprint(errw, rootHelp)
		return 2
	}
	if len(rest) == 0 {
		fmt.Fprint(out, rootHelp)
		return 0
	}
	cmd, cargs := rest[0], rest[1:]
	switch cmd {
	case "serve":
		err = e.cmdServe(ctx, dataDir, cargs)
	case "keys":
		err = e.cmdKeys(ctx, dataDir, cargs)
	case "status":
		err = e.cmdStatus(ctx, dataDir, cargs)
	case "usage":
		err = e.cmdUsage(ctx, dataDir, cargs)
	case "version":
		// Both halves of a bug report start here: which build, from which commit, built when.
		fmt.Fprintf(out, "%s %s (%s, %s)\n", product.Name, product.Version, product.Commit, product.Date)
	case "help", "-h", "--help":
		fmt.Fprint(out, rootHelp)
	default:
		fmt.Fprintf(errw, "%s: unknown command %q\n\n", product.CLIName, cmd)
		fmt.Fprint(errw, rootHelp)
		return 2
	}
	switch {
	case err == nil || errors.Is(err, errDone):
		return 0
	case errors.Is(err, errUsage):
		return 2
	default:
		fmt.Fprintf(errw, "%s: %v\n", product.CLIName, err)
		return 1
	}
}

// splitGlobal pulls a leading --data-dir off the argument list so both spellings work:
//
//	bunny-network --data-dir DIR serve
//	bunny-network serve --data-dir DIR
func splitGlobal(args []string) (dataDir string, rest []string, err error) {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		a := args[0]
		switch {
		case a == "--data-dir" || a == "-data-dir":
			if len(args) < 2 {
				return "", nil, errors.New("--data-dir needs a directory")
			}
			dataDir, args = args[1], args[2:]
		case strings.HasPrefix(a, "--data-dir=") || strings.HasPrefix(a, "-data-dir="):
			dataDir, args = a[strings.Index(a, "=")+1:], args[1:]
		case a == "-h" || a == "--help" || a == "help":
			return dataDir, []string{"help"}, nil
		case a == "-v" || a == "--version" || a == "-version":
			return dataDir, []string{"version"}, nil
		default:
			return "", nil, fmt.Errorf("unknown flag %q before the command", a)
		}
	}
	return dataDir, args, nil
}

// peekDataDir finds --data-dir inside a subcommand's own arguments. `serve` needs the data dir
// before it can build its flag set, because config.json supplies the flag defaults.
func peekDataDir(args []string, fallback string) string {
	for i, a := range args {
		for _, n := range []string{"--data-dir", "-data-dir"} {
			if a == n && i+1 < len(args) {
				return args[i+1]
			}
			if strings.HasPrefix(a, n+"=") {
				return strings.TrimPrefix(a, n+"=")
			}
		}
	}
	return fallback
}

// resolveDataDir defaults to os.UserConfigDir()/bunny-network (docs/ARCHITECTURE.md).
func resolveDataDir(s string) (string, error) {
	if s != "" {
		return filepath.Abs(s)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no default data dir on this system (%w); pass --data-dir", err)
	}
	return filepath.Join(dir, product.CLIName), nil
}

// reorder moves positional arguments behind the flags. The standard flag package stops parsing
// at the first non-flag word, which would make the documented `keys add alice --rpm 60` silently
// treat --rpm as a second name. A value flag with nothing after it is an error here, because the
// "--" terminator appended below would otherwise become its value (005 fix 10h).
func reorder(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // --flag=value carries its own value
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // unknown flag: let flag.Parse produce the error
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue // --bool takes no value
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("flag needs an argument: %s", a)
		}
		i++
		flags = append(flags, args[i])
	}
	if len(positional) == 0 {
		return flags, nil
	}
	// The "--" terminator keeps a positional that looks like a flag (or one that followed an
	// explicit "--") positional; flag.Parse consumes it.
	return append(append(flags, "--"), positional...), nil
}

// parse runs fs, turning -h into printed help and a bad flag into a usage error.
func (e *env) parse(fs *flag.FlagSet, help string, args []string) error {
	fs.SetOutput(e.errw)
	fs.Usage = func() {}
	ordered, err := reorder(fs, args)
	if err != nil {
		fmt.Fprintln(e.errw, err)
	} else {
		err = fs.Parse(ordered)
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(e.out, help)
			return errDone
		}
		fmt.Fprint(e.errw, "\n", help)
		return errUsage
	}
	return nil
}

// dataDirUsage is the one description of --data-dir every subcommand shows.
const dataDirUsage = "where keys, usage, config, and the host key live"

func (e *env) logf(format string, a ...any) {
	fmt.Fprintf(e.errw, format+"\n", a...)
}

const rootHelp = `Bunny Network — share your local inference with a friend. They paste one code.

Usage:
  bunny-network <command> [flags]

Commands:
  serve      run the host: the tunnel and the gateway in front of your inference server
  keys       mint and manage per-friend keys: add, list, pause, resume, revoke, rotate, limits
  status     what the running host is doing right now
  usage      what your friends have used, from the usage log
  version    print the version

Start here:
  bunny-network serve                  # finds llama.cpp, Ollama, LM Studio, or vLLM
  bunny-network keys add alice         # prints Alice's invite, once
  bunny-network status

Global flags:
  --data-dir DIR   where keys, usage, config, and the host key live
                   (default: <user config dir>/bunny-network)

bunny-network <command> -h explains one command.
`

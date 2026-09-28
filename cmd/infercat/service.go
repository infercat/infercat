package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
	"github.com/infercat/infercat/internal/product"
)

const serviceHelp = `Usage: infercat service install|uninstall|start|stop|restart|status|login on|off [--data-dir DIR] [--json]

Keep the host running without a terminal. macOS uses a per-user LaunchAgent, Linux the packaged
systemd user unit; both run this same binary's ` + "`serve`" + `, with the settings remembered in config.json.
  install    write the LaunchAgent (macOS) / enable the user unit (Linux); does not start it
  uninstall  stop it and remove the LaunchAgent (macOS) / disable the unit (Linux)
  start      start it now; stop, restart do the obvious thing
  status     installed, loaded, running, stuck, pid, since, start at login, binary, log
  login on   start at login, login off stops that. A running host keeps running either way;
             the new value applies from the next login.

  --start-at-login=false   install without starting at login (macOS RunAtLoad; Linux: leave the unit disabled)
  --json                   print the machine envelope instead of the human form

The service is per user, never a system daemon, and never root. Two hosts must never serve one
data directory: start refuses while another host is already serving it.
`

// serviceLabel is the launchd label and serviceUnit the systemd unit, both named from the one
// product constant (CONTRIBUTING: the product name lives in one place). One per user.
func serviceLabel() string { return "ai." + product.CLIName + ".host" }
func serviceUnit() string  { return product.CLIName + ".service" }

// serviceStatus is the service.status payload of docs/design/desktop-app.md. Lingering is systemd's
// and omitted on macOS, where launchd has no such thing.
type serviceStatus struct {
	Binary       string `json:"binary"`
	Log          string `json:"log"`
	Since        string `json:"since"`
	PID          int    `json:"pid"`
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	Running      bool   `json:"running"`
	Stuck        bool   `json:"stuck"`
	StartAtLogin bool   `json:"start_at_login"`
	Lingering    *bool  `json:"lingering,omitempty"`

	// Neither of these is part of the payload: dataDir is the directory the installed service
	// serves, read back out of its own argv, and host is who answered there.
	dataDir string
	host    machine.Host
}

// serviceManager is the platform's service supervisor. Both implementations are built on every OS
// so their rendering and argv are tested everywhere, including Linux CI.
type serviceManager interface {
	install(ctx context.Context, dataDir string, atLogin bool) error
	uninstall(ctx context.Context) error
	start(ctx context.Context) error
	stop(ctx context.Context) error
	restart(ctx context.Context) error
	status(ctx context.Context) (serviceStatus, error)
	setStartAtLogin(ctx context.Context, on bool) error
}

// serviceHost is everything the verbs cannot decide for themselves, so a test can bind a fake
// launchctl or systemctl, a temporary home and a known binary path instead of this machine's.
type serviceHost struct {
	goos   string
	home   string
	uid    string
	binary string
	run    func(ctx context.Context, name string, args ...string) (string, error)
	// settle is how long start and restart wait for the supervisor to report a pid, so the
	// printed result is the state after the action rather than the state during it.
	settle time.Duration
}

// runTool runs one supervisor command, resolved through PATH, and returns its combined output.
func runTool(ctx context.Context, name string, args ...string) (string, error) {
	out, err := osexec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// tool reports a failed supervisor command with what the tool said, which is the only useful part.
func (h serviceHost) tool(ctx context.Context, name string, args ...string) (string, error) {
	out, err := h.run(ctx, name, args...)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out, nil
}

// newServiceHost describes this machine. The executable path is kept as it was invoked and not
// resolved through symlinks: a package manager's stable /usr/local/bin path is what a service
// should point at, not the versioned file behind it.
func newServiceHost() (serviceHost, error) {
	h := serviceHost{goos: runtime.GOOS, uid: strconv.Itoa(os.Getuid()), run: runTool, settle: 2 * time.Second}
	home, err := os.UserHomeDir()
	if err != nil {
		return h, err
	}
	exe, err := os.Executable()
	if err != nil {
		return h, err
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return h, err
	}
	h.home, h.binary = home, abs
	return h, nil
}

// manager picks the supervisor for an OS, or says the OS has none.
func (h serviceHost) manager() (serviceManager, error) {
	switch h.goos {
	case "darwin":
		return launchdService{h}, nil
	case "linux":
		return systemdService{h}, nil
	case "windows":
		// Nothing supervises the host on Windows yet: `serve` in a terminal, or a Task Scheduler
		// entry of the host's own making, is what there is (ticket 196 scope 4).
		return nil, &machine.Failure{Code: "unsupported_platform", Message: "managing the host as a service is not supported on Windows yet", Exit: 1}
	default:
		return nil, &machine.Failure{Code: "unsupported_platform", Message: fmt.Sprintf("managing the host as a service is not supported on %s yet", h.goos), Exit: 1}
	}
}

var errServiceNotInstalled = &machine.Failure{
	Code:    "service_not_installed",
	Message: "the service is not installed; run `" + product.CLIName + " service install` first",
	Exit:    1,
}

// serviceVerbs are the actions, and whether each one takes an on|off argument of its own.
var serviceVerbs = map[string]bool{
	"install": false, "uninstall": false, "start": false,
	"stop": false, "restart": false, "status": false, "login": true,
}

func (e *env) cmdService(ctx context.Context, pre string, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(e.out, serviceHelp)
		return nil
	}
	action := args[0]
	if _, ok := serviceVerbs[action]; !ok {
		return errUsage
	}
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	atLogin := fs.Bool("start-at-login", true, "start the host when this user logs in")
	if err := e.parse(fs, serviceHelp, args[1:]); err != nil {
		return err
	}
	on, err := serviceArgument(action, fs.Args(), *atLogin)
	if err != nil {
		return errUsage
	}
	st, err := e.serviceAction(ctx, action, *dd, on)
	if err != nil {
		return err
	}
	e.printServiceStatus(action, st)
	return nil
}

// serviceArgument reads `login`'s on|off, the only positional any of these verbs takes.
func serviceArgument(action string, rest []string, atLogin bool) (bool, error) {
	if !serviceVerbs[action] {
		if len(rest) != 0 {
			return false, fmt.Errorf("%s takes no arguments", action)
		}
		return atLogin, nil
	}
	if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off") {
		return false, fmt.Errorf("service %s takes on or off", action)
	}
	return rest[0] == "on", nil
}

// machineService answers `service … --json` with the shared envelope, before the human dispatch
// ever runs: machine mode writes exactly one JSON object to stdout and nothing to the terminal
// (docs/design/desktop-app.md §Envelope). Returns the exit code, and false when this is not it.
func (e *env) machineService(ctx context.Context, args []string) (int, bool) {
	clean, enabled, parseErr := machine.Parse(args)
	if !enabled {
		return 0, false
	}
	pre, rest, splitErr := splitGlobal(clean)
	if len(rest) == 0 || rest[0] != "service" {
		return 0, false
	}
	action := ""
	if len(rest) > 1 {
		action = rest[1]
	}
	command := "service." + action
	err := parseErr
	if err == nil {
		err = splitErr
	}
	var data json.RawMessage
	var host machine.Host
	if err == nil {
		if _, ok := serviceVerbs[action]; !ok {
			err = &machine.Failure{Code: "invalid_arguments", Message: "unknown service verb " + action, Exit: 2}
		}
	}
	var on bool
	if err == nil {
		fs := flag.NewFlagSet("service", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dd := fs.String("data-dir", pre, dataDirUsage)
		atLogin := fs.Bool("start-at-login", true, "start the host when this user logs in")
		ordered, rerr := reorder(fs, rest[2:])
		if rerr == nil {
			rerr = fs.Parse(ordered)
		}
		if rerr == nil {
			on, rerr = serviceArgument(action, fs.Args(), *atLogin)
		}
		if rerr != nil {
			err = &machine.Failure{Code: "invalid_arguments", Message: rerr.Error(), Exit: 2}
		} else {
			var st serviceStatus
			st, err = e.serviceAction(ctx, action, *dd, on)
			if err == nil {
				host = st.host
				data, err = json.Marshal(st)
			}
		}
	}
	return machine.Write(e.out, command, host, data, err), true
}

// serviceAction performs one verb and returns the state it left behind, which is what both output
// forms print: a result is shown, never inferred from an exit code (docs/PRINCIPLES.md).
func (e *env) serviceAction(ctx context.Context, action, dataDir string, atLogin bool) (serviceStatus, error) {
	host := serviceHost{}
	if e.svcHost != nil {
		host = *e.svcHost
	} else {
		h, err := newServiceHost()
		if err != nil {
			return serviceStatus{}, err
		}
		host = h
	}
	m, err := host.manager()
	if err != nil {
		return serviceStatus{}, err
	}
	switch action {
	case "install":
		dir := ""
		if dataDir != "" { // only an explicit --data-dir goes into the service's argv
			if dir, err = filepath.Abs(dataDir); err != nil {
				return serviceStatus{}, err
			}
		}
		err = m.install(ctx, dir, atLogin)
	case "uninstall":
		err = m.uninstall(ctx)
	case "start", "restart":
		if err = e.refuseSecondHost(ctx, m, dataDir); err == nil {
			if action == "start" {
				err = m.start(ctx)
			} else {
				err = m.restart(ctx)
			}
		}
	case "stop":
		err = m.stop(ctx)
	case "login":
		// Flips start-at-login alone: a host that is running keeps running, and the new value takes
		// effect at the next login (the app's Settings toggle, ticket 197).
		err = m.setStartAtLogin(ctx, atLogin)
	}
	if err != nil {
		return serviceStatus{}, err
	}
	st, err := m.status(ctx)
	if err != nil {
		return st, err
	}
	if !st.Running && (action == "start" || action == "restart" || action == "status" && host.goos == "darwin" && st.Loaded && st.PID == 0) {
		st = waitForService(ctx, m, st, host.settle, host.goos == "darwin")
	}
	st.Since, st.host = serviceRunningHost(ctx, st, dataDir)
	return st, nil
}

// refuseSecondHost keeps two hosts off one data directory: `serve` holds an exclusive lock on it,
// so a second one would fail and be restarted forever by the supervisor. A service that is already
// running is not a second host — restarting it is exactly what was asked.
func (e *env) refuseSecondHost(ctx context.Context, m serviceManager, dataDir string) error {
	st, err := m.status(ctx)
	if err != nil {
		return err
	}
	if st.Running {
		return nil
	}
	dir := st.dataDir
	if dir == "" {
		if dir, err = resolveDataDir(dataDir); err != nil {
			return err
		}
	}
	if _, err := admin.Fetch(ctx, dir); err == nil {
		return &machine.Failure{Code: "data_dir_busy", Message: "another host is already serving " + dir + "; stop it first", Exit: 1}
	}
	return nil
}

// waitForService gives the supervisor a moment to report a pid after start or restart.
func waitForService(ctx context.Context, m serviceManager, st serviceStatus, settle time.Duration, detectStuck bool) serviceStatus {
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return st
		case <-time.After(100 * time.Millisecond):
		}
		next, err := m.status(ctx)
		if err != nil {
			return st
		}
		st = next
		if st.Running {
			return st
		}
	}
	st.Stuck = detectStuck && settle > 0 && ctx.Err() == nil && st.Loaded && st.PID == 0 && !st.Running
	return st
}

// serviceRunningHost asks the host the service started how long it has been up, and who it is. Its
// own uptime is the only truthful start time available on both platforms — launchd keeps none — and
// its version and name are the envelope's `host`. A host that does not answer contributes neither,
// beyond this build's own version.
func serviceRunningHost(ctx context.Context, st serviceStatus, dataDir string) (string, machine.Host) {
	host := machine.Host{Version: product.Version}
	if !st.Running {
		return "", host
	}
	dir := st.dataDir
	if dir == "" {
		var err error
		if dir, err = resolveDataDir(dataDir); err != nil {
			return "", host
		}
	}
	running, err := admin.Fetch(ctx, dir)
	if err != nil {
		return "", host
	}
	if running.Version != "" {
		host = machine.Host{Version: running.Version, Name: running.Name}
	}
	if running.UptimeS <= 0 {
		return "", host
	}
	return time.Now().Add(-time.Duration(running.UptimeS) * time.Second).UTC().Format(time.RFC3339), host
}

// printServiceStatus is the human form: one state line, then the facts a person needs to go
// looking — where the binary and the log are, and whether logging out stops the host.
func (e *env) printServiceStatus(action string, st serviceStatus) {
	switch action {
	case "status":
	case "login":
		state := "off"
		if st.StartAtLogin {
			state = "on"
		}
		fmt.Fprintf(e.out, "Start at login is %s. A running host was left alone; the change applies at the next login.\n", state)
	default:
		fmt.Fprintln(e.out, serviceDone[action])
	}
	fmt.Fprintln(e.out, "service   "+serviceStateLine(st))
	if !st.Installed {
		fmt.Fprintf(e.out, "Install it with: %s service install\n", product.CLIName)
		return
	}
	login := "no"
	if st.StartAtLogin {
		login = "yes"
	}
	fmt.Fprintf(e.out, "login     starts at login: %s\n", login)
	fmt.Fprintln(e.out, "binary    "+st.Binary)
	fmt.Fprintln(e.out, "log       "+st.Log)
	if st.Lingering != nil && !*st.Lingering {
		fmt.Fprintf(e.out, "Logging out stops the host. An administrator can keep it running with: sudo loginctl enable-linger %s\n", serviceUser())
	}
}

var serviceDone = map[string]string{
	"install":   "Installed.",
	"uninstall": "Uninstalled.",
	"start":     "Started.",
	"stop":      "Stopped.",
	"restart":   "Restarted.",
}

// serviceStateLine says exactly what is true, in the order a person reads it.
func serviceStateLine(st serviceStatus) string {
	if !st.Installed {
		return "not installed"
	}
	parts := []string{"installed"}
	if !st.Loaded {
		return strings.Join(append(parts, "not loaded"), " · ")
	}
	parts = append(parts, "loaded")
	if st.Stuck {
		return "loaded · stuck (no pid after 2s)"
	}
	if !st.Running {
		return strings.Join(append(parts, "not running"), " · ")
	}
	parts = append(parts, "running", "pid "+strconv.Itoa(st.PID))
	if st.Since != "" {
		parts = append(parts, "since "+st.Since)
	}
	return strings.Join(parts, " · ")
}

func serviceUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "USERNAME"
}

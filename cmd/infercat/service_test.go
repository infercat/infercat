package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
)

// fakeTool stands in for launchctl, systemctl and loginctl. Unit tests never run the real ones:
// bootstrapping a LaunchAgent or touching `systemctl --user` on a developer's machine is a real
// side effect, and the argv is exactly what these tests are for.
type fakeTool struct {
	mu    sync.Mutex
	calls []string
	reply func(name string, args []string) (string, error)
}

func (f *fakeTool) run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	f.mu.Unlock()
	if f.reply == nil {
		return "", nil
	}
	return f.reply(name, args)
}

func (f *fakeTool) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

// cliEnvelope is the shared envelope (internal/machine) as the app reads it off stdout.
type cliEnvelope struct {
	Schema  int              `json:"schema"`
	Command string           `json:"command"`
	Host    machine.Host     `json:"host"`
	Data    *serviceStatus   `json:"data"`
	Error   *machine.Failure `json:"error"`
}

// callMachine runs one `service … --json` through the machine-mode entry point run() uses, and
// returns the one object it wrote plus the process exit code.
func callMachine(t *testing.T, e *env, out *bytes.Buffer, args ...string) (cliEnvelope, int) {
	t.Helper()
	out.Reset()
	code, handled := e.machineService(context.Background(), append(append([]string{"service"}, args...), "--json"))
	if !handled {
		t.Fatal("machine mode did not claim", args)
	}
	var msg cliEnvelope
	if err := json.Unmarshal(out.Bytes(), &msg); err != nil {
		t.Fatal(err, out.String())
	}
	if msg.Schema != machine.Schema {
		t.Fatal(out.String())
	}
	return msg, code
}

// launchctlPrintRunning is the shape of a real `launchctl print gui/501/<label>` for a running
// agent, captured on macOS 27 from the plist this command writes (ticket 196 proof). The pid line
// is the only thing read out of it, and there is exactly one.
const launchctlPrintRunning = `gui/501/ai.infercat.host = {
	active count = 1
	path = /Users/max/Library/LaunchAgents/ai.infercat.host.plist
	type = LaunchAgent
	state = running

	program = /usr/local/bin/infercat
	arguments = {
		/usr/local/bin/infercat
		serve
	}

	domain = gui/501 [100017]
	minimum runtime = 10
	runs = 1
	pid = 44375
	immediate reason = speculative
	last exit code = (never exited)

	semaphores = {
		successful exit => 0
	}
}
`

const launchctlPrintWaiting = `gui/501/ai.infercat.host = {
	active count = 0
	state = waiting
	runs = 0
	last exit code = 0
}
`

// launchctlPrintMissing is what the real tool says, on exit 113, for a label launchd never loaded.
const launchctlPrintMissing = "Could not find service \"ai.infercat.host\" in domain for user gui: 501\n"

func TestServicePlistRenderingIsPinned(t *testing.T) {
	t.Parallel()
	log := "/Users/max/Library/Logs/Infercat/host.log"
	for _, c := range []struct {
		golden  string
		dataDir string
		atLogin bool
	}{
		{"agent.plist", "/private/tmp/ic-data", true},
		{"agent-no-login.plist", "", false},
	} {
		got := renderPlist("/usr/local/bin/infercat", c.dataDir, log, c.atLogin)
		want, err := os.ReadFile(filepath.Join("testdata", "service", c.golden))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s:\n--- got ---\n%s\n--- want ---\n%s", c.golden, got, want)
		}
		p, err := readPlist(got)
		if err != nil {
			t.Fatal(c.golden, err)
		}
		if p.runAtLoad != c.atLogin || p.args[0] != "/usr/local/bin/infercat" || p.args[1] != "serve" || p.log != log {
			t.Fatal(c.golden, p)
		}
		if peekDataDir(p.args, "") != c.dataDir {
			t.Fatal("data dir round trip", c.golden, p.args)
		}
	}
}

// A data directory may hold any character a filesystem allows; the plist must stay well formed.
func TestServicePlistEscapesPaths(t *testing.T) {
	t.Parallel()
	dir := `/tmp/a&b/<c>/"d"`
	raw := renderPlist("/usr/local/bin/infercat", dir, "/tmp/log", true)
	if bytes.Contains(raw, []byte("<c>/")) {
		t.Fatal("unescaped path in plist", string(raw))
	}
	p, err := readPlist(raw)
	if err != nil {
		t.Fatal(err)
	}
	if peekDataDir(p.args, "") != dir {
		t.Fatal(p.args)
	}
}

// plutil is macOS's own parser: the strongest available statement that launchd will accept what
// this command writes. Skipped where it does not exist (Linux CI), where the golden still applies.
func TestServicePlistIsValidToMacOS(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is macOS only")
	}
	path := filepath.Join(t.TempDir(), "agent.plist")
	if err := os.WriteFile(path, renderPlist("/usr/local/bin/infercat", "/tmp/d", "/tmp/l", true), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runTool(context.Background(), "plutil", "-lint", path); err != nil {
		t.Fatal(err, out)
	}
}

func TestServiceLaunchctlPIDReadsRealOutput(t *testing.T) {
	t.Parallel()
	if got := launchctlPID(launchctlPrintRunning); got != 44375 {
		t.Fatal(got)
	}
	if got := launchctlPID(launchctlPrintWaiting); got != 0 {
		t.Fatal(got)
	}
}

// launchdFake is a launchctl that keeps the state the real one would: whether the label is loaded
// in gui/<uid> and whether the job has a process.
type launchdFake struct {
	tool            *fakeTool
	loaded, running bool
}

func newLaunchdFake(t *testing.T) *launchdFake {
	t.Helper()
	f := &launchdFake{tool: &fakeTool{}}
	f.tool.reply = func(name string, args []string) (string, error) {
		if name != "launchctl" {
			t.Fatalf("unit tests must not run %q", name)
		}
		switch args[0] {
		case "print":
			if !f.loaded {
				return launchctlPrintMissing, errors.New("exit status 113")
			}
			if f.running {
				return launchctlPrintRunning, nil
			}
			return launchctlPrintWaiting, nil
		case "bootstrap":
			f.loaded, f.running = true, true
		case "bootout":
			f.loaded, f.running = false, false
		case "kickstart":
			f.running = true
		}
		return "", nil
	}
	return f
}

func (f *launchdFake) host(home string) serviceHost {
	return serviceHost{goos: "darwin", home: home, uid: "501", binary: "/usr/local/bin/infercat", run: f.tool.run}
}

func TestServiceLaunchdVerbsDriveLaunchctl(t *testing.T) {
	t.Parallel()
	home, data := t.TempDir(), t.TempDir()
	fake := newLaunchdFake(t)
	host := fake.host(home)
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	call := func(action string, flags ...string) string {
		t.Helper()
		out.Reset()
		if err := e.cmdService(context.Background(), "", append([]string{action, "--data-dir", data}, flags...)); err != nil {
			t.Fatal(action, err)
		}
		return out.String()
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")

	if text := call("status"); !strings.Contains(text, "service   not installed") {
		t.Fatal(text)
	}
	if text := call("install"); !strings.Contains(text, "installed · not loaded") || !strings.Contains(text, "starts at login: yes") {
		t.Fatal(text)
	}
	raw, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	p, err := readPlist(raw)
	if err != nil || !p.runAtLoad || peekDataDir(p.args, "") != data {
		t.Fatal(p, err)
	}
	if info, err := os.Stat(plist); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatal("launchd refuses a plist only its owner may read", info, err)
	}
	if text := call("start"); !strings.Contains(text, "Started.") || !strings.Contains(text, "running · pid 44375") {
		t.Fatal(text)
	}
	if text := call("restart"); !strings.Contains(text, "running · pid 44375") {
		t.Fatal(text)
	}
	if text := call("stop"); !strings.Contains(text, "Stopped.") || !strings.Contains(text, "installed · not loaded") {
		t.Fatal(text)
	}
	if text := call("uninstall"); !strings.Contains(text, "not installed") {
		t.Fatal(text)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatal("uninstall left the plist", err)
	}
	want := strings.Join([]string{
		"launchctl print gui/501/" + serviceLabel(), // status
		"launchctl print gui/501/" + serviceLabel(), // install's own status read
		"launchctl print gui/501/" + serviceLabel(), // start: the second-host check
		"launchctl print gui/501/" + serviceLabel(), // start: not loaded yet
		"launchctl bootstrap gui/501 " + plist,      //
		"launchctl print gui/501/" + serviceLabel(), // the printed result
		"launchctl print gui/501/" + serviceLabel(), // restart: the second-host check
		"launchctl print gui/501/" + serviceLabel(), // restart: loaded and running
		"launchctl kickstart -k gui/501/" + serviceLabel(),
		"launchctl print gui/501/" + serviceLabel(),
		"launchctl print gui/501/" + serviceLabel(), // stop
		"launchctl bootout gui/501/" + serviceLabel(),
		"launchctl print gui/501/" + serviceLabel(),
		"launchctl print gui/501/" + serviceLabel(), // uninstall
		"launchctl print gui/501/" + serviceLabel(),
	}, "\n")
	if got := fake.tool.log(); got != want {
		t.Fatalf("launchctl argv:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// --start-at-login=false installs a plist launchd will not run by itself; start still runs it.
func TestServiceInstallWithoutStartAtLogin(t *testing.T) {
	t.Parallel()
	home, data := t.TempDir(), t.TempDir()
	fake := newLaunchdFake(t)
	host := fake.host(home)
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	if err := e.cmdService(context.Background(), "", []string{"install", "--data-dir", data, "--start-at-login=false"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "starts at login: no") {
		t.Fatal(out.String())
	}
	raw, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := readPlist(raw); err != nil || p.runAtLoad {
		t.Fatal("RunAtLoad survived --start-at-login=false", err)
	}
	// Loaded but idle is kickstart's case, not bootstrap's.
	fake.loaded = true
	out.Reset()
	if err := e.cmdService(context.Background(), "", []string{"start", "--data-dir", data}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.tool.log(), "launchctl kickstart gui/501/"+serviceLabel()) {
		t.Fatal(fake.tool.log())
	}
}

// `service login on|off` flips start-at-login alone. The plist is rewritten in place, byte for byte
// the installed one with RunAtLoad changed, and the loaded job is never booted out or kickstarted:
// a host that is serving keeps serving (ticket 197's Settings toggle).
func TestServiceLoginTogglesStartAtLoginWithoutTouchingTheHost(t *testing.T) {
	t.Parallel()
	home, data := t.TempDir(), t.TempDir()
	golden := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", "service", name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, golden("agent.plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := newLaunchdFake(t)
	fake.loaded, fake.running = true, true
	host := fake.host(home)
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	call := func(state string) string {
		t.Helper()
		out.Reset()
		if err := e.cmdService(context.Background(), "", []string{"login", state, "--data-dir", data}); err != nil {
			t.Fatal(state, err)
		}
		return out.String()
	}
	text := call("off")
	if !strings.Contains(text, "Start at login is off.") || !strings.Contains(text, "starts at login: no") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "running · pid 44375") {
		t.Fatal("the running host was disturbed", text)
	}
	raw, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, golden("agent-login-off.plist")) {
		t.Fatalf("login off:\n--- got ---\n%s", raw)
	}
	// Idempotent, and back to the installed bytes exactly: nothing else in the plist moves.
	call("off")
	if text := call("on"); !strings.Contains(text, "Start at login is on.") {
		t.Fatal(text)
	}
	raw, _ = os.ReadFile(plist)
	if !bytes.Equal(raw, golden("agent.plist")) {
		t.Fatalf("login on:\n--- got ---\n%s", raw)
	}
	for _, forbidden := range []string{"bootout", "bootstrap", "kickstart"} {
		if strings.Contains(fake.tool.log(), forbidden) {
			t.Fatalf("login ran %s: %s", forbidden, fake.tool.log())
		}
	}
	msg, code := callMachine(t, e, &out, "login", "on", "--data-dir", data)
	if code != 0 || msg.Command != "service.login" || msg.Data == nil || !msg.Data.StartAtLogin {
		t.Fatal(code, out.String())
	}
	// A verb that needs an installed service says so instead of writing a file nothing reads.
	if err := os.Remove(plist); err != nil {
		t.Fatal(err)
	}
	if err := e.cmdService(context.Background(), "", []string{"login", "off", "--data-dir", data}); !errors.Is(err, errServiceNotInstalled) {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"login"}, {"login", "yes"}, {"login", "on", "off"}} {
		if err := e.cmdService(context.Background(), "", args); !errors.Is(err, errUsage) {
			t.Fatal(args, err)
		}
	}
}

// Two hosts must never serve one data directory: `serve` holds an exclusive lock on it, so the
// second would fail and be restarted for ever. A host that is already answering there is a refusal.
func TestServiceStartRefusesAnotherHostOnTheDataDir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	data, err := os.MkdirTemp("", "ic196-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	server, err := admin.Serve(data, func() admin.Status { return admin.Status{Mode: "host", UptimeS: 90} }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	fake := newLaunchdFake(t)
	host := fake.host(home)
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	if err := e.cmdService(context.Background(), "", []string{"install", "--data-dir", data}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"start", "restart"} {
		out.Reset()
		err := e.cmdService(context.Background(), "", []string{action, "--data-dir", data})
		if err == nil || !strings.Contains(err.Error(), "another host is already serving") {
			t.Fatal(action, err)
		}
		if strings.Contains(fake.tool.log(), "bootstrap") || strings.Contains(fake.tool.log(), "kickstart") {
			t.Fatal("refusal started something", fake.tool.log())
		}
	}
	// The service's own host is not a second host: restarting it is exactly what was asked, and
	// its uptime is where `since` comes from.
	fake.loaded, fake.running = true, true
	out.Reset()
	if err := e.cmdService(context.Background(), "", []string{"restart", "--data-dir", data}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.tool.log(), "kickstart -k") {
		t.Fatal(fake.tool.log())
	}
	since, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.SplitN(strings.SplitN(out.String(), "since ", 2)[1], "\n", 2)[0]))
	if err != nil {
		t.Fatal(err, out.String())
	}
	if d := time.Since(since); d < 80*time.Second || d > 120*time.Second {
		t.Fatal("since does not come from the host's uptime", d, out.String())
	}
}

func TestServiceJSONEnvelope(t *testing.T) {
	t.Parallel()
	home, data := t.TempDir(), t.TempDir()
	fake := newLaunchdFake(t)
	host := fake.host(home)
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}

	msg, code := callMachine(t, e, &out, "status", "--data-dir", data)
	if code != 0 || msg.Command != "service.status" || msg.Data == nil || msg.Error != nil {
		t.Fatal(code, out.String())
	}
	if msg.Data.Installed || msg.Data.Running || msg.Data.Log == "" || msg.Data.Binary != "/usr/local/bin/infercat" {
		t.Fatal(out.String())
	}
	// Zero values are present in machine mode, and lingering is systemd's alone.
	for _, field := range []string{`"installed":false`, `"running":false`, `"loaded":false`, `"pid":0`, `"since":""`, `"start_at_login":false`} {
		if !strings.Contains(out.String(), field) {
			t.Fatal(field, out.String())
		}
	}
	if strings.Contains(out.String(), "lingering") {
		t.Fatal("launchd has no lingering", out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatal("machine mode writes exactly one object", out.String())
	}
	// A failure is still one JSON object on stdout, with a code, and a non-zero exit.
	msg, code = callMachine(t, e, &out, "start", "--data-dir", data)
	if code != 1 || msg.Error == nil || msg.Error.Code != "service_not_installed" || msg.Data != nil || msg.Command != "service.start" {
		t.Fatal(code, out.String())
	}
	if _, code := callMachine(t, e, &out, "install", "--data-dir", data); code != 0 {
		t.Fatal(code, out.String())
	}
	msg, code = callMachine(t, e, &out, "start", "--data-dir", data)
	if code != 0 || msg.Data == nil || !msg.Data.Running || msg.Data.PID != 44375 {
		t.Fatal(code, out.String())
	}
	// Bad arguments are the envelope's business too, and they exit 2.
	for _, args := range [][]string{{"reload"}, {"status", "extra"}, {"login"}, {"login", "maybe"}} {
		msg, code := callMachine(t, e, &out, append(args, "--data-dir", data)...)
		if code != 2 || msg.Error == nil || msg.Data != nil {
			t.Fatal(args, code, out.String())
		}
	}
}

func TestServiceSystemdVerbsDriveSystemctl(t *testing.T) {
	t.Parallel()
	unit := serviceUnit()
	state := map[string]string{"UnitFileState": "enabled", "ActiveState": "active"}
	fake := &fakeTool{}
	fake.reply = func(name string, args []string) (string, error) {
		switch name {
		case "loginctl":
			return "Linger=no\n", nil
		case "systemctl":
			if args[1] != "show" {
				switch args[1] {
				case "enable":
					state["UnitFileState"] = "enabled"
				case "disable":
					state["UnitFileState"] = "disabled"
				case "stop":
					state["ActiveState"] = "inactive"
				case "start", "restart":
					state["ActiveState"] = "active"
				}
				return "", nil
			}
			return "LoadState=loaded\nActiveState=" + state["ActiveState"] + "\nSubState=running\nMainPID=4242\n" +
				"UnitFileState=" + state["UnitFileState"] + "\nFragmentPath=/usr/lib/systemd/user/" + unit + "\n" +
				"ExecStart={ path=/usr/bin/infercat ; argv[]=/usr/bin/infercat serve ; ignore_errors=no }\n", nil
		}
		t.Fatalf("unit tests must not run %q", name)
		return "", nil
	}
	host := serviceHost{goos: "linux", home: t.TempDir(), uid: "1000", binary: "/tmp/build/infercat", run: fake.run}
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	call := func(action string, flags ...string) string {
		t.Helper()
		out.Reset()
		if err := e.cmdService(context.Background(), "", append([]string{action}, flags...)); err != nil {
			t.Fatal(action, err)
		}
		return out.String()
	}
	text := call("status")
	// The unit's own ExecStart is the truth about which binary runs, not the one that was typed.
	if !strings.Contains(text, "running · pid 4242") || !strings.Contains(text, "binary    /usr/bin/infercat") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "journalctl --user -u infercat") || !strings.Contains(text, "enable-linger") {
		t.Fatal("systemd log and lingering", text)
	}
	if text := call("stop"); !strings.Contains(text, "installed · loaded · not running") {
		t.Fatal(text)
	}
	call("start")
	call("restart")
	call("uninstall")
	if text := call("install"); !strings.Contains(text, "starts at login: yes") {
		t.Fatal(text)
	}
	// login is enable/disable without --now: the unit's running state is not part of the change.
	state["ActiveState"] = "active" // the host is serving while start-at-login is toggled
	if text := call("login", "off"); !strings.Contains(text, "starts at login: no") || !strings.Contains(text, "running · pid 4242") {
		t.Fatal(text)
	}
	if text := call("login", "on"); !strings.Contains(text, "starts at login: yes") || !strings.Contains(text, "running · pid 4242") {
		t.Fatal(text)
	}
	if strings.Contains(fake.log(), "--now") {
		t.Fatal("a lifecycle flag leaked into a start-at-login change", fake.log())
	}
	for _, want := range []string{
		"systemctl --user stop " + unit,
		"systemctl --user start " + unit,
		"systemctl --user restart " + unit,
		"systemctl --user disable " + unit,
		"systemctl --user enable " + unit,
		"loginctl show-user 1000 --property=Linger",
		"systemctl --user show " + unit + " --property=LoadState --property=ActiveState",
	} {
		if !strings.Contains(fake.log(), want) {
			t.Fatalf("missing %q in\n%s", want, fake.log())
		}
	}
	// This command never writes the packaged unit, so it cannot honour a --data-dir; it says so.
	err := e.cmdService(context.Background(), "", []string{"install", "--data-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--data-dir cannot be applied") {
		t.Fatal(err)
	}
}

func TestServiceSystemdWithoutThePackagedUnit(t *testing.T) {
	t.Parallel()
	fake := &fakeTool{reply: func(name string, args []string) (string, error) {
		if name == "loginctl" {
			return "", errors.New("exit status 1")
		}
		return "LoadState=not-found\nActiveState=inactive\nMainPID=0\nUnitFileState=\nFragmentPath=\nExecStart=\n", nil
	}}
	host := serviceHost{goos: "linux", home: t.TempDir(), uid: "1000", binary: "/usr/bin/infercat", run: fake.run}
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	err := e.cmdService(context.Background(), "", []string{"install"})
	if err == nil || !strings.Contains(err.Error(), "docs/LINUX.md") {
		t.Fatal(err)
	}
	if _, code := callMachine(t, e, &out, "status"); code != 0 {
		t.Fatal(code, out.String())
	}
	// loginctl could not answer, so the payload says nothing about lingering rather than guessing.
	if strings.Contains(out.String(), "lingering") || !strings.Contains(out.String(), `"installed":false`) {
		t.Fatal(out.String())
	}
	if err := e.cmdService(context.Background(), "", []string{"start"}); !errors.Is(err, errServiceNotInstalled) {
		t.Fatal(err)
	}
}

func TestServiceWindowsIsNotSupportedYet(t *testing.T) {
	t.Parallel()
	host := serviceHost{goos: "windows", home: t.TempDir(), uid: "0", binary: `C:\infercat.exe`, run: func(context.Context, string, ...string) (string, error) {
		t.Fatal("no supervisor on Windows")
		return "", nil
	}}
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard, svcHost: &host}
	for _, action := range []string{"install", "uninstall", "start", "stop", "restart", "status"} {
		out.Reset()
		err := e.cmdService(context.Background(), "", []string{action})
		if err == nil || !strings.Contains(err.Error(), "not supported on Windows yet") {
			t.Fatal(action, err)
		}
		msg, code := callMachine(t, e, &out, action)
		if code != 1 || msg.Error == nil || msg.Error.Code != "unsupported_platform" || msg.Data != nil {
			t.Fatal(action, code, out.String())
		}
	}
}

// The supervisor is resolved through PATH, like any other tool this project shells out to. This is
// the one test that runs the real runner — against a script called launchctl in a temp directory.
func TestServiceResolvesTheSupervisorThroughPATH(t *testing.T) {
	bin, home, data := t.TempDir(), t.TempDir(), t.TempDir()
	record := filepath.Join(bin, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + record + "\ncase \"$1\" in print) exit 113;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	host := serviceHost{goos: "darwin", home: home, uid: "501", binary: "/usr/local/bin/infercat", run: runTool}
	e := &env{out: io.Discard, errw: io.Discard, svcHost: &host}
	for _, action := range []string{"install", "start"} {
		if err := e.cmdService(context.Background(), "", []string{action, "--data-dir", data}); err != nil {
			t.Fatal(action, err)
		}
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
	if !strings.Contains(string(got), "bootstrap gui/501 "+plist) {
		t.Fatal(string(got))
	}
	if strings.Count(string(got), "print gui/501/"+serviceLabel()) == 0 {
		t.Fatal(string(got))
	}
}

func TestServiceUsageErrors(t *testing.T) {
	t.Parallel()
	var out, errw bytes.Buffer
	host := serviceHost{goos: "darwin", home: t.TempDir(), uid: "501", binary: "/usr/local/bin/infercat", run: func(context.Context, string, ...string) (string, error) {
		return launchctlPrintMissing, errors.New("exit status 113")
	}}
	e := &env{out: &out, errw: &errw, svcHost: &host}
	for _, args := range [][]string{{"reload"}, {"status", "extra"}, {"status", "--nope"}} {
		if err := e.cmdService(context.Background(), "", args); !errors.Is(err, errUsage) {
			t.Fatal(args, err)
		}
	}
	out.Reset()
	if err := e.cmdService(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "install|uninstall|start|stop|restart|status") {
		t.Fatal(out.String())
	}
}

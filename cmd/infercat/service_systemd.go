package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/infercat/infercat/internal/product"
)

// systemdService wraps `systemctl --user` on the unit the Linux packages install
// (/usr/lib/systemd/user/infercat.service, docs/LINUX.md). It never writes a unit file: the package
// owns it, so "install" here means enable, and the verbs are the ones a person would type by hand.
type systemdService struct{ h serviceHost }

// unitProps are the only properties the status payload needs. `systemctl show` answers for an
// unknown unit too (LoadState=not-found), so one call covers installed, loaded and running.
var unitProps = []string{"LoadState", "ActiveState", "SubState", "MainPID", "UnitFileState", "FragmentPath", "ExecStart"}

func (s systemdService) unitctl(ctx context.Context, args ...string) error {
	_, err := s.h.tool(ctx, "systemctl", append([]string{"--user"}, args...)...)
	return err
}

func (s systemdService) install(ctx context.Context, dataDir string, atLogin bool) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if !st.Installed {
		return fmt.Errorf("no %s unit on this system; install the .deb or .rpm package (docs/LINUX.md)", serviceUnit())
	}
	if dataDir != "" && dataDir != st.dataDir {
		// The unit is the package's and this command does not rewrite it, so say so rather than
		// installing something that quietly serves a different directory.
		return fmt.Errorf("the packaged unit runs `%s serve` with the default data directory; --data-dir cannot be applied to it", product.CLIName)
	}
	return s.setStartAtLogin(ctx, atLogin)
}

func (s systemdService) uninstall(ctx context.Context) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if !st.Installed {
		return nil
	}
	if st.Running {
		if err := s.unitctl(ctx, "stop", serviceUnit()); err != nil {
			return err
		}
	}
	if !st.StartAtLogin {
		return nil
	}
	return s.unitctl(ctx, "disable", serviceUnit())
}

// setStartAtLogin is enable/disable without --now, so the unit's current state is untouched: a
// running host keeps running and the change takes effect at the next login.
func (s systemdService) setStartAtLogin(ctx context.Context, on bool) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if !st.Installed {
		return errServiceNotInstalled
	}
	if st.StartAtLogin == on {
		return nil
	}
	if on {
		return s.unitctl(ctx, "enable", serviceUnit())
	}
	return s.unitctl(ctx, "disable", serviceUnit())
}

func (s systemdService) start(ctx context.Context) error {
	return s.act(ctx, "start")
}

func (s systemdService) stop(ctx context.Context) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if !st.Installed {
		return nil
	}
	return s.unitctl(ctx, "stop", serviceUnit())
}

func (s systemdService) restart(ctx context.Context) error {
	return s.act(ctx, "restart")
}

func (s systemdService) act(ctx context.Context, verb string) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if !st.Installed {
		return errServiceNotInstalled
	}
	return s.unitctl(ctx, verb, serviceUnit())
}

func (s systemdService) status(ctx context.Context) (serviceStatus, error) {
	args := []string{"--user", "show", serviceUnit()}
	for _, p := range unitProps {
		args = append(args, "--property="+p)
	}
	out, err := s.h.tool(ctx, "systemctl", args...)
	if err != nil {
		return serviceStatus{}, err
	}
	props := unitProperties(out)
	st := serviceStatus{
		Binary:       s.h.binary,
		Log:          "journalctl --user -u " + product.CLIName,
		Installed:    props["FragmentPath"] != "",
		Loaded:       props["LoadState"] == "loaded",
		Running:      props["ActiveState"] == "active",
		StartAtLogin: props["UnitFileState"] == "enabled",
	}
	if path, argv := execStartCommand(props["ExecStart"]); path != "" {
		st.Binary, st.dataDir = path, peekDataDir(argv, "")
	}
	if pid, err := strconv.Atoi(props["MainPID"]); err == nil && pid > 0 && st.Running {
		st.PID = pid
	}
	st.Running = st.Running && st.PID > 0
	st.Lingering = s.lingering(ctx)
	return st, nil
}

// lingering reports whether this user's services survive logout, and never changes it: enabling
// lingering is an administrator's decision (docs/LINUX.md). nil means loginctl could not say.
func (s systemdService) lingering(ctx context.Context) *bool {
	out, err := s.h.run(ctx, "loginctl", "show-user", s.h.uid, "--property=Linger")
	if err != nil {
		return nil
	}
	value, ok := unitProperties(out)["Linger"]
	if !ok {
		return nil
	}
	on := value == "yes"
	return &on
}

// unitProperties parses the Key=Value lines of `systemctl show` and `loginctl show-user`.
func unitProperties(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			props[key] = value
		}
	}
	return props
}

// execStartCommand pulls the executable and its argv out of systemd's structured ExecStart value,
// `{ path=/usr/bin/infercat ; argv[]=/usr/bin/infercat serve ; … }`. The argv says whether the
// packaged unit was customized with a --data-dir, which status needs to find the right host.
func execStartCommand(value string) (path string, argv []string) {
	_, rest, ok := strings.Cut(value, "path=")
	if !ok {
		return "", nil
	}
	path = strings.TrimSpace(strings.SplitN(rest, ";", 2)[0])
	if _, tail, ok := strings.Cut(value, "argv[]="); ok {
		argv = strings.Fields(strings.SplitN(tail, ";", 2)[0])
	}
	return path, argv
}

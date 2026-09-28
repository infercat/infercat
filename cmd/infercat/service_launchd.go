package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/infercat/infercat/internal/fsx"
	"github.com/infercat/infercat/internal/machine"
	"github.com/infercat/infercat/internal/product"
)

// launchdService manages the per-user LaunchAgent. Everything it needs is in the plist and in
// `launchctl print`, so the same code answers "installed" and "running" without a state file of
// its own. The domain is always gui/<uid>: a per-user agent, never a system daemon, never root.
type launchdService struct{ h serviceHost }

func (s launchdService) plistPath() string {
	return filepath.Join(s.h.home, "Library", "LaunchAgents", serviceLabel()+".plist")
}

// logPath is where launchd sends the host's stdout and stderr. ~/Library/Logs is the directory
// Console.app shows, so a host can find it without being told a path.
func (s launchdService) logPath() string {
	return filepath.Join(s.h.home, "Library", "Logs", product.Name, "host.log")
}

func (s launchdService) domain() string { return "gui/" + s.h.uid }
func (s launchdService) target() string { return s.domain() + "/" + serviceLabel() }

// macOS privacy protection gates these locations for background processes. Relative roots
// are under the user's home; /Volumes covers removable and network volumes.
const macOSBackgroundProtectedRoots = "Desktop\nDocuments\nDownloads\nLibrary/Mobile Documents\n/Volumes"

func refuseProtectedServiceBinary(home, binary string) error {
	homes := []string{filepath.Clean(home)}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		homes = append(homes, resolved)
	}
	protected := func(path string) bool {
		for _, root := range strings.Split(macOSBackgroundProtectedRoots, "\n") {
			for _, home := range homes {
				base := root
				if !filepath.IsAbs(base) {
					base = filepath.Join(home, base)
				}
				if path == base || strings.HasPrefix(path, base+string(filepath.Separator)) {
					return true
				}
			}
		}
		return false
	}
	refusal := &machine.Failure{Code: "binary_in_protected_directory", Message: "the service binary is in a macOS privacy-protected directory; move the binary, for example to /usr/local/bin or /Applications", Exit: 1}
	if protected(filepath.Clean(binary)) {
		return refusal
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err == nil && protected(resolved) {
		return refusal
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s launchdService) install(ctx context.Context, dataDir string, atLogin bool) error {
	if err := refuseProtectedServiceBinary(s.h.home, s.h.binary); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.logPath()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.plistPath()), 0o755); err != nil {
		return err
	}
	// 0644: launchd refuses a plist that is writable by anyone but its owner.
	return fsx.WriteFile(s.plistPath(), renderPlist(s.h.binary, dataDir, s.logPath(), atLogin), 0o644)
}

// installed reads the LaunchAgent on disk. os.ErrNotExist means there is none.
func (s launchdService) installed() (installedPlist, error) {
	raw, err := os.ReadFile(s.plistPath())
	if err != nil {
		return installedPlist{}, err
	}
	return readPlist(raw)
}

// setStartAtLogin rewrites RunAtLoad in place and touches nothing else: no bootout, no kickstart, so
// a host that is serving keeps serving. launchd reads the file when the agent is next bootstrapped,
// which is what "at the next login" means. The write is atomic, so a crash cannot leave half a plist.
func (s launchdService) setStartAtLogin(ctx context.Context, on bool) error {
	p, err := s.installed()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errServiceNotInstalled
		}
		return err
	}
	if p.runAtLoad == on {
		return nil
	}
	binary, log := s.h.binary, s.logPath()
	if len(p.args) > 0 {
		binary = p.args[0]
	}
	if p.log != "" {
		log = p.log
	}
	return fsx.WriteFile(s.plistPath(), renderPlist(binary, peekDataDir(p.args, ""), log, on), 0o644)
}

func (s launchdService) uninstall(ctx context.Context) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if st.Loaded {
		if _, err := s.h.tool(ctx, "launchctl", "bootout", s.target()); err != nil {
			return err
		}
	}
	if err := os.Remove(s.plistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s launchdService) start(ctx context.Context) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	switch {
	case !st.Installed:
		return errServiceNotInstalled
	case !st.Loaded:
		_, err = s.h.tool(ctx, "launchctl", "bootstrap", s.domain(), s.plistPath())
	case !st.Running:
		// Loaded but idle: an agent installed with --start-at-login=false, or one that exited 0.
		_, err = s.h.tool(ctx, "launchctl", "kickstart", s.target())
	}
	return err
}

func (s launchdService) stop(ctx context.Context) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	if !st.Loaded {
		return nil
	}
	_, err = s.h.tool(ctx, "launchctl", "bootout", s.target())
	return err
}

func (s launchdService) restart(ctx context.Context) error {
	st, err := s.status(ctx)
	if err != nil {
		return err
	}
	switch {
	case !st.Installed:
		return errServiceNotInstalled
	case !st.Loaded:
		_, err = s.h.tool(ctx, "launchctl", "bootstrap", s.domain(), s.plistPath())
	default:
		_, err = s.h.tool(ctx, "launchctl", "kickstart", "-k", s.target())
	}
	return err
}

func (s launchdService) status(ctx context.Context) (serviceStatus, error) {
	st := serviceStatus{Binary: s.h.binary, Log: s.logPath()}
	p, err := s.installed()
	switch {
	case err == nil:
		st.Installed, st.StartAtLogin = true, p.runAtLoad
		if len(p.args) > 0 {
			st.Binary = p.args[0]
		}
		if p.log != "" {
			st.Log = p.log
		}
		st.dataDir = peekDataDir(p.args, "")
	case !errors.Is(err, os.ErrNotExist):
		return st, err
	}
	// `launchctl print` fails (113) when the label is unknown to launchd: not loaded, not an error.
	out, err := s.h.run(ctx, "launchctl", "print", s.target())
	if err != nil {
		return st, nil
	}
	st.Loaded = true
	st.PID = launchctlPID(out)
	st.Running = st.PID > 0
	return st, nil
}

// launchctlPID reads the `pid = N` line of `launchctl print`, which is present only while the job
// has a process. Anything else in that tree is ignored.
func launchctlPID(out string) int {
	for _, line := range strings.Split(out, "\n") {
		field := strings.TrimSpace(line)
		if !strings.HasPrefix(field, "pid ") {
			continue
		}
		if _, value, ok := strings.Cut(field, "="); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				return n
			}
		}
	}
	return 0
}

// renderPlist writes the LaunchAgent. KeepAlive is SuccessfulExit false, so launchd restarts a host
// that crashed and leaves alone one that was asked to stop. The argv is this binary's absolute path
// plus `serve`, and --data-dir only when the host named one: every other `serve` setting comes from
// config.json in that directory, as it does for a host who runs serve by hand.
func renderPlist(binary, dataDir, log string, atLogin bool) []byte {
	args := []string{binary, "serve"}
	if dataDir != "" {
		args = append(args, "--data-dir", dataDir)
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	plistKey(&b, 1, "Label")
	plistString(&b, 1, serviceLabel())
	plistKey(&b, 1, "ProgramArguments")
	b.WriteString("\t<array>\n")
	for _, a := range args {
		plistString(&b, 2, a)
	}
	b.WriteString("\t</array>\n")
	plistKey(&b, 1, "RunAtLoad")
	plistBool(&b, 1, atLogin)
	plistKey(&b, 1, "KeepAlive")
	b.WriteString("\t<dict>\n")
	plistKey(&b, 2, "SuccessfulExit")
	plistBool(&b, 2, false)
	b.WriteString("\t</dict>\n")
	plistKey(&b, 1, "StandardOutPath")
	plistString(&b, 1, log)
	plistKey(&b, 1, "StandardErrorPath")
	plistString(&b, 1, log)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

func plistKey(b *bytes.Buffer, depth int, name string) { plistElem(b, depth, "key", name) }

func plistString(b *bytes.Buffer, depth int, value string) { plistElem(b, depth, "string", value) }

func plistElem(b *bytes.Buffer, depth int, tag, value string) {
	b.WriteString(strings.Repeat("\t", depth) + "<" + tag + ">")
	xml.EscapeText(b, []byte(value))
	b.WriteString("</" + tag + ">\n")
}

func plistBool(b *bytes.Buffer, depth int, value bool) {
	tag := "false"
	if value {
		tag = "true"
	}
	b.WriteString(strings.Repeat("\t", depth) + "<" + tag + "/>\n")
}

// installedPlist is what this command reads back out of a LaunchAgent it wrote: the argv, which
// carries the binary and any --data-dir; where launchd sends the host's output; and RunAtLoad.
type installedPlist struct {
	args      []string
	log       string
	runAtLoad bool
}

// readPlist walks the flat <dict> this command writes. Keys of a nested dict (KeepAlive) cannot be
// confused with the top-level ones because every <key> replaces the current one.
func readPlist(raw []byte) (installedPlist, error) {
	var p installedPlist
	dec := xml.NewDecoder(bytes.NewReader(raw))
	key, inArgs := "", false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return p, nil
		}
		if err != nil {
			return installedPlist{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var name string
				if err := dec.DecodeElement(&name, &t); err != nil {
					return installedPlist{}, err
				}
				key, inArgs = name, false
			case "array":
				inArgs = key == "ProgramArguments"
			case "string":
				var value string
				if err := dec.DecodeElement(&value, &t); err != nil {
					return installedPlist{}, err
				}
				if inArgs {
					p.args = append(p.args, value)
				} else if key == "StandardOutPath" {
					p.log = value
				}
			case "true":
				if key == "RunAtLoad" {
					p.runAtLoad = true
				}
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				inArgs = false
			}
		}
	}
}

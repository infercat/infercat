package main

// RETIRE after 2026-09-25: ticket 137 removes this file and the command's tests/wiring.
import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/dirlock"
	"github.com/infercat/infercat/internal/tunnel"
)

const identityHelp = `Usage: infercat identity upgrade [--data-dir DIR]

Stop serve first. Back up the old identity once and replace it with an ic2 identity.
Every existing invite stops working and must be re-issued after starting serve.
This temporary command is scheduled for removal after 2026-09-25.
`

func (e *env) cmdIdentity(ctx context.Context, pre string, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(e.out, identityHelp)
		return nil
	}
	if args[0] != "upgrade" {
		return errUsage
	}
	fs := flag.NewFlagSet("identity upgrade", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	if err := e.parse(fs, identityHelp, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	dir, err := resolveDataDir(*dd)
	if err != nil {
		return err
	}
	guard, err := dirlock.Acquire(dir)
	if err != nil {
		return err
	}
	defer guard.Close()
	// Older binaries do not hold host.lock, so their admin endpoint must also be absent.
	if err := stoppedForUpgrade(ctx, dir); err != nil {
		return err
	}
	path := filepath.Join(dir, tunnel.KeyFile)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("identity must be a regular file, not a symlink")
	}
	old, err := tunnel.ReadIdentity(path)
	if err != nil {
		return err
	}
	if old.Format == 2 {
		fmt.Fprintln(e.out, "This identity already uses ic2; nothing changed.")
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next := tunnel.NewIdentity()
	next.Public.Region, next.Public.RegionID = old.Public.Region, old.Public.RegionID
	encoded, err := json.MarshalIndent(next, "", "\t")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeIdentityFile(tmp, encoded); err != nil {
		return err
	}
	backup := path + ".pre-ic2"
	saved, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("cannot create identity backup (never overwritten); inspect %s before retrying: %w", backup, err)
	}
	if err := writeIdentityFile(saved, raw); err != nil {
		return fmt.Errorf("backup incomplete; original identity unchanged: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("backup saved, identity replacement failed: %w", err)
	}
	fmt.Fprintf(e.out, "Identity upgraded to ic2; the old identity is backed up at %s. Every existing invite stops working and must be re-issued. Start serve, then rotate or add keys and send each friend their fresh invite.\n", backup)
	return nil
}

func writeIdentityFile(f *os.File, b []byte) error {
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// Do not interpret an unavailable token or a failed HTTP request as a stopped old binary.
func stoppedForUpgrade(ctx context.Context, dir string) error {
	network, address := "unix", filepath.Join(dir, admin.SockName)
	if runtime.GOOS == "windows" {
		raw, err := os.ReadFile(filepath.Join(dir, admin.PortName))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		port, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || port < 1 || port > 65535 {
			return errors.New("cannot verify old host: invalid admin port")
		}
		network, address = "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
	if err == nil {
		c.Close()
		return errors.New("stop serve before upgrading its identity")
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	return fmt.Errorf("cannot verify that the old host is stopped: %w", err)
}

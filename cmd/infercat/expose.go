package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/bridge"
)

// Overridden at build time for the PM's preview; no public configuration flag.
var bridgeEndpoint = "https://gateway.infercat.ai"

func (e *env) cmdExpose(ctx context.Context, dataDir string, args []string) error {
	fs := flag.NewFlagSet("expose", flag.ContinueOnError)
	fs.SetOutput(e.errw)
	fs.StringVar(&dataDir, "data-dir", dataDir, "host data directory")
	code := fs.String("register", "", "one-time registration code from Infercat")
	off := fs.Bool("off", false, "disable this host's public endpoint")
	on := fs.Bool("on", false, "enable this host's public endpoint with its stored token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errDone
		}
		return errUsage
	}
	if fs.NArg() != 0 || (*off && *on) || ((*off || *on) && *code != "") {
		return errors.New("use expose [--register CODE | --on | --off]")
	}
	dir, err := resolveDataDir(dataDir)
	if err != nil {
		return err
	}
	if *off {
		if err = bridge.SetEnabled(dir, false); err != nil {
			return err
		}
	} else {
		c, err := bridge.Load(dir)
		if err != nil {
			return err
		}
		if *code != "" {
			if c != (bridge.Config{}) {
				return errors.New("host already registered; use expose without --register")
			}
			// Verify the running host before consuming a code; never create a parallel host.
			if _, err = admin.Fetch(ctx, dir); err != nil {
				return err
			}
			c, err = bridge.Register(ctx, bridgeEndpoint, *code)
			if err != nil {
				return err
			}
			if err = bridge.Save(dir, c); err != nil {
				return fmt.Errorf("registration succeeded but saving bridge.json failed: %w; ask PM for recovery", err)
			}
		}
		if c == (bridge.Config{}) {
			return errors.New("not registered; use expose --register CODE")
		}
		if *on {
			if err = bridge.SetEnabled(dir, true); err != nil {
				return err
			}
			c.Disabled = false
		}
		fmt.Fprintf(e.out, "%s\n%s\n", c.URL(), bridge.TrustLine)
		if c.Disabled {
			fmt.Fprintln(e.out, "public endpoint disabled; use expose --on to enable it")
		}
	}
	reloadCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = admin.Reload(reloadCtx, dir); err != nil {
		return fmt.Errorf("bridge configuration saved; host reload failed: %w", err)
	}
	if *off {
		fmt.Fprintln(e.out, "public endpoint disabled")
	}
	return nil
}

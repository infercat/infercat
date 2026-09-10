package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/infercat/infercat/internal/agent"
)

func (e *env) cmdAgent(ctx context.Context, dataDir string, args []string) error {
	if len(args) == 0 || args[0] != "install" {
		fmt.Fprintln(e.out, "Usage: infercat agent install [--data-dir DIR]\nInstalls pinned Node and DeepSeek Harness privately; no system service is added.")
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			return nil
		}
		return errUsage
	}
	fs := flag.NewFlagSet("agent install", flag.ContinueOnError)
	fs.SetOutput(e.errw)
	fs.StringVar(&dataDir, "data-dir", dataDir, "host data directory")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	resolved, err := resolveDataDir(dataDir)
	if err != nil {
		return err
	}
	if err := agent.Install(ctx, resolved, e.out); err != nil {
		return err
	}
	fmt.Fprintln(e.out, "Agent runtime installed. Start the host with infercat serve --agent.")
	return nil
}

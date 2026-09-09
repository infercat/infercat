package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/infercat/infercat/internal/admin"
)

const remoteHelp = `Usage: infercat remote on|off|rotate|status [--data-dir DIR] [--json] [--no-qr]

Manage remote console access on the running host. Requires the loopback console listener.
  on       enable and print a new admin code once
  off      disable access and invalidate the code
  rotate   replace the code; existing remote sessions are refused
  status   show on/off, in-use state and any recovery warning; never the code

  --json   print the API result for scripts (on/rotate contain the secret)
  --no-qr  omit the QR; QR is also omitted when stdout is not a terminal

Anyone with the admin code controls this host's keys. Keep it private.
An action is never automatically retried. If a result is lost, check status and rotate.
`

func (e *env) cmdRemote(ctx context.Context, pre string, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(e.out, remoteHelp)
		return nil
	}
	action := args[0]
	if action != "on" && action != "off" && action != "rotate" && action != "status" {
		return errUsage
	}
	fs := flag.NewFlagSet("remote", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	noQR := fs.Bool("no-qr", false, "omit the QR")
	if err := e.parse(fs, remoteHelp, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	dir, err := resolveDataDir(*dd)
	if err != nil {
		return err
	}
	st, err := admin.Fetch(ctx, dir)
	if err != nil {
		return err
	}
	if st.Console == "" {
		return errors.New("remote access requires the loopback console listener on")
	}
	if st.Remote == nil {
		return errors.New("this host does not support remote console access")
	}
	if action == "status" {
		if *asJSON {
			return json.NewEncoder(e.out).Encode(st.Remote)
		}
		label := "off"
		if st.Remote.Enabled {
			label = "on · not in use"
			if st.Remote.InUse {
				label = "on · in use"
			}
		}
		fmt.Fprintln(e.out, "remote    "+label)
		if st.Remote.WarningFile != "" {
			fmt.Fprintf(e.out, "Remote access is off: %s is unreadable or invalid. Turn remote access on again to mint a new code.\n", st.Remote.WarningFile)
		}
		return nil
	}
	if action == "on" {
		action = "enable"
	}
	result, err := admin.RemoteAction(ctx, dir, action)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(e.out).Encode(result)
	}
	if action == "off" {
		fmt.Fprintln(e.out, "remote    off")
		return nil
	}
	if result.Invite == "" {
		return errors.New("remote action returned no code; check status and rotate")
	}
	fmt.Fprintln(e.out, "Keep this admin code private. Anyone with it controls this host's keys.")
	code := result.Invite
	if result.Link != "" {
		code = result.Link
	}
	if e.tty && !*noQR {
		writeQR(e.out, code)
	}
	fmt.Fprintf(e.out, "\n  %s\n", code)
	return nil
}

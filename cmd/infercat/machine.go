package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
	"github.com/infercat/infercat/internal/product"
)

// The first contract slice wires two read operations through the shared envelope. The
// remaining operations migrate together in 195-b; their existing human paths stay intact.
func (e *env) machineRead(ctx context.Context, args []string) (int, bool) {
	clean, enabled, parseErr := machine.Parse(args)
	if !enabled {
		return 0, false
	}
	dir, rest, splitErr := splitGlobal(clean)
	if len(rest) == 0 || (rest[0] != "version" && rest[0] != "status") {
		return 0, false
	}
	command := rest[0]
	var host machine.Host
	var data json.RawMessage
	err := parseErr
	if err == nil {
		err = splitErr
	}
	if err == nil {
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.StringVar(&dir, "data-dir", dir, dataDirUsage)
		if x := fs.Parse(rest[1:]); x != nil {
			err = &machine.Failure{Code: "invalid_arguments", Message: x.Error(), Exit: 2}
		} else if fs.NArg() != 0 {
			err = &machine.Failure{Code: "invalid_arguments", Message: command + " takes no arguments", Exit: 2}
		}
	}
	if err == nil && command == "version" {
		host = machine.Host{Version: product.Version}
		data, err = json.Marshal(struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Date    string `json:"date"`
			Schemas []int  `json:"schemas"`
		}{product.Version, product.Commit, product.Date, []int{machine.Schema}})
	} else if err == nil {
		dir, err = resolveDataDir(dir)
		if err == nil {
			var client *admin.Client
			client, err = admin.NewClient(dir)
			if err == nil {
				data, err = client.Call(ctx, "GET", "/status", nil)
			}
			if err == nil {
				err = json.Unmarshal(data, &host)
			}
		}
	}
	return machine.Write(e.out, command, host, data, err), true
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	osexec "os/exec"

	"github.com/infercat/infercat/internal/admin"
)

func (e *env) machineConsole(ctx context.Context, dir string, raw json.RawMessage) (json.RawMessage, error) {
	var st admin.Status
	if json.Unmarshal(raw, &st) != nil {
		return nil, admin.ErrResponse
	}
	link, err := consoleURL(dir, st.Console)
	if err != nil {
		return nil, err
	}
	open := e.openBrowser
	if open == nil {
		open = func(ctx context.Context, link string) error {
			for _, name := range []string{"open", "xdg-open"} {
				if path, err := osexec.LookPath(name); err == nil {
					return osexec.CommandContext(ctx, path, link).Run()
				}
			}
			return errors.New("no browser opener")
		}
	}
	if open(ctx, link) != nil {
		return nil, errors.New("could not open the local console in a browser")
	}
	return json.RawMessage(`{"opened":true}`), nil
}

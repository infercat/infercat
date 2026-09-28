package main

import (
	"context"
	"encoding/json"

	"github.com/infercat/infercat/internal/admin"
)

// Every CLI admin operation selects its transport here; renderers consume the same route body.
func (e *env) adminClient(ctx context.Context, dir string) (*admin.Client, error) {
	if e.adminClientFactory != nil {
		return e.adminClientFactory(ctx, dir)
	}
	return admin.NewClient(dir)
}

func clientStatus(ctx context.Context, client *admin.Client) (admin.Status, error) {
	var status admin.Status
	raw, err := client.Call(ctx, "GET", "/status", nil)
	if err != nil {
		return status, err
	}
	if json.Unmarshal(raw, &status) != nil {
		return status, admin.ErrResponse
	}
	return status, nil
}

// Human watch reconnects local reads after restart; a remote client retains its one tunnel.
func (e *env) refreshStatus(ctx context.Context, dir string, client *admin.Client) (admin.Status, error) {
	if client.Remote() {
		return clientStatus(ctx, client)
	}
	fresh, err := e.adminClient(ctx, dir)
	if err != nil {
		return admin.Status{}, err
	}
	defer fresh.Close()
	return clientStatus(ctx, fresh)
}

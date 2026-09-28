package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
)

// Absence is established with a read before choosing the offline path. Once a mutation
// is dispatched, every failure is returned; it can never fall back to a second file write.
func (e *env) onlineClient(ctx context.Context, dir string) (*admin.Client, error) {
	client, _, _, err := e.hostClient(ctx, dir)
	if errors.Is(err, admin.ErrNoDaemon) {
		return nil, nil
	}
	return client, err
}

func remoteKey(ctx context.Context, client *admin.Client, ref string) (*keys.Key, error) {
	raw, err := client.Call(ctx, "GET", "/keys", nil)
	if err != nil {
		return nil, err
	}
	var list []consoleKey
	if json.Unmarshal(raw, &list) != nil {
		return nil, admin.ErrResponse
	}
	var all, live []consoleKey
	convert := func(k consoleKey) *keys.Key {
		return &keys.Key{ID: k.ID, Name: k.Name, Status: k.Status, Agent: k.Agent, Limits: k.Limits, CreatedAt: k.Created}
	}
	for _, k := range list {
		if k.ID == ref {
			return convert(k), nil
		}
		if k.Name == ref {
			all = append(all, k)
			if k.Status != keys.Revoked {
				live = append(live, k)
			}
		}
	}
	if len(live) == 1 {
		return convert(live[0]), nil
	}
	if len(live) > 1 {
		return nil, fmt.Errorf("%q names %d keys; use the key id instead", ref, len(live))
	}
	if len(all) == 1 {
		return convert(all[0]), nil
	}
	if len(all) > 1 {
		return nil, fmt.Errorf("%q names %d revoked keys; use the key id instead", ref, len(all))
	}
	return nil, fmt.Errorf("%w: %q", keys.ErrNotFound, ref)
}

func callJSON(ctx context.Context, client *admin.Client, method, path string, body any, out any) error {
	var raw json.RawMessage
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	raw, err = client.Call(ctx, method, path, raw)
	if err != nil {
		return err
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return admin.ErrResponse
	}
	return nil
}

package main

import (
	"context"
	"fmt"
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/profile"
	"github.com/infercat/infercat/internal/upstream"
)

func startProfile(ctx context.Context, dir, name string, addresses map[string]string) (*profile.Runtime, error) {
	if name == "" {
		return nil, nil
	}
	in, e := profile.LoadInstallation(dir, name)
	if e != nil {
		return nil, e
	}
	p, e := profile.Builtin(in.Profile)
	if e != nil {
		return nil, e
	}
	for _, m := range p.Members {
		if url, ok := addresses[m.Class]; ok && url != "" && url != m.URL() {
			return nil, fmt.Errorf("%s conflicts with installed profile; rerun setup", m.Class)
		}
	}
	return profile.StartInstalled(ctx, dir, in)
}
func profileBindings(r *profile.Runtime, engines map[string]probeEngine) map[string]gateway.ManagedMember {
	out := map[string]gateway.ManagedMember{}
	if r == nil {
		return out
	}
	for class, engine := range engines {
		if engine == nil || !r.Owns(class) {
			continue
		}
		m, _ := r.Member(class)
		id := class
		if class == "image" {
			id = "images"
		}
		out[id] = gateway.ManagedMember{Model: m.Model.Name, Offered: func() bool { return r.Offered(class) }, Acquire: func(ctx context.Context) (func(), error) {
			release, e := r.Acquire(ctx, class)
			if e != nil {
				return nil, e
			}
			if e = engine.Refresh(ctx); e != nil {
				release()
				return nil, e
			}
			return release, nil
		}}
	}
	return out
}
func profileEmbedding(ctx context.Context, r *profile.Runtime) (upstream.Upstream, error) {
	if r != nil {
		if m, ok := r.Member("embed"); ok {
			return upstream.Open(ctx, m.URL(), "")
		}
	}
	return nil, nil
}

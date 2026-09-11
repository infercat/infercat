package profile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/supervise"
)

type InstalledMember struct {
	ID       string            `json:"id"`
	External bool              `json:"external"`
	Paths    map[string]string `json:"paths"`
}
type Installation struct {
	Version   int               `json:"version"`
	Profile   string            `json:"profile"`
	Digest    string            `json:"digest"`
	Members   []InstalledMember `json:"members"`
	Artifacts map[string]string `json:"artifacts"`
	Files     map[string]string `json:"files"`
	Links     map[string]string `json:"links"`
}

func profileDigest(p Profile) string {
	b, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// Prepare never commits host configuration. Every owned dry-check process stops
// before return; verified downloads survive any later refusal or cancellation.
func Prepare(ctx context.Context, p Profile, dir string, paths map[string]string, roots []string, client *http.Client, out io.Writer) (Installation, error) {
	in := Installation{Version: 1, Profile: p.ID, Digest: profileDigest(p), Artifacts: map[string]string{}, Files: map[string]string{}, Links: map[string]string{}}
	if err := p.Validate(); err != nil {
		return in, err
	}
	for _, m := range p.Members {
		if m.Pending != "" {
			return in, fmt.Errorf("anchor %s: %s", m.ID, m.Pending)
		}
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return in, e
	}
	dir, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return in, e
	}
	obtain := func(a Asset, existing string) (string, error) {
		path := existing
		var err error
		if path == "" {
			path, err = Fetch(ctx, a, filepath.Join(dir, "profiles", "downloads"), client, out)
			if err != nil {
				return "", err
			}
		}
		in.Files[path] = a.SHA256
		if a.Archive == nil {
			return path, nil
		}
		parent := filepath.Join(dir, "profiles", "trees")
		if err = os.MkdirAll(parent, 0700); err != nil {
			return "", err
		}
		// A fresh tree avoids trusting extracted bytes left by a previous invocation.
		stage, err := os.MkdirTemp(parent, a.ID+"-")
		if err != nil {
			return "", err
		}
		os.Remove(stage)
		if err = Unpack(ctx, path, stage, *a.Archive); err != nil {
			return "", err
		}
		err = filepath.WalkDir(stage, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				target, e := os.Readlink(path)
				if e != nil {
					return e
				}
				in.Links[path] = target
			}
			if d.Type().IsRegular() {
				sum, e := fileHash(path)
				if e != nil {
					return e
				}
				in.Files[path] = sum
			}
			return nil
		})
		return stage, err
	}
	for _, m := range p.Members {
		check := Check(ctx, m, paths, roots)
		if check.Err != nil {
			return in, check.Err
		}
		im := InstalledMember{ID: m.ID, External: check.State == "running", Paths: check.Paths}
		if im.External {
			fmt.Fprintf(out, "%s: external engine reused; active weights not attested\n", m.ID)
			in.Members = append(in.Members, im)
			continue
		}
		if m.Artifact == "" {
			fmt.Fprintf(out, "%s: unavailable; no published engine pin\n", m.ID)
			if m.Class == "text" {
				return in, fmt.Errorf("anchor has no published engine pin")
			}
			continue
		}
		if len(in.Artifacts) == 0 {
			for _, a := range p.Artifacts {
				fmt.Fprintf(out, "Engine %s: %s\n", a.ID, a.License)
				path, e := obtain(a.Asset, "")
				if e != nil {
					return in, e
				}
				in.Artifacts[a.ID] = path
			}
		}
		fmt.Fprintf(out, "Model %s licences:", m.Model.Name)
		licenses := map[string]bool{}
		for _, a := range m.Model.Assets {
			if !licenses[a.License] {
				fmt.Fprintf(out, " %s;", a.License)
				licenses[a.License] = true
			}
		}
		fmt.Fprintln(out)
		for _, a := range m.Model.Assets {
			path, e := obtain(a, check.Paths[a.ID])
			if e != nil {
				return in, e
			}
			im.Paths[a.ID] = path
		}
		command, env, work, e := Materialize(p, m, im, in.Artifacts, dir)
		if e != nil {
			return in, e
		}
		proc, e := supervise.Start(command, env, work, strings.TrimPrefix(m.URL(), "http://"))
		if e != nil {
			return in, e
		}
		ready, cancel := context.WithTimeout(ctx, 2*time.Minute)
		probeMember := m
		probeMember.Class = "probe" // readiness never repeats the anchor generation.
		e = proc.WaitHealth(ready, func(c context.Context) error {
			r := Check(c, probeMember, nil, nil)
			if r.Err != nil {
				return r.Err
			}
			if r.State != "running" {
				return fmt.Errorf("not ready")
			}
			return nil
		})
		if e == nil {
			r := Check(ready, m, nil, nil)
			e = r.Err
			if e == nil && r.State != "running" {
				e = fmt.Errorf("dry check lost engine")
			}
		}
		cancel()
		proc.Close()
		if e != nil {
			return in, fmt.Errorf("%s dry check: %w", m.ID, e)
		}
		fmt.Fprintf(out, "%s: dry check passed; owned process stopped\n", m.ID)
		in.Members = append(in.Members, im)
	}
	return in, ctx.Err()
}

func SaveInstallation(dir string, in Installation) (string, error) {
	b, e := json.MarshalIndent(in, "", "  ")
	if e != nil {
		return "", e
	}
	b = append(b, '\n')
	name := fmt.Sprintf("profiles/install-%x.json", sha256.Sum256(b))
	path := filepath.Join(dir, filepath.FromSlash(name))
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".manifest-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	return name, os.Rename(f.Name(), path)
}
func LoadInstallation(dir, name string) (Installation, error) {
	var in Installation
	if !filepath.IsLocal(name) || filepath.Dir(name) != "profiles" || !strings.HasPrefix(filepath.Base(name), "install-") {
		return in, fmt.Errorf("invalid installation path")
	}
	f, e := os.Open(filepath.Join(dir, name))
	if e != nil {
		return in, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if e != nil {
		return in, e
	}
	if len(b) > 8<<20 || filepath.Base(name) != fmt.Sprintf("install-%x.json", sha256.Sum256(b)) {
		return in, fmt.Errorf("installation size or digest mismatch")
	}
	if e = unique(json.NewDecoder(bytes.NewReader(b)), 0, ""); e != nil {
		return in, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&in); e != nil {
		return in, e
	}
	if d.Decode(new(any)) != io.EOF {
		return in, fmt.Errorf("installation trailing content")
	}
	p, e := Builtin(in.Profile)
	if e != nil || in.Version != 1 || in.Digest != profileDigest(p) {
		return in, fmt.Errorf("installation does not match embedded profile")
	}
	return in, in.validate(p)
}

func (in Installation) validate(p Profile) error {
	bad := fmt.Errorf("invalid installation paths or inventory")
	absolute := func(s string) bool { return filepath.IsAbs(s) && filepath.Clean(s) == s && clean(s) }
	if len(in.Members) > len(p.Members) || len(in.Files) > 100000 || len(in.Links) > 100000 {
		return bad
	}
	artifacts := map[string]bool{}
	for _, a := range p.Artifacts {
		artifacts[a.ID] = true
	}
	for id, path := range in.Artifacts {
		if !artifacts[id] || !absolute(path) {
			return bad
		}
	}
	seen := map[string]bool{}
	for _, im := range in.Members {
		var member *Member
		for i := range p.Members {
			if p.Members[i].ID == im.ID {
				member = &p.Members[i]
			}
		}
		if member == nil || seen[im.ID] {
			return bad
		}
		seen[im.ID] = true
		assets := map[string]bool{}
		for _, a := range member.Model.Assets {
			if !im.External && a.Archive == nil && in.Files[im.Paths[a.ID]] != a.SHA256 {
				return bad
			}
			assets[a.ID] = true
		}
		if !im.External && (len(im.Paths) != len(assets) || in.Artifacts[member.Artifact] == "") {
			return bad
		}
		if !im.External {
			for _, a := range p.Artifacts {
				if a.ID == member.Artifact && (a.Executable == "" || in.Files[filepath.Join(in.Artifacts[a.ID], a.Executable)] == "") {
					return bad
				}
			}
		}
		for id, path := range im.Paths {
			if !assets[id] || !absolute(path) {
				return bad
			}
		}
	}
	for _, m := range p.Members {
		if m.Class == "text" && !seen[m.ID] {
			return bad
		}
	}
	for path, hash := range in.Files {
		if !absolute(path) || !hashPattern.MatchString(hash) {
			return bad
		}
	}
	for path, target := range in.Links {
		if !absolute(path) || target == "" || filepath.IsAbs(target) || !clean(target) {
			return bad
		}
	}
	return nil
}

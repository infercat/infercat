package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/infercat/infercat/internal/profile"
)

func setupFixture(t *testing.T, optional bool) (profile.Profile, profile.Machine) {
	t.Helper()
	p, err := profile.Builtin("apple-64g")
	if err != nil {
		t.Fatal(err)
	}
	if optional {
		p.Members = p.Members[:2]
	} else {
		p.Members = p.Members[:1]
	}
	return p, profile.Machine{Hardware: p.Hardware, GPUName: "fixture GPU", DiskBytes: 10 << 30}
}
func setupEngine(t *testing.T, m *profile.Member, answer bool) {
	t.Helper()
	name := m.Model.Name
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": name}}})
			return
		}
		if answer {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
		} else {
			fmt.Fprint(w, `{"choices":[]}`)
		}
	}))
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	m.Port, _ = strconv.Atoi(u.Port())
}
func applyFixture(t *testing.T, dir string, p profile.Profile, m profile.Machine, paths map[string]string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	e := env{out: &out, errw: &out}
	err := e.setup(context.Background(), dir, p, true, m, paths, nil)
	return out.String(), err
}
func TestSetupMergesVerifiedSettings(t *testing.T) {
	p, m := setupFixture(t, true)
	for i := range p.Members {
		setupEngine(t, &p.Members[i], true)
	}
	dir := t.TempDir()
	before := config{Name: "keep name", Console: "off", WebURL: "https://example.com", DevListen: "127.0.0.1:9100"}
	if err := saveConfig(dir, before); err != nil {
		t.Fatal(err)
	}
	out, err := applyFixture(t, dir, p, m, nil)
	if err != nil {
		t.Fatal(err, out)
	}
	got, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := before
	want.Upstream = p.Members[0].URL()
	want.Models = p.Members[0].Model.Name
	want.Slots = 2
	want.UpstreamTranscribe = p.Members[1].URL()
	want.UpstreamTranscribeModel = p.Members[1].Model.Name
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if !strings.Contains(out, "no performance promise") || !strings.Contains(out, "active weights") {
		t.Fatal(out)
	}
	st, _ := os.Stat(configPath(dir))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}
func TestSetupRefusalNeverWrites(t *testing.T) {
	for _, kind := range []string{"URL conflict", "key conflict", "model conflict", "empty answer", "pending", "wrong pin", "unknown path", "ambiguous path", "hardware", "offline optional conflict"} {
		t.Run(kind, func(t *testing.T) {
			p, m := setupFixture(t, kind == "offline optional conflict")
			setupEngine(t, &p.Members[0], kind != "empty answer")
			cfg := config{Name: "preserve"}
			paths := map[string]string{}
			switch kind {
			case "URL conflict":
				cfg.Upstream = "http://127.0.0.1:1"
			case "key conflict":
				cfg.UpstreamKey = "do-not-print"
			case "model conflict":
				cfg.Models = "other"
			case "pending":
				floor, _ := profile.Builtin("apple-16g")
				p.Members = floor.Members[:1]
			case "wrong pin":
				f := t.TempDir() + "/bad"
				os.WriteFile(f, []byte("bad"), 0600)
				paths["anchor"] = f
			case "unknown path":
				paths["typo"] = "/missing"
			case "ambiguous path":
				paths["anchor"] = "one"
				paths["anchor-model"] = "two"
			case "hardware":
				m.RAMBytes = 1
			case "offline optional conflict":
				dead := httptest.NewServer(http.NotFoundHandler())
				u, _ := url.Parse(dead.URL)
				p.Members[1].Port, _ = strconv.Atoi(u.Port())
				dead.Close()
				cfg.UpstreamTranscribe = "http://127.0.0.1:2"
			}
			dir := t.TempDir()
			saveConfig(dir, cfg)
			before, _ := os.ReadFile(configPath(dir))
			out, err := applyFixture(t, dir, p, m, paths)
			if err == nil {
				t.Fatal("refusal missing", out)
			}
			after, _ := os.ReadFile(configPath(dir))
			if !bytes.Equal(before, after) {
				t.Fatal("refusal rewrote config")
			}
			if strings.Contains(out, "do-not-print") {
				t.Fatal("secret printed")
			}
		})
	}
}
func TestSetupUnavailableAndAbsentMembers(t *testing.T) {
	p, m := setupFixture(t, false)
	setupEngine(t, &p.Members[0], true)
	all, _ := profile.Builtin("apple-64g")
	// A profile-marked unavailable member stays unavailable even if reachable.
	embed := all.Members[3]
	embed.Unavailable = "fixture unavailable"
	setupEngine(t, &embed, true)
	p.Members = append(p.Members, embed)
	dir := t.TempDir()
	out, err := applyFixture(t, dir, p, m, nil)
	if err != nil {
		t.Fatal(err, out)
	}
	cfg, _ := loadConfig(dir)
	if cfg.Upstream != p.Members[0].URL() || cfg.UpstreamSpeech != "" {
		t.Fatal(cfg)
	}
	if !strings.Contains(out, "fixture unavailable") {
		t.Fatal(out)
	}
}
func TestSetupHelpAndFlagRefusal(t *testing.T) {
	var out bytes.Buffer
	e := env{out: &out, errw: &out}
	if err := e.cmdSetup(context.Background(), t.TempDir(), []string{"--help"}); err != errDone {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--profile", "apple-64g", "--custom", "p.json"}, {"--model-path", "bad"}, {"--model-path", "anchor=a", "--model-path", "anchor=b"}} {
		out.Reset()
		if e.cmdSetup(context.Background(), t.TempDir(), args) == nil || out.Len() == 0 {
			t.Fatal(args, out.String())
		}
	}
}

func TestSetupNeverExecutesCommands(t *testing.T) {
	p, m := setupFixture(t, false)
	setupEngine(t, &p.Members[0], true)
	dir := t.TempDir()
	marker := dir + "/should-not-exist"
	p.Members[0].Command = []string{"touch", marker}
	p.Members[0].Env = map[string]string{"PROFILE_TEST": "unused"}
	if _, err := applyFixture(t, dir, p, m, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("profile command executed")
	}
}

func TestManagedSetupCommitAndRollback(t *testing.T) {
	for _, kind := range []string{"success", "anchor fails", "manifest fails", "cancel", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			p, m := setupFixture(t, false)
			setupEngine(t, &p.Members[0], kind != "anchor fails")
			dir := t.TempDir()
			before := config{Name: "keep", Region: "region", WebURL: "https://example.com", ProfileInstall: "prior"}
			if kind == "conflict" {
				before.Upstream = "http://127.0.0.1:1"
			}
			if e := saveConfig(dir, before); e != nil {
				t.Fatal(e)
			}
			original, _ := os.ReadFile(configPath(dir))
			if kind == "manifest fails" {
				os.WriteFile(filepath.Join(dir, "profiles"), []byte("occupied"), 0600)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			var out bytes.Buffer
			e := env{out: &out, errw: &out}
			err := e.setup(ctx, dir, p, false, m, nil, nil)
			after, _ := os.ReadFile(configPath(dir))
			if kind != "success" {
				if err == nil || !bytes.Equal(original, after) {
					t.Fatalf("%v rewrote config=%v", err, !bytes.Equal(original, after))
				}
				return
			}
			if err != nil {
				t.Fatal(err, out.String())
			}
			cfg, err := loadConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Name != before.Name || cfg.Region != before.Region || cfg.WebURL != before.WebURL || cfg.Upstream != p.Members[0].URL() || cfg.ProfileInstall == before.ProfileInstall {
				t.Fatal(cfg)
			}
			manifest, err := os.ReadFile(filepath.Join(dir, cfg.ProfileInstall))
			if err != nil {
				t.Fatal(err)
			}
			var record profile.Installation
			if json.Unmarshal(manifest, &record) != nil || len(record.Members) != 1 || !record.Members[0].External {
				t.Fatal("bad installation")
			}
			// The external endpoint is still serving after setup; setup never owns it.
			check := profile.Check(context.Background(), p.Members[0], nil, nil)
			if check.State != "running" || check.Err != nil {
				t.Fatal(check)
			}
		})
	}
}

func TestManagedSetupOptionalFailurePreservesChat(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprint(declared), func(t *testing.T) {
			p, m := setupFixture(t, true)
			setupEngine(t, &p.Members[0], true)
			// The profile-marked unavailable case must not even probe the live member.
			var probes atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probes.Add(1); http.Error(w, "unavailable", 500) }))
			defer s.Close()
			u, _ := url.Parse(s.URL)
			p.Members[1].Port, _ = strconv.Atoi(u.Port())
			if declared {
				p.Members[1].Unavailable = "fixture unavailable"
			}
			dir := t.TempDir()
			before := config{Name: "preserved", UpstreamTranscribe: p.Members[1].URL(), UpstreamTranscribeModel: p.Members[1].Model.Name}
			if e := saveConfig(dir, before); e != nil {
				t.Fatal(e)
			}
			var out bytes.Buffer
			e := env{out: &out, errw: &out}
			if err := e.setup(context.Background(), dir, p, false, m, nil, nil); err != nil {
				t.Fatal(err, out.String())
			}
			cfg, err := loadConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Name != before.Name || cfg.Upstream != p.Members[0].URL() || cfg.UpstreamTranscribe != "" || cfg.UpstreamTranscribeModel != "" || cfg.ProfileInstall == "" {
				t.Fatal(cfg)
			}
			b, err := os.ReadFile(filepath.Join(dir, cfg.ProfileInstall))
			if err != nil {
				t.Fatal(err)
			}
			var in profile.Installation
			if json.Unmarshal(b, &in) != nil || in.Members[1].Unavailable == "" {
				t.Fatal(string(b))
			}
			if declared && probes.Load() != 0 {
				t.Fatal("unavailable member probed", probes.Load())
			}
		})
	}
}

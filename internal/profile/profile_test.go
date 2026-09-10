package profile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) Profile {
	t.Helper()
	p, e := Builtin("apple-64g")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestEmbeddedProfiles(t *testing.T) {
	for _, id := range []string{"apple-64g", "apple-16g", "nvidia-12g"} {
		t.Run(id, func(t *testing.T) {
			p, e := Builtin(id)
			if e != nil {
				t.Fatal(e)
			}
			if p.ID != id || p.Headroom.Friends != 2 {
				t.Fatal(p.ID)
			}
			for _, m := range p.Members {
				if m.Policy.Kind == "on-demand" && m.Policy.IdleSeconds != 600 {
					t.Fatal("idle policy")
				}
				if id == "nvidia-12g" && m.Model.Measurement.Status != "unmeasured" {
					t.Fatal("NVIDIA fabricated measurement")
				}
				if m.Class == "embed" && m.Model.Measurement.RSSBytes != 0 {
					t.Fatal("BGE-small numbers reused")
				}
			}
		})
	}
	p, _ := Builtin("apple-16g")
	if p.Members[0].Pending != "pending founder decision" || len(p.Members[0].Candidates) != 2 {
		t.Fatal("floor decision was made silently")
	}
	for _, m := range p.Members {
		if m.Class == "image" {
			t.Fatal("floor has image")
		}
	}
}
func TestStrictProfile(t *testing.T) {
	valid, _ := json.Marshal(fixture(t))
	// Nil optional slices are omitted by real files; the fixture's Marshal uses null.
	valid = bytes.ReplaceAll(valid, []byte(":null"), []byte(":[]"))
	for name, mutate := range map[string]func([]byte) []byte{
		"case alias": func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":1`), []byte(`"VERSION":1`), 1) },
		"root unknown": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"typo":1`), 1)
		},
		"nested unknown": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"engine":"llama.cpp"`), []byte(`"engine":"llama.cpp","typo":1`), 1)
		},
		"duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
		},
		"null":     func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":null`), 1) },
		"trailing": func(b []byte) []byte { return append(b, []byte(` {}`)...) },
		"oversize": func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), 256<<10)...) },
		"control":  func(b []byte) []byte { return bytes.Replace(b, []byte(`"apple-64g"`), []byte(`"apple-64g\u001b"`), 1) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := Parse(mutate(valid)); e == nil {
				t.Fatal("accepted invalid JSON schema")
			}
		})
	}
	for name, mutate := range map[string]func(*Profile){
		"version": func(p *Profile) { p.Version = 2 }, "duplicate port": func(p *Profile) { p.Members[1].Port = p.Members[0].Port },
		"unknown engine": func(p *Profile) { p.Members[0].Engine = "shell" }, "negative idle": func(p *Profile) { p.Members[1].Policy.IdleSeconds = -1 },
		"fake measurement": func(p *Profile) { p.Members[3].Model.Measurement.RSSBytes = 123 }, "negative timing": func(p *Profile) { p.Members[0].Model.Measurement.TTFTMillis = -1 },
		"hash": func(p *Profile) { p.Members[0].Model.Assets[0].SHA256 = "abc" }, "path traversal": func(p *Profile) { p.Members[0].Model.Assets[0].File = "../model.gguf" },
		"URL auth": func(p *Profile) { p.Members[0].Model.Assets[0].URL = "https://key@example.com/model" }, "empty command": func(p *Profile) { p.Members[0].Command = []string{""} },
		"no anchor": func(p *Profile) { p.Members = p.Members[1:] }, "candidate without decision": func(p *Profile) { p.Members[0].Candidates = []Model{p.Members[0].Model} },
	} {
		t.Run(name, func(t *testing.T) {
			p := fixture(t)
			mutate(&p)
			if p.Validate() == nil {
				t.Fatal("accepted invalid profile")
			}
		})
	}
}
func tiny() Asset {
	return Asset{ID: "test-model", File: "test.gguf", Bytes: 4, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("data")))}
}
func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCacheLayouts(t *testing.T) {
	for _, layout := range []string{"hf snapshot symlink", "hf snapshot copy", "hf blob", "ollama", "lmstudio", "explicit"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			a := tiny()
			var path, explicit string
			switch layout {
			case "hf snapshot symlink", "hf snapshot copy":
				path = filepath.Join(root, "models--org--repo", "snapshots", "rev", a.File)
				if strings.HasSuffix(layout, "symlink") {
					blob := filepath.Join(root, "models--org--repo", "blobs", a.SHA256)
					write(t, blob, []byte("data"))
					os.MkdirAll(filepath.Dir(path), 0700)
					if e := os.Symlink(filepath.Join("..", "..", "blobs", a.SHA256), path); e != nil {
						t.Fatal(e)
					}
				} else {
					write(t, path, []byte("data"))
				}
			case "hf blob":
				path = filepath.Join(root, "models--org--repo", "blobs", a.SHA256)
			case "ollama":
				path = filepath.Join(root, "blobs", "sha256-"+a.SHA256)
				write(t, filepath.Join(root, "manifests", "registry.ollama.ai", "org", "model", "latest"), []byte(`{"layers":[{"digest":"sha256-`+a.SHA256+`"}]}`))
			case "lmstudio":
				path = filepath.Join(root, "publisher", "repo", a.File)
			case "explicit":
				path = filepath.Join(root, "different-name.bin")
				explicit = path
			}
			if !strings.HasPrefix(layout, "hf snapshot") {
				write(t, path, []byte("data"))
			}
			got, e := Find(context.Background(), a, explicit, []string{root})
			if e != nil || got == "" {
				t.Fatal(got, e)
			}
		})
	}
}
func TestCacheRefusesFalsePins(t *testing.T) {
	a := tiny()
	root := t.TempDir()
	path := filepath.Join(root, "pub", "repo", a.File)
	write(t, path, []byte("fake"))
	if p, e := Find(context.Background(), a, "", []string{root}); p != "" || e != nil {
		t.Fatal(p, e)
	}
	if _, e := Find(context.Background(), a, path, nil); e == nil {
		t.Fatal("wrong hash accepted")
	}
	write(t, path, []byte("larger"))
	if _, e := Find(context.Background(), a, path, nil); e == nil {
		t.Fatal("wrong size accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Find(ctx, a, path, nil); e == nil {
		t.Fatal("cancellation lost")
	}
}
func TestCacheEnvironment(t *testing.T) {
	env := map[string]string{"HF_HOME": "/hf", "OLLAMA_MODELS": "/ollama"}
	get := func(k string) string { return env[k] }
	r := Roots("/home/person", get)
	if r[0] != "/hf/hub" || r[1] != "/ollama" {
		t.Fatal(r)
	}
	env["HF_HUB_CACHE"] = "/exact"
	if Roots("/home/person", get)[0] != "/exact" {
		t.Fatal("HF precedence")
	}
	delete(env, "HF_HUB_CACHE")
	delete(env, "HF_HOME")
	env["XDG_CACHE_HOME"] = "/xdg"
	if Roots("/home/person", get)[0] != "/xdg/huggingface/hub" {
		t.Fatal("XDG ignored")
	}
}
func TestHardwareDetection(t *testing.T) {
	for _, tc := range []struct{ name, os, arch, ram, gpu, sm, profile string }{
		{"apple64", "darwin", "arm64", "68719476736", `{"SPDisplaysDataType":[{"sppci_model":"Apple M5 Max"}]}`, "", "apple-64g"},
		{"apple16", "darwin", "arm64", "17179869184", `{"SPDisplaysDataType":[{"sppci_model":"Apple M2"}]}`, "", "apple-16g"},
		{"nvidia", "linux", "amd64", "MemTotal: 33554432 kB", "", "NVIDIA RTX, 12288\n", "nvidia-12g"},
		{"small GPU", "linux", "amd64", "MemTotal: 33554432 kB", "", "NVIDIA RTX, 8192\n", ""},
		{"unknown", "linux", "amd64", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, n string, a ...string) []byte {
				switch n {
				case "sysctl", "cat":
					return []byte(tc.ram)
				case "system_profiler":
					return []byte(tc.gpu)
				case "nvidia-smi":
					return []byte(tc.sm)
				case "df":
					return []byte("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/test 1000 200 800 20% /\n")
				}
				return nil
			}
			m := Detect(context.Background(), tc.os, tc.arch, t.TempDir(), run)
			if m.Propose() != tc.profile || m.DiskBytes != 819200 {
				t.Fatal(m, m.Propose())
			}
		})
	}
}
func serverMember(t *testing.T, h http.HandlerFunc) (Member, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(h)
	u, _ := url.Parse(s.URL)
	port, _ := strconv.Atoi(u.Port())
	m := fixture(t).Members[0]
	m.Port = port
	m.Model.Assets = []Asset{tiny()}
	return m, s
}
func TestDryProbe(t *testing.T) {
	for _, mode := range []string{"ok", "wrong model", "bad health", "empty answer", "oversize", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			prompts := 0
			m, s := serverMember(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected credential")
				}
				if r.URL.Path == "/v1/models" {
					switch mode {
					case "wrong model":
						fmt.Fprint(w, `{"data":[{"id":"other"}]}`)
					case "bad health":
						w.WriteHeader(503)
					case "oversize":
						fmt.Fprint(w, strings.Repeat("x", (1<<20)+1))
					case "redirect":
						http.Redirect(w, r, "https://example.com", 302)
					default:
						fmt.Fprint(w, `{"data":[{"id":"gemma4-e4b"}]}`)
					}
					return
				}
				prompts++
				if mode == "empty answer" {
					fmt.Fprint(w, `{"choices":[]}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
				}
			})
			defer s.Close()
			r := Check(context.Background(), m, nil, nil)
			if mode == "ok" {
				if r.Err != nil || r.State != "running" || prompts != 1 || !strings.Contains(r.Detail, "not attested") {
					t.Fatal(r, prompts)
				}
			} else if r.Err == nil {
				t.Fatal("bad engine accepted", r)
			}
		})
	}
}
func TestAbsentAndCancelledProbe(t *testing.T) {
	m, s := serverMember(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := Check(ctx, m, nil, nil)
	if r.Err == nil {
		t.Fatal("cancelled probe accepted")
	}
	s.Close()
	if r = Check(context.Background(), m, nil, nil); r.State != "missing" || r.Err != nil {
		t.Fatal(r)
	}
}

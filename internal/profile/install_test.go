package profile

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/supervise"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "_profile-guardian" {
		os.Exit(supervise.Guardian(os.Args[2:], false))
	}
	if len(os.Args) > 3 && os.Args[1] == "_profile-fixture" {
		if os.Getenv("PROFILE_START_DELAY") == "1" {
			time.Sleep(500 * time.Millisecond)
		}
		os.WriteFile(os.Getenv("PROFILE_PID"), []byte(strconv.Itoa(os.Getpid())), 0600)
		listener, err := net.Listen("tcp4", "127.0.0.1:"+os.Args[2])
		if err != nil {
			os.Exit(1)
		}
		if path := os.Getenv("PROFILE_PORT"); path != "" {
			os.WriteFile(path+".tmp", []byte(strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)), 0600)
			os.Rename(path+".tmp", path)
		}
		http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if os.Getenv("PROFILE_STALL") == "1" {
				<-r.Context().Done()
				return
			}
			if r.URL.Path == "/v1/models" {
				fmt.Fprintf(w, `{"data":[{"id":%q}]}`, os.Args[3])
			} else if os.Getenv("PROFILE_BAD_ANSWER") == "1" {
				fmt.Fprint(w, `{"choices":[]}`)
			} else {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
			}
		}))
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func pin(data []byte, url string) Asset {
	return Asset{ID: "test", File: "test.bin", Bytes: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), URL: url, License: "MIT", Revision: "fixture"}
}
func TestFetchResumeAndRefusals(t *testing.T) {
	data := []byte("verified bytes")
	for _, kind := range []string{"fresh", "resume", "ignored range", "short", "oversize", "bad hash", "wrong range", "redirect", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch kind {
				case "resume":
					if r.Header.Get("Range") != "bytes=4-" {
						t.Error(r.Header)
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 4-%d/%d", len(data)-1, len(data)))
					w.WriteHeader(206)
					w.Write(data[4:])
				case "wrong range":
					w.Header().Set("Content-Range", "bytes 0-1/2")
					w.WriteHeader(206)
					w.Write(data)
				case "short":
					w.Write(data[:4])
				case "oversize":
					w.Write(append(data, '!'))
				case "bad hash":
					w.Write(bytes.Repeat([]byte("x"), len(data)))
				case "redirect":
					http.Redirect(w, r, "http://127.0.0.1:1", 302)
				default:
					w.Write(data)
				}
			}))
			defer s.Close()
			a := pin(data, s.URL)
			dir := t.TempDir()
			target := filepath.Join(dir, a.SHA256+"-"+a.File)
			if kind == "resume" || kind == "ignored range" || kind == "wrong range" {
				os.WriteFile(target+".part", data[:4], 0600)
			}
			if kind == "symlink" {
				os.Symlink(filepath.Join(dir, "victim"), target+".part")
			}
			got, e := Fetch(context.Background(), a, dir, s.Client(), io.Discard)
			success := kind == "fresh" || kind == "resume" || kind == "ignored range"
			if (e == nil) != success {
				t.Fatalf("got %s error %v", got, e)
			}
			if success {
				b, _ := os.ReadFile(got)
				if !bytes.Equal(b, data) {
					t.Fatal("bad bytes")
				}
				Fetch(context.Background(), a, dir, s.Client(), io.Discard)
				if requests != 1 {
					t.Fatal("cache redownload")
				}
			} else {
				if _, e := os.Stat(target); !os.IsNotExist(e) {
					t.Fatal("unverified final published")
				}
			}
		})
	}
}
func tarBytes(t *testing.T, entries []*tar.Header, contents []string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for i, h := range entries {
		if e := tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(contents[i]))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func TestArchiveBoundaries(t *testing.T) {
	for _, kind := range []string{"good", "traversal", "absolute", "duplicate", "device", "hardlink", "escape link", "cycle", "link child", "too big"} {
		t.Run(kind, func(t *testing.T) {
			entries := []*tar.Header{{Name: "root/file", Mode: 0700, Size: 3, Typeflag: tar.TypeReg}}
			contents := []string{"yes"}
			bound := int64(20)
			switch kind {
			case "good":
				entries = append(entries, &tar.Header{Name: "root/link", Typeflag: tar.TypeSymlink, Linkname: "file"})
				contents = append(contents, "")
			case "traversal":
				entries[0].Name = "root/../escape"
			case "absolute":
				entries[0].Name = "/absolute"
			case "duplicate":
				entries = append(entries, entries[0])
				contents = append(contents, "yes")
			case "device":
				entries[0].Typeflag = tar.TypeChar
				entries[0].Size = 0
			case "hardlink":
				entries[0].Typeflag = tar.TypeLink
				entries[0].Size = 0
				entries[0].Linkname = "file"
			case "escape link", "cycle", "link child":
				entries = append(entries, &tar.Header{Name: "root/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"})
				contents = append(contents, "")
				if kind == "cycle" {
					entries[1].Linkname = "link"
				}
				if kind == "link child" {
					entries[1].Linkname = "file"
					entries = append(entries, &tar.Header{Name: "root/link/child", Typeflag: tar.TypeReg, Mode: 0600, Size: 3})
					contents = append(contents, "yes")
				}
			case "too big":
				bound = 2
			}
			dir := t.TempDir()
			archive := filepath.Join(dir, "a.tgz")
			os.WriteFile(archive, tarBytes(t, entries, contents), 0600)
			out := filepath.Join(dir, "tree")
			e := Unpack(context.Background(), archive, out, Archive{"tar.gz", 1, bound})
			if (e == nil) != (kind == "good") {
				t.Fatal(e)
			}
			if kind == "good" {
				b, _ := os.ReadFile(filepath.Join(out, "link"))
				if string(b) != "yes" {
					t.Fatal("link")
				}
			} else if _, e := os.Stat(out); !os.IsNotExist(e) {
				t.Fatal("partial tree left")
			}
		})
	}
}
func TestZipExtraction(t *testing.T) {
	for _, name := range []string{"bin/server", "../escape"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(name)
		w.Write([]byte("yes"))
		z.Close()
		dir := t.TempDir()
		f := filepath.Join(dir, "a.zip")
		os.WriteFile(f, b.Bytes(), 0600)
		e := Unpack(context.Background(), f, filepath.Join(dir, "out"), Archive{"zip", 0, 10})
		if (e == nil) != (name == "bin/server") {
			t.Fatal(e)
		}
	}
}
func TestPrepareOwnedEngineStopsAndPendingRefuses(t *testing.T) {
	for _, mode := range []string{"served", "bad answer", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			bad := mode != "served"
			p := fixture(t)
			p.Members = p.Members[:1]
			m := &p.Members[0]
			l, _ := net.Listen("tcp4", "127.0.0.1:0")
			m.Port = l.Addr().(*net.TCPAddr).Port
			l.Close()
			exe, _ := os.Executable()
			script := []byte("#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' _profile-fixture \"$@\"\n")
			archive := tarBytes(t, []*tar.Header{{Name: "root/engine", Typeflag: tar.TypeReg, Mode: 0700, Size: int64(len(script))}}, []string{string(script)})
			model := []byte("model")
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/engine" {
					w.Write(archive)
				} else {
					w.Write(model)
				}
			}))
			defer s.Close()
			a := pin(archive, s.URL+"/engine")
			a.ID = "engine"
			a.Archive = &Archive{"tar.gz", 1, 1 << 20}
			p.Artifacts = []Artifact{{a, "engine"}}
			a = pin(model, s.URL+"/model")
			a.ID = "weights"
			m.Model.Assets = []Asset{a}
			m.Artifact = "engine"
			m.Command = []string{"engine", "{port}", "{model_name}"}
			dir := t.TempDir()
			pid := filepath.Join(dir, "pid")
			m.Env = map[string]string{"PROFILE_PID": pid}
			if mode == "bad answer" {
				m.Env["PROFILE_BAD_ANSWER"] = "1"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if mode == "cancel" {
				m.Env["PROFILE_STALL"] = "1"
				go func() {
					for ctx.Err() == nil {
						if _, e := os.Stat(pid); e == nil {
							cancel()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()
			}
			var out bytes.Buffer
			in, e := Prepare(ctx, p, dir, nil, nil, s.Client(), &out)
			if (e == nil) == bad {
				t.Fatal(e, out.String())
			}
			if !bad && (len(in.Members) != 1 || in.Members[0].External) {
				t.Fatal(in)
			}
			c, e := net.DialTimeout("tcp", strings.TrimPrefix(m.URL(), "http://"), time.Second)
			if e == nil {
				c.Close()
				t.Fatal("owned engine leaked")
			}
			if _, e = os.Stat(pid); e != nil {
				t.Fatal("fixture never launched")
			}
			// Pending anchor fails before another publisher request or installation.
			floor, _ := Builtin("apple-16g")
			if _, e = Prepare(context.Background(), floor, t.TempDir(), nil, nil, s.Client(), io.Discard); e == nil {
				t.Fatal("pending anchor accepted")
			}
		})
	}
}
func TestInstallationStrictRecord(t *testing.T) {
	p := fixture(t)
	in := Installation{Version: 1, Profile: p.ID, Digest: profileDigest(p), Members: []InstalledMember{{ID: "anchor", External: true, Paths: map[string]string{}}}, Artifacts: map[string]string{}, Files: map[string]string{}, Links: map[string]string{}}
	dir := t.TempDir()
	name, e := SaveInstallation(dir, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = LoadInstallation(dir, name); e != nil {
		t.Fatal(e)
	}
	in.Members = append(in.Members, in.Members[0])
	name, _ = SaveInstallation(dir, in)
	if _, e = LoadInstallation(dir, name); e == nil {
		t.Fatal("duplicate member accepted")
	}
	b, _ := json.Marshal(in)
	os.WriteFile(filepath.Join(dir, name), b, 0600)
	if _, e = LoadInstallation(dir, name); e == nil {
		t.Fatal("content hash ignored")
	}
}

func TestFetchCancellationKeepsOnlyResumablePartial(t *testing.T) {
	data := []byte("verified bytes")
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data[:4])
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer s.Close()
	a := pin(data, s.URL)
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { _, e := Fetch(ctx, a, dir, s.Client(), io.Discard); done <- e }()
	<-entered
	cancel()
	if e := <-done; e == nil {
		t.Fatal("cancel ignored")
	}
	target := filepath.Join(dir, a.SHA256+"-"+a.File)
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("cancel published final")
	}
	b, e := os.ReadFile(target + ".part")
	if e != nil || len(b) > 4 {
		t.Fatal(e, len(b))
	}
}
func TestCanceledExtractionLeavesNoTree(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tgz")
	os.WriteFile(archive, tarBytes(t, []*tar.Header{{Name: "file", Size: 3, Mode: 0600, Typeflag: tar.TypeReg}}, []string{"yes"}), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(dir, "tree")
	if e := Unpack(ctx, archive, dest, Archive{"tar.gz", 0, 20}); e == nil {
		t.Fatal("cancel ignored")
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("partial extraction left")
	}
}
func TestMaterializeMemberKinds(t *testing.T) {
	p := fixture(t)
	for _, member := range p.Members {
		t.Run(member.Class, func(t *testing.T) {
			m := member
			m.Artifact = "fixture"
			p.Artifacts = []Artifact{{Asset: Asset{ID: "fixture"}, Executable: "bin/engine"}}
			root := t.TempDir()
			paths := map[string]string{}
			for _, a := range m.Model.Assets {
				paths[a.ID] = filepath.Join(root, a.File)
			}
			if m.Class == "speech" {
				m.Command = []string{"infercat-speech", "--model-dir", "{asset:speech-support}", "--listen", "127.0.0.1:{port}"}
			}
			command, env, work, e := Materialize(p, m, InstalledMember{ID: m.ID, Paths: paths}, map[string]string{"fixture": root, "sherpa": root}, root)
			if e != nil {
				t.Fatal(e)
			}
			if command[0] != filepath.Join(root, "bin/engine") || !strings.Contains(strings.Join(env, "\n"), "HOME="+work) {
				t.Fatal(command, env)
			}
			if m.Class == "speech" && !strings.Contains(strings.Join(env, "\n"), "DYLD_LIBRARY_PATH="+filepath.Join(root, "lib")) {
				t.Fatal("speech library path is not pinned", env)
			}
			for _, arg := range command {
				if strings.ContainsAny(arg, "{}") {
					t.Fatal("unexpanded", arg)
				}
			}
			if m.Engine == "audio.cpp" {
				b, e := os.ReadFile(filepath.Join(work, "engine.json"))
				if e != nil {
					t.Fatal(e)
				}
				var cfg struct {
					Port   int
					Models []struct{ Path, Family, ID string }
				}
				if json.Unmarshal(b, &cfg) != nil || cfg.Port != m.Port || len(cfg.Models) != 1 || cfg.Models[0].Path != paths[m.Model.Assets[0].ID] || cfg.Models[0].Family != "fun_asr_nano" {
					t.Fatal(string(b))
				}
				os.Remove(filepath.Join(work, "engine.json"))
				victim := filepath.Join(root, "victim")
				os.WriteFile(victim, []byte("keep"), 0600)
				os.Symlink(victim, filepath.Join(work, "engine.json"))
				if _, _, _, e = Materialize(p, m, InstalledMember{Paths: paths}, map[string]string{"fixture": root}, root); e == nil {
					t.Fatal("config symlink followed")
				}
				b, _ = os.ReadFile(victim)
				if string(b) != "keep" {
					t.Fatal("victim rewritten")
				}
			}
		})
	}
}
func TestInstallationRefusesInvalidInventory(t *testing.T) {
	for _, kind := range []string{"missing anchor", "unknown member", "relative file", "bad hash", "unknown asset", "unknown artifact", "absolute link", "missing owned hashes"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t)
			in := Installation{Version: 1, Profile: p.ID, Digest: profileDigest(p), Members: []InstalledMember{{ID: "anchor", External: true, Paths: map[string]string{}}}, Artifacts: map[string]string{}, Files: map[string]string{}, Links: map[string]string{}}
			switch kind {
			case "missing anchor":
				in.Members = []InstalledMember{}
			case "unknown member":
				in.Members[0].ID = "unknown"
			case "relative file":
				in.Files["relative"] = "bad"
			case "bad hash":
				in.Files["/tmp/file"] = "bad"
			case "unknown asset":
				in.Members[0].Paths["unknown"] = "/tmp/model"
			case "unknown artifact":
				in.Artifacts["unknown"] = "/tmp/tree"
			case "missing owned hashes":
				in.Members[0].External = false
				in.Artifacts["llama"] = "/tmp/engine"
				for _, a := range p.Members[0].Model.Assets {
					in.Members[0].Paths[a.ID] = "/tmp/" + a.File
				}
			case "absolute link":
				in.Links["/tmp/link"] = "/outside"
			}
			dir := t.TempDir()
			name, e := SaveInstallation(dir, in)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = LoadInstallation(dir, name); e == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}
func TestManagedBusyPortAndEarlyExit(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	dir := t.TempDir()
	if p, e := supervise.Start([]string{"/not-an-engine"}, nil, dir, l.Addr().String()); e == nil {
		p.Close()
		t.Fatal("busy port accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "engine.log")); !os.IsNotExist(e) {
		t.Fatal("busy refusal wrote log")
	}
	address := l.Addr().String()
	l.Close()
	p, e := supervise.Start([]string{"/not-an-engine"}, nil, dir, address)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = p.WaitHealth(ctx, func(context.Context) error { return fmt.Errorf("not ready") }); e == nil {
		t.Fatal("dead child considered ready")
	}
}

func TestDegradedInstallationReloadAndChangedProfile(t *testing.T) {
	p := fixture(t)
	in := Installation{Version: 1, Profile: p.ID, Digest: profileDigest(p), Artifacts: map[string]string{}, Files: map[string]string{}, Links: map[string]string{}}
	for _, m := range p.Members {
		im := InstalledMember{ID: m.ID, Paths: map[string]string{}, Unavailable: "publisher unavailable"}
		if m.Class == "text" {
			im.External = true
			im.Unavailable = ""
		}
		in.Members = append(in.Members, im)
	}
	dir := t.TempDir()
	name, e := SaveInstallation(dir, in)
	if e != nil {
		t.Fatal(e)
	}
	loaded, e := LoadInstallation(dir, name)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := StartInstalled(context.Background(), dir, loaded)
	if e != nil {
		t.Fatal(e)
	}
	runtime.Close()
	in.Digest = strings.Repeat("0", 64)
	name, e = SaveInstallation(dir, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = LoadInstallation(dir, name); e == nil {
		t.Fatal("changed embedded digest accepted")
	}
}

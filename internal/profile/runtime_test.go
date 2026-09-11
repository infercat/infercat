//go:build darwin || linux

package profile

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func runtimeFixture(t *testing.T, policy string, classes ...string) (*Runtime, *managed, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	art := filepath.Join(root, "engine")
	os.Mkdir(art, 0700)
	exe, _ := os.Executable()
	script := []byte("#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' _profile-fixture \"$@\"\n")
	bin := filepath.Join(art, "engine")
	os.WriteFile(bin, script, 0700)
	model := filepath.Join(root, "weights")
	os.WriteFile(model, []byte("model"), 0600)
	p := fixture(t)
	m := p.Members[0]
	if len(classes) > 0 {
		m.Class = classes[0]
	}
	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	m.Port = l.Addr().(*net.TCPAddr).Port
	l.Close()
	m.Artifact = "engine"
	m.Command = []string{"engine", "{port}", "{model_name}"}
	m.Policy = Policy{Kind: policy}
	if policy == "on-demand" {
		m.Policy.IdleSeconds = 1
	}
	m.Model.Assets = []Asset{pin([]byte("model"), "https://example.test/model")}
	m.Env = map[string]string{"PROFILE_PID": filepath.Join(root, "pid")}
	p.Artifacts = []Artifact{{Asset: Asset{ID: "engine"}, Executable: "engine"}}
	im := InstalledMember{ID: m.ID, Paths: map[string]string{"test": model}}
	bh, _ := fileHash(bin)
	mh, _ := fileHash(model)
	in := Installation{Artifacts: map[string]string{"engine": art}, Files: map[string]string{bin: bh, model: mh}, Links: map[string]string{}}
	ctx, cancel := context.WithCancel(context.Background())
	v := &managed{ctx: ctx, member: m, installed: im, installation: in, profile: p, dir: root, status: MemberStatus{ID: m.ID, State: "dormant"}, done: make(chan struct{})}
	r := &Runtime{members: map[string]*managed{m.Class: v}, cancel: cancel}
	go v.loop()
	t.Cleanup(r.Close)
	return r, v, root
}
func waitState(t *testing.T, r *Runtime, want string) {
	t.Helper()
	until := time.Now().Add(6 * time.Second)
	for time.Now().Before(until) {
		if r.Status()[0].State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state %+v want %s", r.Status(), want)
}
func TestManagedCoalescesAndOnlyFinalReleaseStartsIdle(t *testing.T) {
	r, m, root := runtimeFixture(t, "on-demand")
	var wg sync.WaitGroup
	releases := make(chan func(), 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, e := r.Acquire(context.Background(), "text")
			if e != nil {
				errs <- e
				return
			}
			releases <- release
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if len(releases) != 8 || r.Status()[0].Restarts != 0 {
		t.Fatal(r.Status(), len(releases))
	}
	first, _ := os.ReadFile(filepath.Join(root, "pid"))
	for range 7 {
		(<-releases)()
	}
	time.Sleep(1500 * time.Millisecond)
	if r.Status()[0].State != "ready" {
		t.Fatal("idle killed active lease")
	}
	(<-releases)()
	waitState(t, r, "dormant")
	if !r.Offered("text") || r.Status()[0].Healthy {
		t.Fatal("dormant offer fabricated health")
	}
	release, e := r.Acquire(context.Background(), "text")
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	next, _ := os.ReadFile(filepath.Join(root, "pid"))
	if string(first) == string(next) {
		t.Fatal("engine did not restart")
	}
	m.mu.Lock()
	refs := m.refs
	m.mu.Unlock()
	if refs != 1 {
		t.Fatal(refs)
	}
}
func TestManagedPinChangeRefusesNextStart(t *testing.T) {
	r, _, root := runtimeFixture(t, "on-demand")
	release, e := r.Acquire(context.Background(), "text")
	if e != nil {
		t.Fatal(e)
	}
	release()
	waitState(t, r, "dormant")
	os.WriteFile(filepath.Join(root, "weights"), []byte("changed"), 0600)
	if release, e = r.Acquire(context.Background(), "text"); e == nil {
		release()
		t.Fatal("changed pin executed")
	}
	if r.Offered("text") {
		t.Fatal("failed engine advertised")
	}
}
func TestRetireOnlyUnreferencedOwnedTrees(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	trees := filepath.Join(root, "profiles", "trees")
	for _, name := range []string{"old", "kept"} {
		os.MkdirAll(filepath.Join(trees, name), 0700)
		os.WriteFile(filepath.Join(trees, name, "file"), []byte(name), 0600)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "keep"), []byte("safe"), 0600)
	os.Symlink(outside, filepath.Join(trees, "link"))
	in := Installation{Artifacts: map[string]string{"engine": filepath.Join(trees, "kept")}}
	if e := RetireTrees(root, in); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(trees, "old")); !os.IsNotExist(e) {
		t.Fatal("old tree retained")
	}
	for _, p := range []string{filepath.Join(trees, "kept", "file"), filepath.Join(outside, "keep")} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCommandLineQuotesShellMetacharacters(t *testing.T) {
	s := CommandLine(InstalledMember{Directory: "/tmp/a'b", Command: []string{"/bin/echo", "$(false)", "a b"}, Env: []string{"X=a b"}})
	if !strings.Contains(s, "'$(false)'") || !strings.Contains(s, "'X=a b'") || !strings.Contains(s, "'\\''") {
		t.Fatal(fmt.Sprint(s))
	}
}

func TestManagedCanceledWaiterDoesNotCancelSharedStart(t *testing.T) {
	r, m, root := runtimeFixture(t, "on-demand")
	m.member.Env["PROFILE_START_DELAY"] = "1"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := r.Acquire(ctx, "text"); e == nil {
		t.Fatal("canceled request admitted")
	}
	if _, e := os.Stat(filepath.Join(root, "pid")); !os.IsNotExist(e) {
		t.Fatal("canceled request started engine")
	}
	ctx, cancel = context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		release, e := r.Acquire(ctx, "text")
		if e == nil {
			release()
		}
		errc <- e
	}()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		m.mu.Lock()
		starting := m.starting != nil
		m.mu.Unlock()
		if starting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan func(), 1)
	go func() {
		release, e := r.Acquire(context.Background(), "text")
		if e != nil {
			t.Error(e)
		}
		done <- release
	}()
	cancel()
	if e := <-errc; e != context.Canceled {
		t.Fatal(e)
	}
	release := <-done
	if release == nil {
		t.Fatal("shared startup canceled")
	}
	release()
	if r.Status()[0].Restarts != 0 {
		t.Fatal(r.Status())
	}
}
func TestManagedResidentRestartsAndShutdownClosesPort(t *testing.T) {
	r, m, root := runtimeFixture(t, "resident")
	waitState(t, r, "ready")
	b, e := os.ReadFile(filepath.Join(root, "pid"))
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(string(b))
	if e != nil {
		t.Fatal(e)
	}
	proc, e := os.FindProcess(pid)
	if e != nil {
		t.Fatal(e)
	}
	if e = proc.Kill(); e != nil {
		t.Fatal(e)
	}
	until := time.Now().Add(7 * time.Second)
	for time.Now().Before(until) {
		b, _ = os.ReadFile(filepath.Join(root, "pid"))
		if string(b) != strconv.Itoa(pid) && r.Status()[0].State == "ready" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(b) == strconv.Itoa(pid) || r.Status()[0].Restarts == 0 || !r.Status()[0].Healthy {
		t.Fatal(r.Status())
	}
	r.Close()
	conn, e := net.DialTimeout("tcp", strings.TrimPrefix(m.member.URL(), "http://"), time.Second)
	if e == nil {
		conn.Close()
		t.Fatal("owned engine survived host close")
	}
}

func TestManagedLifecyclePerMemberClass(t *testing.T) {
	for _, class := range []string{"transcribe", "speech", "embed", "image"} {
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			r, _, _ := runtimeFixture(t, "on-demand", class)
			if !r.Offered(class) || r.Status()[0].Healthy {
				t.Fatal(r.Status())
			}
			release, e := r.Acquire(context.Background(), class)
			if e != nil {
				t.Fatal(e)
			}
			if !r.Status()[0].Healthy {
				t.Fatal(r.Status())
			}
			release()
			waitState(t, r, "dormant")
		})
	}
}

func TestManagedParentFixture(t *testing.T) {
	dir := os.Getenv("INFERCAT_PROFILE_PARENT")
	if dir == "" {
		return
	}
	r, m, root := runtimeFixture(t, "resident")
	waitState(t, r, "ready")
	pid, e := os.ReadFile(filepath.Join(root, "pid"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "child"), []byte(string(pid)+" "+strings.TrimPrefix(m.member.URL(), "http://")), 0600); e != nil {
		t.Fatal(e)
	}
	select {}
}
func TestManagedGuardianAfterHostDeath(t *testing.T) {
	dir := t.TempDir()
	binary, _ := os.Executable()
	parent := exec.Command(binary, "-test.run=^TestManagedParentFixture$")
	parent.Env = []string{"INFERCAT_PROFILE_PARENT=" + dir}
	if e := parent.Start(); e != nil {
		t.Fatal(e)
	}
	defer parent.Process.Kill()
	var fields []string
	until := time.Now().Add(6 * time.Second)
	for time.Now().Before(until) {
		b, _ := os.ReadFile(filepath.Join(dir, "child"))
		fields = strings.Fields(string(b))
		if len(fields) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(fields) != 2 {
		t.Fatal("parent did not start owned member")
	}
	pid, e := strconv.Atoi(fields[0])
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if e = parent.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	parent.Wait()
	until = time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		conn, e := net.DialTimeout("tcp", fields[1], 50*time.Millisecond)
		if e != nil && syscall.Kill(pid, 0) != nil {
			return
		}
		if conn != nil {
			conn.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("managed member survived host death")
}

func TestManagedFailedStartDoesNotRetryWithoutLease(t *testing.T) {
	r, m, root := runtimeFixture(t, "on-demand", "image")
	m.mu.Lock()
	m.member.Policy.IdleSeconds = 600
	m.mu.Unlock()
	busy, e := net.Listen("tcp4", strings.TrimPrefix(m.member.URL(), "http://"))
	if e != nil {
		t.Fatal(e)
	}
	defer busy.Close()
	if release, e := r.Acquire(context.Background(), "image"); e == nil {
		release()
		t.Fatal("occupied port accepted")
	}
	before := r.Status()[0]
	if !strings.Contains(before.Error, "engine port unavailable") {
		t.Fatal(before)
	}
	// A verification attempt would now fail differently. Keep the real 600 s idle
	// policy and wait beyond the failed start's one-second backoff and loop tick.
	model := filepath.Join(root, "weights")
	if e = os.WriteFile(model, []byte("changed pin: verification tripwire"), 0600); e != nil {
		t.Fatal(e)
	}
	unchanged := func() {
		t.Helper()
		time.Sleep(2500 * time.Millisecond)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.refs != 0 || m.starting != nil || m.process != nil || m.status != before {
			t.Fatalf("zero-lease retry/verification: before=%+v after=%+v refs=%d", before, m.status, m.refs)
		}
	}
	unchanged()
	// Restoring the pin and freeing the port still must not launch unrequested work.
	if e = os.WriteFile(model, []byte("model"), 0600); e != nil {
		t.Fatal(e)
	}
	busy.Close()
	unchanged()
	if _, e = os.Stat(filepath.Join(root, "pid")); !os.IsNotExist(e) {
		t.Fatal("unrequested engine started")
	}
	release, e := r.Acquire(context.Background(), "image")
	if e != nil {
		t.Fatal(e)
	}
	release()
	if !r.Status()[0].Healthy {
		t.Fatal("explicit demand did not recover")
	}
}

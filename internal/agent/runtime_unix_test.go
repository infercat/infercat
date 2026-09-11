//go:build darwin || linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/supervise"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "_sandbox-canary" {
		os.Exit(sandboxCanary(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "_confine" {
		os.Exit(Confine(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "_agent-guardian" {
		os.Exit(supervise.Guardian(os.Args[2:], true))
	}
	os.Exit(m.Run())
}
func TestHarnessFixture(t *testing.T) {
	if os.Getenv("INFERCAT_AGENT_FIXTURE") == "" {
		return
	}
	if os.Getenv("INFERCAT_AGENT_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	health := os.NewFile(3, "health")
	listener, err := net.FileListener(health)
	if err != nil {
		os.Exit(3)
	}
	health.Close()
	var healthy atomic.Bool
	healthy.Store(true)
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, "ready")
	}))
	fmt.Printf("{\"pid\":%d}\n", os.Getpid())
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if string(scanner.Bytes()) == `{"close_output":true}` {
			os.Stdout.Close()
			select {}
		}
		if string(scanner.Bytes()) == `{"unhealthy":true}` {
			healthy.Store(false)
		}
		fmt.Println(string(scanner.Bytes()))
	}
	if os.Getenv("INFERCAT_AGENT_IGNORE_TERM") == "1" {
		select {}
	}
}
func runtimeFixture(t *testing.T, ignore bool) (RuntimeOptions, <-chan int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pids := make(chan int, 8)
	env := []string{"INFERCAT_AGENT_FIXTURE=1"}
	if ignore {
		env = append(env, "INFERCAT_AGENT_IGNORE_TERM=1")
	}
	return RuntimeOptions{Command: []string{binary, "-test.run=^TestHarnessFixture$"}, Dir: t.TempDir(), Env: env, Frame: func(raw json.RawMessage) {
		var v struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(raw, &v) == nil && v.PID != 0 {
			pids <- v.PID
		}
	}}, pids
}
func waitRuntime(t *testing.T, r *Runtime, state string) RuntimeStatus {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		s := r.Status()
		if s.State == state {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("runtime did not become %s: %+v", state, r.Status())
	return RuntimeStatus{}
}
func TestRuntimeReadinessRestartAndShutdown(t *testing.T) {
	options, pids := runtimeFixture(t, false)
	r := StartRuntime(context.Background(), options)
	defer r.Close()
	first := waitRuntime(t, r, "healthy")
	<-pids
	if err := r.Send(json.RawMessage(`{"echo":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(first.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		s := r.Status()
		if s.State == "healthy" && s.PID != first.PID && s.Restarts == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	second := r.Status()
	if second.State != "healthy" || second.PID == first.PID {
		t.Fatalf("no restart: %+v", second)
	}
	// A running process that loses health is restarted as well.
	if err := r.Send(json.RawMessage(`{"unhealthy":true}`)); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		s := r.Status()
		if s.State == "healthy" && s.PID != second.PID {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s := r.Status(); s.State != "healthy" || s.PID == second.PID {
		t.Fatalf("unhealthy process retained: %+v", s)
	}
	third := r.Status()
	if err := r.Send(json.RawMessage(`{"close_output":true}`)); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		s := r.Status()
		if s.State == "healthy" && s.PID != third.PID {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s := r.Status(); s.State != "healthy" || s.PID == third.PID {
		t.Fatalf("closed protocol stream retained: %+v", s)
	}
	r.Close()
	if r.Status().State != "stopped" {
		t.Fatal("runtime not stopped")
	}
}
func TestRuntimeBusyPortDoesNotTouchListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	options, _ := runtimeFixture(t, false)
	options.Address = listener.Addr().String()
	start := time.Now()
	r := StartRuntime(context.Background(), options)
	defer r.Close()
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("startup blocked caller")
	}
	s := waitRuntime(t, r, "failed")
	if s.PID != 0 {
		t.Fatal("started despite occupied port")
	}
	connection, err := net.DialTimeout("tcp", options.Address, time.Second)
	if err != nil {
		t.Fatal("original listener changed", err)
	}
	connection.Close()
}
func TestRuntimeEscalatesTermIgnoringChild(t *testing.T) {
	options, pids := runtimeFixture(t, true)
	r := StartRuntime(context.Background(), options)
	defer r.Close()
	waitRuntime(t, r, "healthy")
	pid := <-pids
	start := time.Now()
	r.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cleanup took %v", elapsed)
	}
	if processLive(pid) {
		t.Fatal("TERM-ignoring child survived")
	}
}
func TestParentLifetimeFixture(t *testing.T) {
	dir := os.Getenv("INFERCAT_AGENT_PARENT_FIXTURE")
	if dir == "" {
		return
	}
	binary, _ := os.Executable()
	pids := make(chan int, 1)
	r := StartRuntime(context.Background(), RuntimeOptions{Command: []string{binary, "-test.run=^TestHarnessFixture$"}, Dir: dir, Env: []string{"INFERCAT_AGENT_FIXTURE=1"}, Frame: func(raw json.RawMessage) {
		var p struct {
			PID int `json:"pid"`
		}
		json.Unmarshal(raw, &p)
		if p.PID != 0 {
			pids <- p.PID
		}
	}})
	s := waitRuntime(t, r, "healthy")
	child := <-pids
	raw, _ := json.Marshal([]int{s.PID, child})
	os.WriteFile(filepath.Join(dir, "pids.json"), raw, 0600)
	select {}
}
func TestGuardianCleansAfterHostSIGKILL(t *testing.T) {
	dir := t.TempDir()
	binary, _ := os.Executable()
	parent := exec.Command(binary, "-test.run=^TestParentLifetimeFixture$")
	parent.Env = []string{"INFERCAT_AGENT_PARENT_FIXTURE=" + dir}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer parent.Process.Kill()
	var pids []int
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		raw, err := os.ReadFile(filepath.Join(dir, "pids.json"))
		if err == nil && json.Unmarshal(raw, &pids) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pids) != 2 {
		t.Fatal("parent fixture did not start")
	}
	start := time.Now()
	parent.Process.Kill()
	parent.Wait()
	until = time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if !processLive(pids[0]) && !processLive(pids[1]) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	t.Fatalf("owned process survived host death after %v", time.Since(start))
}
func processLive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// A reparented zombie is dead; it cannot retain a port or run code.
	out, _ := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "stat=").Output()
	return len(out) > 0 && out[0] != 'Z'
}

func TestStopGenerationCannotStopReplacement(t *testing.T) {
	options, _ := runtimeFixture(t, false)
	r := StartRuntime(context.Background(), options)
	defer r.Close()
	waitRuntime(t, r, "healthy")
	old := r.Generation()
	if err := r.SendGeneration(0, json.RawMessage(`{}`)); err == nil {
		t.Fatal("zero generation bypassed ownership")
	}
	if err := r.StopGeneration(old); err != nil {
		t.Fatal(err)
	}
	waitRuntime(t, r, "healthy")
	replacement := r.Generation()
	if old == replacement {
		t.Fatal("generation did not advance")
	}
	if err := r.StopGeneration(old); err != nil {
		t.Fatal("stale generation should already be stopped", err)
	}
	if err := r.StopGeneration(0); err != nil {
		t.Fatal("zero generation has nothing to stop", err)
	}
	if err := r.SendGeneration(old, json.RawMessage(`{}`)); err == nil {
		t.Fatal("old run dispatched into replacement")
	}
	if r.Generation() != replacement || r.Status().State != "healthy" {
		t.Fatal("old stop killed replacement")
	}
}

func Test157V5RestartedChildDoesNotQuarantineOtherKeys(t *testing.T) {
	options, _ := runtimeFixture(t, false)
	runtime := StartRuntime(context.Background(), options)
	defer runtime.Close()
	waitRuntime(t, runtime, "healthy")
	owned := runtime.Generation()
	store, err := runstate.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runstate.New(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); manager.Close() }()
	var calls atomic.Int32
	if err := manager.Register("test", manager.Consumer(func(context.Context, *runstate.Work) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return json.RawMessage(`{}`), nil
	}), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error {
		err := runtime.StopGeneration(owned)
		once.Do(func() { close(release) })
		return err
	}}); err != nil {
		t.Fatal(err)
	}
	first, err := manager.Submit("first", "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	// Restart the child while its owned run has not joined. The hook will see the old generation.
	if err := runtime.StopGeneration(owned); err != nil {
		t.Fatal(err)
	}
	waitRuntime(t, runtime, "healthy")
	replacement := runtime.Generation()
	if replacement == owned {
		t.Fatal("child did not restart")
	}
	next, err := manager.Submit("friend", "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Cancel(first.KeyID, first.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(50 * time.Second)
	for {
		row, err := store.Get(next.KeyID, next.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.State == runstate.Failed {
			t.Fatal("friend was quarantined", row.Reason)
		}
		if row.State == runstate.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("friend did not resume", row.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if runtime.Generation() != replacement || runtime.Status().State != "healthy" {
		t.Fatal("replacement stopped")
	}
	if _, err := manager.Submit("third", "test", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal("new work refused", err)
	}
	t.Log("stale owned generation stop: successor completed, new key admitted, replacement healthy")
}

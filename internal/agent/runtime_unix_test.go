//go:build darwin || linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "_agent-guardian" {
		os.Exit(Guardian(os.Args[2:]))
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

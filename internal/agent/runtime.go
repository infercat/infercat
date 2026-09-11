package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/infercat/infercat/internal/supervise"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const MaxFrame = 2 << 20

type RuntimeStatus struct {
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"last_error,omitempty"`
}
type RuntimeOptions struct {
	Command []string
	Dir     string
	Env     []string
	Address string // Loopback; empty allocates a port without a release-to-bind race.
	Frame   func(json.RawMessage)
	Exited  func()
}
type Runtime struct {
	mu             sync.Mutex
	status         RuntimeStatus
	input          *os.File
	writeMu        sync.Mutex
	cancel         context.CancelFunc
	done           chan struct{}
	options        RuntimeOptions
	generation     uint64
	stopGeneration context.CancelFunc
	generationDone chan struct{}
}

func StartRuntime(ctx context.Context, options RuntimeOptions) *Runtime {
	child, cancel := context.WithCancel(ctx)
	r := &Runtime{status: RuntimeStatus{State: "starting"}, cancel: cancel, done: make(chan struct{}), options: options}
	go func() { defer close(r.done); defer cancel(); r.loop(child) }()
	return r
}
func (r *Runtime) Status() RuntimeStatus { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *Runtime) set(state, message string, pid int) {
	r.mu.Lock()
	r.status.State = state
	r.status.LastError = message
	r.status.PID = pid
	r.mu.Unlock()
}
func (r *Runtime) Close()                         { r.cancel(); <-r.done }
func (r *Runtime) Send(raw json.RawMessage) error { return r.send(0, raw) }
func (r *Runtime) SendGeneration(generation uint64, raw json.RawMessage) error {
	if generation == 0 {
		return errors.New("agent runtime generation is not established")
	}
	return r.send(generation, raw)
}
func (r *Runtime) send(generation uint64, raw json.RawMessage) error {
	if len(raw) > MaxFrame || !json.Valid(raw) {
		return errors.New("invalid agent protocol frame")
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	r.mu.Lock()
	if generation != 0 && generation != r.generation {
		r.mu.Unlock()
		return errors.New("agent runtime generation changed")
	}
	in := r.input
	r.mu.Unlock()
	if in == nil {
		return errors.New("agent runtime unavailable")
	}
	if err := in.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	_, err := in.Write(append(append([]byte(nil), raw...), '\n'))
	return err
}
func (r *Runtime) loop(ctx context.Context) {
	if !Supported() {
		r.set("failed", "agent runtime is supported on Darwin and Linux only", 0)
		return
	}
	if len(r.options.Command) == 0 {
		r.set("failed", "agent runtime command missing", 0)
		return
	}
	delay := 500 * time.Millisecond
	for ctx.Err() == nil {
		err := r.runGeneration(ctx)
		if r.options.Exited != nil {
			r.options.Exited()
		}
		if ctx.Err() != nil {
			break
		}
		message := "agent runtime exited"
		if errors.Is(err, errPort) {
			r.set("failed", "agent health port unavailable", 0)
			<-ctx.Done()
			break
		}
		r.set("backoff", message, 0)
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		if ctx.Err() != nil {
			break
		}
		delay = min(2*delay, 4*time.Second)
		r.mu.Lock()
		r.status.Restarts++
		r.mu.Unlock()
	}
	r.set("stopped", "", 0)
}

// Generation identifies the supervised child owned by a run, including restarts.
func (r *Runtime) Generation() uint64 { r.mu.Lock(); defer r.mu.Unlock(); return r.generation }
func (r *Runtime) StopGeneration(generation uint64) error {
	r.mu.Lock()
	if generation == 0 || r.generation != generation {
		r.mu.Unlock()
		return nil // The owned generation is already gone; never stop its replacement.
	}
	if r.stopGeneration == nil {
		r.mu.Unlock()
		return nil
	}
	stop, done := r.stopGeneration, r.generationDone
	r.mu.Unlock()
	stop()
	<-done
	return nil
}
func (r *Runtime) runGeneration(ctx context.Context) error {
	child, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	r.mu.Lock()
	r.generation++
	r.stopGeneration = stop
	r.generationDone = done
	r.mu.Unlock()
	defer func() {
		stop()
		r.mu.Lock()
		r.stopGeneration = nil
		r.status.State = "backoff"
		close(done)
		r.mu.Unlock()
	}()
	return r.once(child)
}

var errPort = errors.New("health port unavailable")

func (r *Runtime) once(ctx context.Context) error {
	o := r.options
	address := o.Address
	if address == "" {
		address = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errPort
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return errPort
	}
	defer listener.Close()
	portFile, err := listener.(*net.TCPListener).File()
	if err != nil {
		return err
	}
	defer portFile.Close()
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer lifeRead.Close()
	defer lifeWrite.Close()
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer inputRead.Close()
	defer inputWrite.Close()
	if err = os.MkdirAll(o.Dir, 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(o.Dir, "runtime.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, append([]string{"_agent-guardian"}, o.Command...)...)
	configureGroup(cmd)
	cmd.ExtraFiles = []*os.File{lifeRead, portFile}
	cmd.Stdin = inputRead
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	cmd.Stderr = supervise.NewLog(logFile)
	cmd.WaitDelay = time.Second
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	lifeRead.Close()
	inputRead.Close()
	listener.Close()
	r.mu.Lock()
	r.input = inputWrite
	r.mu.Unlock()
	r.set("starting", "", cmd.Process.Pid)
	defer func() { r.mu.Lock(); r.input = nil; r.mu.Unlock(); inputWrite.Close(); killGroup(cmd.Process.Pid) }()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer lifeWrite.Close() // A closed or malformed protocol stream ends this runtime generation.
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), MaxFrame)
		for scanner.Scan() {
			raw := append(json.RawMessage(nil), scanner.Bytes()...)
			if !json.Valid(raw) {
				lifeWrite.Close()
				return
			}
			if o.Frame != nil {
				o.Frame(raw)
			}
		}
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	stop := func() {
		lifeWrite.Close()
		select {
		case <-waited:
		case <-time.After(3 * time.Second):
			killGroup(cmd.Process.Pid)
			<-waited
		}
		<-readDone
	}
	client := &http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	endpoint := ""
	// Resolve the bound address from the inherited descriptor, not a probe-and-release port.
	if addr, err := boundAddress(portFile); err == nil {
		endpoint = "http://" + addr + "/health"
	} else {
		stop()
		return err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	started := time.Now()
	failures := 0
	for {
		select {
		case err := <-waited:
			<-readDone
			return err
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-ticker.C:
			response, err := client.Get(endpoint)
			healthy := false
			if err == nil {
				raw, e := io.ReadAll(io.LimitReader(response.Body, 1025))
				response.Body.Close()
				healthy = e == nil && response.StatusCode == 200 && string(raw) == "ready"
			}
			if healthy {
				failures = 0
				r.set("healthy", "", cmd.Process.Pid)
			} else {
				failures++
			}
			if (!healthy && time.Since(started) > 10*time.Second && r.Status().State != "healthy") || (r.Status().State == "healthy" && failures >= 3) {
				stop()
				return errors.New("agent health failed")
			}
		}
	}
}
func boundAddress(f *os.File) (string, error) {
	l, err := net.FileListener(f)
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

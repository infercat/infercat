// Package supervise owns child lifetime and bounded logs, never model policy.
package supervise

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type Process struct {
	cmd       *exec.Cmd
	life, log *os.File
	done      chan error
	once      sync.Once
}

// Start checks the fixed port before spawning. Engines that cannot inherit a socket
// still own their bind; their bounded health check must succeed before use.
func Start(command, env []string, dir, address string) (*Process, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("managed engines require Darwin or Linux")
	}
	if len(command) == 0 || !filepath.IsAbs(command[0]) {
		return nil, fmt.Errorf("absolute engine command required")
	}
	l, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, fmt.Errorf("engine port unavailable: %w", err)
	}
	l.Close()
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if st, e := os.Lstat(filepath.Join(dir, "engine.log")); e == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("engine log must be a regular file")
	}
	log, err := os.OpenFile(filepath.Join(dir, "engine.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		rd.Close()
		wr.Close()
		log.Close()
		return nil, err
	}
	cmd := exec.Command(binary, append([]string{"_profile-guardian"}, command...)...)
	ConfigureGroup(cmd)
	cmd.ExtraFiles = []*os.File{rd}
	cmd.Dir = dir
	cmd.Env = env
	output := NewLog(log)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		rd.Close()
		wr.Close()
		log.Close()
		return nil, err
	}
	rd.Close()
	p := &Process{cmd: cmd, life: wr, log: log, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	return p, nil
}
func (p *Process) Close() {
	p.once.Do(func() {
		p.life.Close()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			KillGroup(p.cmd.Process.Pid)
			<-p.done
		}
		p.log.Close()
	})
}

// WaitHealth retries only readiness; it never sends a generation twice.
func (p *Process) WaitHealth(ctx context.Context, probe func(context.Context) error) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-p.done:
			p.done <- err
			return fmt.Errorf("engine exited before ready: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := probe(ctx); err == nil {
				return nil
			}
		}
	}
}

// Exited is called under the owning member's lock, never concurrently with Close.
func (p *Process) Exited() bool {
	select {
	case err := <-p.done:
		p.done <- err
		return true
	default:
		return false
	}
}

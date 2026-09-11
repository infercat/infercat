package agent

import (
	"context"
	"encoding/json"
	"errors"
	runstate "github.com/infercat/infercat/internal/run"
	"log"
	"os"
	"time"
)

func (a *Adapter) currentRuntime() *Runtime { a.mu.Lock(); defer a.mu.Unlock(); return a.runtime }
func (a *Adapter) Status() RuntimeStatus {
	a.mu.Lock()
	r, status := a.runtime, a.status
	a.mu.Unlock()
	if r != nil && status.State != "failed" {
		return r.Status()
	}
	return status
}
func (a *Adapter) Close() {
	a.mu.Lock()
	a.closed = true
	r, cancel := a.runtime, a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if r != nil {
		r.Close()
	}
	a.workers.Wait()
	a.mu.Lock()
	a.status = RuntimeStatus{State: "stopped"}
	a.mu.Unlock()
}
func (a *Adapter) availability(ctx context.Context) error {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return runstate.ErrAgentUnavailable
	}
	err := sandboxPreflight(ctx, a.dir)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	a.mu.Lock()
	if err != nil {
		a.status = RuntimeStatus{State: "failed", LastError: err.Error()}
	} else if a.runtime == nil {
		a.status = RuntimeStatus{State: "healthy"}
	}
	a.mu.Unlock()
	if err != nil {
		log.Printf("agent unavailable: %v", err)
		return runstate.ErrAgentUnavailable
	}
	return nil
}
func (a *Adapter) runtimeForRun(ctx context.Context, key string) (*Runtime, string, func(error), error) {
	if !a.perRun {
		workspace, err := workspaceFor(a.dir, key)
		return a.currentRuntime(), workspace, func(error) {}, err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, "", func(error) {}, runstate.ErrAgentUnavailable
	}
	a.workers.Add(1)
	a.mu.Unlock()
	workspace, err := sandboxWorkspace(a.dir)
	if err != nil {
		a.workers.Done()
		a.mu.Lock()
		a.status = RuntimeStatus{State: "failed", LastError: "agent workspace unavailable"}
		a.mu.Unlock()
		return nil, "", func(error) {}, runstate.Failure("workspace_unavailable")
	}
	var child *Runtime
	cleanup := func(runErr error) {
		if child != nil {
			child.Close()
		}
		removeErr := os.RemoveAll(workspace) // Only after child join and captured outputs.
		a.mu.Lock()
		if a.runtime == child {
			a.runtime = nil
		}
		a.status = RuntimeStatus{State: "healthy"}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			a.status = RuntimeStatus{State: "failed", LastError: runErr.Error()}
		}
		if removeErr != nil {
			a.status = RuntimeStatus{State: "failed", LastError: "agent workspace cleanup failed"}
			log.Printf("agent workspace cleanup: %v", removeErr)
		}
		a.mu.Unlock()
		a.workers.Done()
	}
	options, err := sandboxOptions(a.dir, workspace)
	if err != nil {
		return nil, workspace, cleanup, runstate.Failure("workspace_unavailable")
	}
	options.Frame = a.onFrame
	options.Exited = a.onExit
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, workspace, cleanup, context.Canceled
	}
	a.status = RuntimeStatus{State: "starting"}
	child = StartRuntime(a.ctx, options)
	a.runtime = child
	a.mu.Unlock()
	return child, workspace, cleanup, nil
}
func (a *Adapter) onFrame(raw json.RawMessage) {
	var f frame
	if json.Unmarshal(raw, &f) != nil {
		return
	}
	a.mu.Lock()
	s := a.live[f.RunID]
	a.mu.Unlock()
	if s == nil {
		return
	}
	if s.bytes.Add(int64(len(raw))) > MaxFrame {
		s.once.Do(func() { close(s.lost) })
		return
	}
	select {
	case s.frames <- raw:
	default:
		s.once.Do(func() { close(s.lost) })
	}
}
func (a *Adapter) onExit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.live {
		s.once.Do(func() { close(s.lost) })
	}
}
func waitChild(ctx context.Context, r *Runtime) error {
	if r == nil {
		return runstate.Failure("runtime_lost")
	}
	for {
		switch r.Status().State {
		case "healthy":
			return nil
		case "failed", "stopped":
			return runstate.Failure("runtime_lost")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

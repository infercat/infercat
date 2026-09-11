//go:build darwin || linux

package supervise

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func ConfigureGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func KillGroup(pid int)            { _ = syscall.Kill(-pid, syscall.SIGKILL) }

// Guardian owns a process group while the host alone owns the lifetime pipe's writer.
// Cleanup is bounded after host death; descendants escaping the group are not contained here.
func Guardian(argv []string, agent bool) int {
	if len(argv) == 0 {
		return 2
	}
	life := os.NewFile(3, "host-lifetime")
	var health *os.File
	if agent {
		health = os.NewFile(4, "agent-health")
	}
	if info, err := life.Stat(); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return 2
	}
	syscall.CloseOnExec(3)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	signal.Ignore(syscall.SIGPIPE) // A dead host's log pipe must not kill cleanup.
	dead := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, life); close(dead) }()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if agent {
		cmd.ExtraFiles = []*os.File{health}
	} // Child fd 3 is health, never the lifetime pipe.
	if err := cmd.Start(); err != nil {
		return 1
	}
	if health != nil {
		health.Close()
	}
	// Only the child owns the protocol descriptors after exec. Retaining the
	// guardian's copies would hide a closed child stream from the supervisor.
	os.Stdin.Close()
	os.Stdout.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
		KillGroup(os.Getpid())
		return 1
	case <-dead:
	case <-signals:
	}
	_ = syscall.Kill(-os.Getpid(), syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
	}
	KillGroup(os.Getpid())
	return 1
}

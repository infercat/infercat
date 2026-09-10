//go:build !windows

package agentconfig

import (
	"os"
	"syscall"
)

func lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }

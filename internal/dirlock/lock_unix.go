//go:build !windows

package dirlock

import (
	"golang.org/x/sys/unix"
	"os"
)

// Lock holds an exclusive nonblocking lock until the file closes or the process exits.
func Lock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }

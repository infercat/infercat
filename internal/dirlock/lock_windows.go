package dirlock

import (
	"golang.org/x/sys/windows"
	"os"
)

// Lock holds an exclusive nonblocking lock until the file closes or the process exits.
func Lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

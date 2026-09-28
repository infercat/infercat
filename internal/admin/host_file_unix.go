//go:build !windows

package admin

import (
	"os"
	"syscall"
)

func openHostFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrHostFile
	}
	f := os.NewFile(uintptr(fd), "admin-code")
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		f.Close()
		return nil, ErrHostFile
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		f.Close()
		return nil, ErrHostFile
	}
	return f, nil
}

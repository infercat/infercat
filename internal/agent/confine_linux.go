//go:build linux

package agent

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"
)

func sandboxTrees() []string {
	return []string{"/usr", "/lib", "/lib64", "/lib32", "/bin", "/sbin", "/etc", "/opt"}
}
func sandboxPath() string { return ":/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" }
func sandboxCommand(workspace, installed string, command []string) ([]string, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return append([]string{binary, "_confine", workspace, installed, "--"}, command...), nil
}

// Confine never returns to ordinary command dispatch and never falls back to exec.
func Confine(args []string) int {
	if len(args) < 4 || args[2] != "--" {
		return 2
	}
	if err := landlockExec(args[0], args[1], args[3:]); err != nil {
		fmt.Fprintln(os.Stderr, "agent confinement unavailable:", err)
	}
	return 1
}
func landlockExec(workspace, installed string, command []string) error {
	runtime.LockOSThread() // Keep no_new_privs, restriction and exec on this thread.
	// Do not unlock a restricted thread; the launcher exits if exec fails.
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || abi < 3 {
		return errors.New("Landlock ABI 3 or newer is required")
	}
	handled := uint64((1 << 15) - 1) // All ABI1 rights, REFER (2), TRUNCATE (3).
	if abi >= 5 {
		handled |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&handled)), unsafe.Sizeof(handled), 0)
	if errno != 0 {
		return errno
	}
	defer unix.Close(int(fd))
	read := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	add := func(path string, access uint64, optional bool) error {
		opened, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if optional && errors.Is(err, unix.ENOENT) {
			return nil
		}
		if err != nil {
			return err
		}
		defer unix.Close(opened)
		var st unix.Stat_t
		if err = unix.Fstat(opened, &st); err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			access &= uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV)
		}
		attr := struct {
			Access uint64
			Parent int32
		}{access & handled, int32(opened)}
		_, _, e := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
		if e != 0 {
			return e
		}
		return nil
	}
	for _, path := range sandboxTrees() {
		if err := add(path, read|unix.LANDLOCK_ACCESS_FS_EXECUTE, true); err != nil {
			return err
		}
	}
	if err := add(installed, read|unix.LANDLOCK_ACCESS_FS_EXECUTE, false); err != nil {
		return err
	}
	writable := handled &^ uint64(unix.LANDLOCK_ACCESS_FS_MAKE_CHAR|unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK)
	if err := add(workspace, writable, false); err != nil {
		return err
	}
	if err := add(filepath.Join(workspace, "tmp"), writable, false); err != nil {
		return err
	}
	if err := add("/proc", read, false); err != nil {
		return err
	}
	for _, path := range []string{"/dev/zero", "/dev/random", "/dev/urandom"} {
		if err := add(path, read, false); err != nil {
			return err
		}
	}
	for _, path := range []string{"/dev/null", "/dev/tty"} {
		if err := add(path, read|unix.LANDLOCK_ACCESS_FS_WRITE_FILE|unix.LANDLOCK_ACCESS_FS_IOCTL_DEV, path == "/dev/tty"); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0)
	if errno != 0 {
		return errno
	}
	return unix.Exec(command[0], command, os.Environ())
}

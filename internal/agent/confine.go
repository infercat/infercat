package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
func beneath(path, root string) bool {
	return root == string(os.PathSeparator) || path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}
func sandboxLayout(data, runtime string) error {
	if beneath(data, runtime) {
		return errors.New("agent runtime may not contain the data directory")
	}
	for _, path := range []string{data, runtime} {
		for _, root := range sandboxTrees() {
			if canonical, err := filepath.EvalSymlinks(root); err == nil && beneath(path, canonical) {
				return fmt.Errorf("agent data/runtime overlaps readable tool tree %s", root)
			}
		}
	}
	return nil
}
func sandboxWorkspace(data string) (string, error) {
	root := filepath.Join(data, "agent", "workspaces")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	workspace, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return "", err
	}
	canonical, err := canonicalPath(workspace)
	if err == nil {
		err = os.Mkdir(filepath.Join(canonical, "tmp"), 0700)
	}
	if err != nil {
		_ = os.RemoveAll(workspace)
		return "", err
	}
	return canonical, nil
}
func sandboxOptions(data, workspace string) (RuntimeOptions, error) {
	canonical, err := canonicalPath(data)
	if err != nil {
		return RuntimeOptions{}, err
	}
	runtime, err := Installed(canonical)
	if err != nil {
		return RuntimeOptions{}, err
	}
	runtime, err = canonicalPath(runtime)
	if err != nil {
		return RuntimeOptions{}, err
	}
	if err = sandboxLayout(canonical, runtime); err != nil {
		return RuntimeOptions{}, err
	}
	options, err := harnessOptions(canonical, filepath.Join(workspace, ".runtime"))
	if err != nil {
		return options, err
	}
	for i, value := range options.Env {
		if strings.HasPrefix(value, "HOME=") {
			options.Env[i] = "HOME=" + workspace
		}
		if strings.HasPrefix(value, "PATH=") {
			options.Env[i] = "PATH=" + filepath.Join(runtime, "node/bin") + sandboxPath()
		}
	}
	options.Env = append(options.Env, "TMPDIR="+filepath.Join(workspace, "tmp"))
	options.Command, err = sandboxCommand(workspace, runtime, options.Command)
	options.SingleGeneration = true
	return options, err
}
func sandboxPreflight(ctx context.Context, data string) error {
	workspace, err := sandboxWorkspace(data)
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	options, err := sandboxOptions(data, workspace)
	if err != nil {
		return err
	}
	// Substitute only the payload; the same launcher/profile applies to the harness.
	runtime, err := Installed(data)
	if err != nil {
		return err
	}
	runtime, err = canonicalPath(runtime)
	if err != nil {
		return err
	}
	command, err := sandboxCommand(workspace, runtime, []string{"/usr/bin/true"})
	if err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, command[0], command[1:]...)
	cmd.Dir = options.Dir
	cmd.Env = options.Env
	if err = cmd.Run(); err != nil {
		return errors.New("agent sandbox could not be applied (Linux requires Landlock ABI 3 or newer)")
	}
	return nil
}

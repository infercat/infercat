//go:build darwin || linux

package agent

import (
	"github.com/infercat/infercat/internal/supervise"
	"os/exec"
)

func configureGroup(cmd *exec.Cmd) { supervise.ConfigureGroup(cmd) }
func killGroup(pid int)            { supervise.KillGroup(pid) }
func Guardian(argv []string) int   { return supervise.Guardian(argv, true) }

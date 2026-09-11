//go:build !darwin && !linux

package supervise

import "os/exec"

func ConfigureGroup(*exec.Cmd)    {}
func KillGroup(int)               {}
func Guardian([]string, bool) int { return 2 }

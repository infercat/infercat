//go:build !darwin && !linux

package agent

import "os/exec"

func configureGroup(*exec.Cmd) {}
func killGroup(int)            {}
func Guardian([]string) int    { return 2 }

//go:build !darwin && !linux

package agent

import "errors"

func sandboxTrees() []string { return nil }
func sandboxPath() string    { return "" }
func sandboxCommand(string, string, []string) ([]string, error) {
	return nil, errors.New("agent read confinement requires macOS or Linux")
}
func Confine([]string) int { return 2 }

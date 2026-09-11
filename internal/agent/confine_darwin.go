//go:build darwin

package agent

import _ "embed"

//go:embed assets/read-sandbox.sb
var seatbelt string

func sandboxTrees() []string {
	return []string{"/usr", "/bin", "/sbin", "/System", "/Library/Frameworks", "/Library/Apple", "/Library/Developer", "/private/etc", "/private/var/db/timezone", "/opt/homebrew", "/usr/local"}
}
func sandboxPath() string {
	return ":/opt/homebrew/bin:/usr/local/bin:/Library/Developer/CommandLineTools/usr/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}
func sandboxCommand(workspace, installed string, command []string) ([]string, error) {
	return append([]string{"/usr/bin/sandbox-exec", "-p", seatbelt, "-D", "RUNTIME=" + installed, "-D", "WORKSPACE=" + workspace, "-D", "TMPDIR=" + workspace + "/tmp"}, command...), nil
}
func Confine([]string) int { return 2 }

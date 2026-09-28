package machine

import (
	"github.com/infercat/infercat/internal/admin"
	"strings"
)

// Target selects only the transport. Resolving local data directories belongs to the local factory.
// Fields stay private to keep generic formatting from exposing a code.
type Target struct {
	code, file string
	remote     bool
}

func (t Target) Remote() bool { return t.remote }
func (t Target) Code() (string, error) {
	if t.file != "" {
		return admin.ReadAdminCode(t.file)
	}
	if _, _, err := admin.ParseAdminCode(t.code); err != nil {
		return "", err
	}
	return t.code, nil
}
func (t Target) GoString() string { return t.String() }
func (t Target) String() string {
	if t.remote {
		return "remote"
	}
	return "local"
}

// ParseTarget removes selectors before the literal delimiter. Value flags are supplied by the
// command's parser so a value spelled --host is not mistaken for another selector.
func ParseTarget(args []string, valueFlags map[string]bool) (rest []string, target Target, err error) {
	invalid := func() {
		err = &Failure{Code: "invalid_arguments", Message: "use exactly one of --host CODE or --host-file PATH", Exit: 2}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, value, has := strings.Cut(a, "=")
		if name != "--host" && name != "--host-file" {
			rest = append(rest, a)
			if !has && valueFlags[name] && i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
			continue
		}
		if target.remote {
			invalid()
		}
		target.remote = true
		if !has {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				invalid()
				continue
			}
			i++
			value = args[i]
		}
		if value == "" {
			invalid()
		}
		if name == "--host" {
			target.code = value
		} else {
			target.file = value
		}
	}
	return
}

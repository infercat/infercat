package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CommandLine(im InstalledMember) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	parts := []string{"cd", quote(im.Directory), "&&", "env"}
	for _, s := range append(append([]string{}, im.Env...), im.Command...) {
		parts = append(parts, quote(s))
	}
	return strings.Join(parts, " ")
}

// Only this reserved directory's real child trees may retire, after config commit.
func RetireTrees(dir string, in Installation) error {
	root := filepath.Join(dir, "profiles", "trees")
	st, e := os.Lstat(root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("profile tree root is not a real directory")
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	keep := map[string]bool{}
	retain := func(path string) {
		rel, e := filepath.Rel(root, path)
		if e == nil && filepath.IsLocal(rel) {
			keep[strings.Split(rel, string(os.PathSeparator))[0]] = true
		}
	}
	for _, path := range in.Artifacts {
		retain(path)
	}
	for _, m := range in.Members {
		for _, path := range m.Paths {
			retain(path)
		}
	}
	for path := range in.Files {
		retain(path)
	}
	for path := range in.Links {
		retain(path)
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() && !keep[entry.Name()] {
			if e = os.RemoveAll(filepath.Join(root, entry.Name())); e != nil {
				return e
			}
		}
	}
	return nil
}

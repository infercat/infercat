package agentconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSymlinkedAncestors(t *testing.T) {
	for _, shape := range []string{"tmp", "home", "dotfiles"} {
		t.Run(shape, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cfgRoot, dshRoot := filepath.Join(root, "config"), filepath.Join(root, "dsh")
			if shape == "tmp" && runtime.GOOS == "darwin" {
				p, err := os.MkdirTemp("/tmp", "infercat-147-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(p) })
				cfgRoot, dshRoot = filepath.Join(p, "config"), filepath.Join(p, "dsh")
			} else {
				real := filepath.Join(root, "real")
				if err := os.MkdirAll(real, 0700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "linked-home")
				if err := os.Symlink(real, link); err != nil {
					t.Fatal(err)
				}
				cfgRoot, dshRoot = filepath.Join(link, ".config"), filepath.Join(link, ".dsh")
				if shape == "dotfiles" {
					for _, dir := range []string{".config", ".dsh"} {
						dest := filepath.Join(real, dir)
						os.MkdirAll(dest, 0700)
						if err := os.Symlink(dest, filepath.Join(root, dir)); err != nil {
							t.Fatal(err)
						}
					}
					cfgRoot, dshRoot = filepath.Join(root, ".config"), filepath.Join(root, ".dsh")
				}
			}
			c := Config{filepath.Join(cfgRoot, "infercat", "agents"), filepath.Join(dshRoot, "settings.yaml")}
			if err := os.MkdirAll(dshRoot, 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("# original\n")
			os.WriteFile(c.DSH, original, 0600)
			if _, err := c.Configure([]string{"opencode", "dsh"}, "owner", "http://127.0.0.1:14700/v1", models()); err != nil {
				t.Fatal(err)
			}
			r, ok, err := c.load("dsh")
			if err != nil || !ok {
				t.Fatal(r, ok, err)
			}
			resolved, err := filepath.EvalSymlinks(c.DSH)
			if err != nil || resolved != r.Target {
				t.Fatal("receipt not canonical", r.Target, resolved, err)
			}
			if err := c.Remove("owner"); err != nil {
				t.Fatal(err)
			}
			if string(mustRead(t, c.DSH)) != string(original) {
				t.Fatal("round trip changed bytes")
			}
		})
	}
}

func TestSymlinkedManagedTargetsRefused(t *testing.T) {
	for _, target := range []string{"extra", "receipt", "backup", "lock"} {
		t.Run(target, func(t *testing.T) {
			c := fixture(t, "# original\n")
			os.MkdirAll(c.Dir, 0700)
			victim := filepath.Join(filepath.Dir(c.Dir), "untouched")
			os.WriteFile(victim, []byte("keep"), 0600)
			name := map[string]string{"extra": "opencode.jsonc", "receipt": "dsh.json", "backup": "dsh-" + digest([]byte(c.DSH)) + ".original", "lock": "lock"}[target]
			if err := os.Symlink(victim, filepath.Join(c.Dir, name)); err != nil {
				t.Fatal(err)
			}
			selected := []string{"dsh"}
			if target == "extra" {
				selected = []string{"opencode"}
			}
			_, err := c.Configure(selected, "o", "u", models())
			if err == nil || !strings.Contains(err.Error(), "symlink refused") {
				t.Fatal(err)
			}
			if string(mustRead(t, victim)) != "keep" {
				t.Fatal("target overwritten")
			}
		})
	}
}

func TestOwnOpenCodePathStillWorks(t *testing.T) {
	c := fixture(t, "")
	t.Setenv("OPENCODE_CONFIG", filepath.Join(c.Dir, "opencode.jsonc"))
	lines, err := c.Configure([]string{"opencode"}, "o", "u", models())
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "OPENCODE_CONFIG=") {
		t.Fatal(lines)
	}
	if err := c.Remove("o"); err != nil {
		t.Fatal(err)
	}
}

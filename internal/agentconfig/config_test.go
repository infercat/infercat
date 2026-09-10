package agentconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixture(t *testing.T, original string) Config {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Config{filepath.Join(root, "receipts"), filepath.Join(root, "dsh", "settings.yaml")}
	if err := os.MkdirAll(filepath.Dir(c.DSH), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.DSH, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	return c
}
func models() Models { return Models{IDs: []string{"m1", "m\"2"}, Context: 32768, Output: 2048} }
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestManagedLifecycle(t *testing.T) {
	original := "# person\nagent-default-model:\n  provider: retained\nllm-pi-ai:\n  providers:\n    retained: {}\n"
	c := fixture(t, original)
	lines, err := c.Configure(names, "owner", "http://127.0.0.1:14000/v1", models())
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "OPENCODE_CONFIG=") || !strings.Contains(lines[1], "/model") {
		t.Fatal(lines)
	}
	installed := mustRead(t, c.DSH)
	if !bytes.Contains(installed, []byte("provider: retained")) {
		t.Fatal("selection changed")
	}
	_, err = c.Configure(names, "owner", "http://127.0.0.1:14000/v1", models())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, c.DSH), installed) {
		t.Fatal("rerun changed bytes")
	}
	if list, err := c.List(); err != nil || strings.Join(list, ",") != "opencode,dsh" {
		t.Fatal(list, err)
	}
	if _, err = c.Configure(names, "other", "http://127.0.0.1:14001/v1", models()); err == nil {
		t.Fatal("foreign owner accepted")
	}
	if err = c.Remove("other"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, c.DSH), installed) {
		t.Fatal("foreign exit removed block")
	}
	edited := append(installed, []byte("\n# person added this after configure\n")...)
	os.WriteFile(c.DSH, edited, 0640)
	if err = c.Remove("owner"); err != nil {
		t.Fatal(err)
	}
	want := original + "\n# person added this after configure\n"
	if string(mustRead(t, c.DSH)) != want {
		t.Fatal("foreign edit lost")
	}
	if err = c.Remove(""); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(c.Dir, "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatal("created extra file remained")
	}
	if string(mustRead(t, filepath.Join(c.Dir, "dsh-"+digest([]byte(c.DSH))+".original"))) != original {
		t.Fatal("backup not original")
	}
	if fi, _ := os.Stat(c.DSH); fi.Mode().Perm() != 0640 {
		t.Fatal("mode changed")
	}
	if _, err = c.Configure(names, "new", "http://127.0.0.1:14002/v1", models()); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(c.Dir, "dsh-"+digest([]byte(c.DSH))+".original"))) != original {
		t.Fatal("first backup overwritten")
	}
}
func TestEditedOwnedBlockRefused(t *testing.T) {
	for _, edit := range []string{"inside", "markers", "duplicate"} {
		t.Run(edit, func(t *testing.T) {
			c := fixture(t, "llm-pi-ai:\n  providers:\n    retained: {}\n")
			if _, err := c.Configure([]string{"dsh"}, "o", "http://127.0.0.1:14000/v1", models()); err != nil {
				t.Fatal(err)
			}
			b := mustRead(t, c.DSH)
			switch edit {
			case "inside":
				b = bytes.ReplaceAll(b, []byte("14000"), []byte("15000"))
			case "markers":
				b = bytes.ReplaceAll(b, []byte("infercat managed provider"), []byte("changed marker"))
			case "duplicate":
				r, _, _ := c.load("dsh")
				b = append(b, []byte(r.Block)...)
			}
			os.WriteFile(c.DSH, b, 0640)
			if err := c.Remove(""); err == nil {
				t.Fatal("edited owned bytes removed")
			}
			if !bytes.Equal(b, mustRead(t, c.DSH)) {
				t.Fatal("mutated on refusal")
			}
			if _, ok, _ := c.load("dsh"); !ok {
				t.Fatal("lost ownership receipt")
			}
		})
	}
}
func TestCreatedParentsProtectForeignChildren(t *testing.T) {
	c := fixture(t, "")
	if _, err := c.Configure([]string{"dsh"}, "o", "u", models()); err != nil {
		t.Fatal(err)
	}
	b := append(mustRead(t, c.DSH), []byte("    person: {}\n")...)
	os.WriteFile(c.DSH, b, 0600)
	if err := c.Remove(""); err == nil {
		t.Fatal("removed parent of foreign entry")
	}
	if !bytes.Equal(b, mustRead(t, c.DSH)) {
		t.Fatal("mutated dependent foreign entry")
	}
}
func TestPendingAndCompletedCleanupRecovery(t *testing.T) {
	for _, stage := range []string{"pending", "already removed"} {
		t.Run(stage, func(t *testing.T) {
			original := "# original\n"
			c := fixture(t, original)
			if _, err := c.Configure([]string{"dsh"}, "o", "u", models()); err != nil {
				t.Fatal(err)
			}
			// Same durable shape after a crash before target replacement or after span removal.
			os.WriteFile(c.DSH, []byte(original), 0640)
			if err := c.Remove(""); err != nil {
				t.Fatal(err)
			}
			if string(mustRead(t, c.DSH)) != original {
				t.Fatal("pending original altered")
			}
		})
	}
}
func TestUnownedFilesAndSymlinks(t *testing.T) {
	c := fixture(t, "# original\n")
	os.MkdirAll(c.Dir, 0700)
	target := filepath.Join(c.Dir, "opencode.jsonc")
	os.WriteFile(target, []byte("{}\n"), 0600)
	if _, err := c.Configure([]string{"opencode"}, "o", "u", models()); err == nil {
		t.Fatal("unowned file overwritten")
	}
	os.Remove(target)
	os.Remove(c.DSH)
	if err := os.Symlink(target, c.DSH); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Configure([]string{"dsh"}, "o", "u", models()); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestReceiptCorruptionAndLock(t *testing.T) {
	c := fixture(t, "")
	if _, err := c.Configure([]string{"dsh"}, "o", "u", models()); err != nil {
		t.Fatal(err)
	}
	r, _, _ := c.load("dsh")
	r.Hash = "wrong"
	b, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(c.Dir, "dsh.json"), b, 0600)
	if err := c.Remove(""); err == nil {
		t.Fatal("bad checksum accepted")
	}
	err := c.locked(func() error {
		if _, err := c.List(); err == nil {
			t.Error("concurrent operation not refused")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestCodexAndUnknownAreAllOrNothing(t *testing.T) {
	for _, s := range []string{"opencode,codex", "dsh,bad", ""} {
		if a, err := Parse(s); err == nil || a != nil {
			t.Fatal(s, a, err)
		}
	}
	if _, err := Parse("codex"); err.Error() != "Codex needs the Responses API; not yet supported" {
		t.Fatal(err)
	}
	a, err := Parse("opencode,dsh,opencode")
	if err != nil || len(a) != 2 {
		t.Fatal(a, err)
	}
}

func TestDefaultExplicitRoots(t *testing.T) {
	if os.Getenv("AGENTCONFIG_CHILD") == "1" {
		c, err := Default()
		if err != nil || c.Dir != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "infercat", "agents") {
			t.Fatal(c, err)
		}
		return
	}
	if runtime.GOOS == "windows" {
		return
	} // XDG override is Unix-only; Windows uses UserConfigDir.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDefaultExplicitRoots$")
	cmd.Env = []string{"AGENTCONFIG_CHILD=1", "XDG_CONFIG_HOME=" + root, "DSH_HOME=" + filepath.Join(root, "dsh")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("explicit roots without inherited home: %v: %s", err, out)
	}
}

func TestBackupIsPerTarget(t *testing.T) {
	c := fixture(t, "# first target\n")
	if _, err := c.Configure([]string{"dsh"}, "a", "u", models()); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(""); err != nil {
		t.Fatal(err)
	}
	c.DSH = filepath.Join(filepath.Dir(c.DSH), "other", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(c.DSH), 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(c.DSH, []byte("# second target\n"), 0600)
	if _, err := c.Configure([]string{"dsh"}, "b", "u", models()); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(c.Dir, "dsh-"+digest([]byte(c.DSH))+".original"))) != "# second target\n" {
		t.Fatal("backup did not follow target")
	}
}

func TestOversizedGeneratedConfigNeverLeavesUnreadableState(t *testing.T) {
	c := fixture(t, "# unchanged\n")
	_, err := c.Configure([]string{"dsh"}, "o", "u", Models{IDs: []string{strings.Repeat("x", 2<<20)}})
	if err == nil {
		t.Fatal("oversized generated config accepted")
	}
	if err := c.Remove(""); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, c.DSH)) != "# unchanged\n" {
		t.Fatal("target changed on refusal")
	}
}

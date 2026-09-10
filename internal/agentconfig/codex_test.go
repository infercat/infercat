package agentconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func codexFixture(t *testing.T) (Config, string, []byte) {
	t.Helper()
	c := fixture(t, "# dsh original\n")
	home := filepath.Join(filepath.Dir(c.Dir), "codex")
	os.MkdirAll(home, 0700)
	t.Setenv("CODEX_HOME", home)
	base := []byte("# person\nweb_search = 'cached'\n[model_providers.retained]\nname = 'retained'\n")
	os.WriteFile(filepath.Join(home, "config.toml"), base, 0640)
	return c, filepath.Join(home, "infercat.config.toml"), base
}
func TestCodexProfileLifecycle(t *testing.T) {
	c, target, base := codexFixture(t)
	m := models()
	m.IDs = []string{"model\"\n\\quoted"}
	lines, err := c.Configure([]string{"codex"}, "owner", "http://127.0.0.1:14900/v1", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "codex --profile infercat" {
		t.Fatal(lines)
	}
	raw := mustRead(t, target)
	var cfg map[string]any
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != m.IDs[0] || cfg["web_search"] != "disabled" || cfg["model_provider"] != "infercat" || cfg["model_context_window"] != int64(m.Context) {
		t.Fatal(cfg)
	}
	provider := cfg["model_providers"].(map[string]any)["infercat"].(map[string]any)
	if provider["base_url"] != "http://127.0.0.1:14900/v1" || provider["wire_api"] != "responses" || provider["requires_openai_auth"] != false || len(provider) != 4 {
		t.Fatal(provider)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(filepath.Dir(target), "config.toml")), base) {
		t.Fatal("base config modified")
	}
	if _, err = c.Configure([]string{"codex"}, "owner", "http://127.0.0.1:14900/v1", m); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, mustRead(t, target)) {
		t.Fatal("rerun changed profile")
	}
	if a, err := c.List(); err != nil || strings.Join(a, ",") != "codex" {
		t.Fatal(a, err)
	}
	if err = c.Remove("other"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, mustRead(t, target)) {
		t.Fatal("other owner removed profile")
	}
	if err = c.Remove("owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("generated file remains")
	}
	if err = c.Remove(""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(filepath.Dir(target), "config.toml")), base) {
		t.Fatal("base changed on removal")
	}
}
func TestCodexPreflightRefusesUnownedOrLegacy(t *testing.T) {
	for _, shape := range []string{"unowned", "legacy-table", "legacy-selector", "bad-toml", "symlink"} {
		t.Run(shape, func(t *testing.T) {
			c, target, _ := codexFixture(t)
			base := filepath.Join(filepath.Dir(target), "config.toml")
			switch shape {
			case "unowned":
				os.WriteFile(target, []byte("# person's existing profile\n"), 0600)
			case "legacy-table":
				os.WriteFile(base, []byte("[profiles.infercat]\nmodel='mine'\n"), 0600)
			case "legacy-selector":
				os.WriteFile(base, []byte("profile='infercat'\n"), 0600)
			case "bad-toml":
				os.WriteFile(base, []byte("token='sensitive\n"), 0600)
			case "symlink":
				if err := os.Symlink(base, target); err != nil {
					t.Fatal(err)
				}
			}
			before := mustRead(t, base)
			_, err := c.Configure([]string{"dsh", "codex"}, "o", "u", models())
			if err == nil {
				t.Fatal("accepted invalid configuration")
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("error exposed file content")
			}
			if _, err = os.Stat(c.Dir); !os.IsNotExist(err) {
				t.Fatal("preflight wrote receipt or lock")
			}
			if !bytes.Equal(before, mustRead(t, base)) {
				t.Fatal("base changed")
			}
		})
	}
}
func TestCodexEditedProfileAndForeignComment(t *testing.T) {
	for _, change := range []string{"inside", "outside"} {
		t.Run(change, func(t *testing.T) {
			c, target, _ := codexFixture(t)
			if _, err := c.Configure([]string{"codex"}, "o", "u", models()); err != nil {
				t.Fatal(err)
			}
			b := mustRead(t, target)
			if change == "inside" {
				b = bytes.Replace(b, []byte("disabled"), []byte("cached"), 1)
			} else {
				b = append(b, []byte("# person's comment\n")...)
			}
			os.WriteFile(target, b, 0600)
			err := c.Remove("o")
			if change == "inside" {
				if err == nil || !bytes.Equal(b, mustRead(t, target)) {
					t.Fatal("edited bytes lost", err)
				}
			} else {
				if err != nil || string(mustRead(t, target)) != "# person's comment\n" {
					t.Fatal("outside bytes lost", err)
				}
			}
		})
	}
}

package agentconfig

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

func codexPath() (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".codex")
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("CODEX_HOME must be absolute")
	}
	return safe(filepath.Join(home, "infercat.config.toml"))
}

// Codex 0.154.0 loads a profile as a complete config layer, not [profiles.name].
// Validate without including any user config content in an error message.
func checkCodexBase(target string) error {
	raw, _, err := read(filepath.Join(filepath.Dir(target), "config.toml"))
	if err != nil {
		return err
	}
	var cfg map[string]any
	if _, err = toml.Decode(string(raw), &cfg); err != nil {
		return errors.New("Codex config.toml is invalid TOML; fix it before configuring")
	}
	profiles, _ := cfg["profiles"].(map[string]any)
	if cfg["profile"] == "infercat" || profiles["infercat"] != nil {
		return errors.New("Codex config.toml contains a legacy infercat profile; migrate it before configuring")
	}
	return nil
}

func codexBlock(url string, m Models) ([]byte, error) {
	cfg := map[string]any{
		"model_provider": "infercat", "model": m.IDs[0], "web_search": "disabled",
		"model_providers": map[string]any{"infercat": map[string]any{
			"name": "Infercat", "base_url": url, "wire_api": "responses", "requires_openai_auth": false,
		}},
	}
	if m.Context > 0 {
		cfg["model_context_window"] = m.Context
	}
	var b bytes.Buffer
	b.WriteString("# BEGIN infercat managed provider\n")
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return nil, err
	}
	b.WriteString("# END infercat managed provider\n")
	return b.Bytes(), nil
}

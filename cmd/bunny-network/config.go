package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// configName holds the `serve` settings so a later `serve` with no flags reuses them.
const configName = "config.json"

// config is written at mode 0600 because --upstream-key may be a real API key.
//
// Two `serve` flags are deliberately NOT persisted: --log-prompts, because a host who debugs
// once must not keep logging their friends' conversations forever (pm/BELIEFS.md Protection 3),
// and --ephemeral, because it changes the host address and is a per-run mode, not a setting.
type config struct {
	Upstream       string `json:"upstream,omitempty"`
	UpstreamKey    string `json:"upstream_key,omitempty"`
	Slots          int    `json:"slots,omitempty"`
	QueueTimeout   string `json:"queue_timeout,omitempty"`
	RequestTimeout string `json:"request_timeout,omitempty"`
	MaxBody        int64  `json:"max_body,omitempty"`
	DevListen      string `json:"dev_listen,omitempty"`
	DERPMapURL     string `json:"derpmap_url,omitempty"`
	Region         string `json:"region,omitempty"`
	Name           string `json:"name,omitempty"`
}

func loadConfig(dataDir string) (config, error) {
	var c config
	b, err := os.ReadFile(filepath.Join(dataDir, configName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

func saveConfig(dataDir string, c config) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dataDir, configName), append(b, '\n'))
}

// writeFileAtomic writes through a sibling temp file so a crash never leaves a half file.
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// durOr parses a stored duration, falling back to def for empty or malformed values.
func durOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func intOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func int64Or(v, def int64) int64 {
	if v <= 0 {
		return def
	}
	return v
}

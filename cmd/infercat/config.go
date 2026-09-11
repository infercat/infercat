package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/infercat/infercat/internal/product"
)

// configName holds the `serve` settings so a later `serve` with no flags reuses them.
const configName = "config.json"

// config is written at mode 0600 because --upstream-key may be a real API key.
//
// Two `serve` flags are deliberately NOT persisted: --log-prompts, because a host who debugs
// once must not keep logging their friends' conversations forever (docs/PRINCIPLES.md, Protection 3),
// and --ephemeral, because it changes the host address and is a per-run mode, not a setting.
// Keys a newer build no longer knows (queue_timeout, request_timeout, max_body, retired by ticket
// 010) are ignored on load and dropped on the next save.
type config struct {
	ProfileInstall          string `json:"profile_install,omitempty"`
	UpstreamImages          string `json:"upstream_images,omitempty"`
	UpstreamImagesKey       string `json:"upstream_images_key,omitempty"`
	UpstreamImagesModel     string `json:"upstream_images_model,omitempty"`
	UpstreamSpeechVoices    string `json:"upstream_speech_voices,omitempty"`
	UpstreamTranscribeModel string `json:"upstream_transcribe_model,omitempty"`
	UpstreamSpeechModel     string `json:"upstream_speech_model,omitempty"`
	UpstreamTranscribe      string `json:"upstream_transcribe,omitempty"`
	UpstreamTranscribeKey   string `json:"upstream_transcribe_key,omitempty"`
	UpstreamSpeech          string `json:"upstream_speech,omitempty"`
	UpstreamSpeechKey       string `json:"upstream_speech_key,omitempty"`
	MaxTranscriptionSeconds int    `json:"max_transcription_seconds,omitempty"`
	Models                  string `json:"models,omitempty"`
	Console                 string `json:"console,omitempty"`
	Upstream                string `json:"upstream,omitempty"`
	UpstreamKey             string `json:"upstream_key,omitempty"`
	Slots                   int    `json:"slots,omitempty"`
	DevListen               string `json:"dev_listen,omitempty"`
	DERPMapURL              string `json:"derpmap_url,omitempty"`
	Region                  string `json:"region,omitempty"`
	Name                    string `json:"name,omitempty"`
	WebURL                  string `json:"web_url,omitempty"`
}

// webURL is where this host's friends open the web app: the remembered --web-url, else the
// built-in product.WebURL, else empty (no hosted app yet).
func webURL(c config) string {
	if c.WebURL != "" {
		return c.WebURL
	}
	return product.WebURL
}

// configPath is the file `serve` remembers its flags in, named in the messages that depend on it.
func configPath(dataDir string) string { return filepath.Join(dataDir, configName) }

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

func intOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

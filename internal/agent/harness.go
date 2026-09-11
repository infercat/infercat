package agent

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

//go:embed assets/base.cordis.patch.yml
var baseComposition []byte

//go:embed assets/wire.mjs
var wire []byte

//go:embed assets/adapter.mjs
var adapter []byte

//go:embed assets/inherited-sandbox.mjs
var inheritedSandbox []byte

func harnessOptions(dataDir, dir, outerWorkspace string) (RuntimeOptions, error) {
	runtime, err := Installed(dataDir)
	if err != nil {
		return RuntimeOptions{}, err
	}
	actual, err := os.ReadFile(filepath.Join(runtime, "node_modules/@deepseek-ai/dsh-base/cordis.patch.yml"))
	if err != nil || !bytes.Equal(actual, baseComposition) {
		return RuntimeOptions{}, errors.New("pinned harness composition missing or changed")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return RuntimeOptions{}, err
	}
	if err = os.MkdirAll(filepath.Join(dir, "harness", "sessions"), 0700); err != nil {
		return RuntimeOptions{}, err
	}
	if err = os.WriteFile(filepath.Join(dir, "wire.mjs"), wire, 0600); err != nil {
		return RuntimeOptions{}, err
	}
	plugin := filepath.Join(dir, "adapter.mjs")
	if err = os.WriteFile(plugin, adapter, 0600); err != nil {
		return RuntimeOptions{}, err
	}
	rows := []any{
		map[string]any{"id": "headless-startup", "disabled": true},
		map[string]any{"id": "headless-runner", "disabled": true},
		map[string]any{"id": "session-persistence-jsonl", "disabled": true},
		map[string]any{"id": "session-checkpoint-policy", "disabled": true},
		map[string]any{"id": "session-projection-cache", "disabled": true},
		map[string]any{"id": "llm-deepseek", "disabled": true},
		map[string]any{"id": "llm-pi-ai", "disabled": true},
		map[string]any{"id": "agent-default-model", "config": map[string]any{"provider": "infercat", "model": "run-model"}},
		map[string]any{"id": "web", "config": map[string]any{"searchProvider": "exa"}},
		map[string]any{"id": "web-search-deepseek", "disabled": true},
		map[string]any{"insert": []any{
			map[string]any{"id": "infercat-agent", "name": plugin},
		}},
	}
	if outerWorkspace != "" {
		provider := filepath.Join(dir, "inherited-sandbox.mjs")
		if err = os.WriteFile(provider, inheritedSandbox, 0600); err != nil {
			return RuntimeOptions{}, err
		}
		rows = append(rows, map[string]any{"id": "sandbox", "disabled": true}, map[string]any{"insert": []any{
			map[string]any{"id": "infercat-outer-sandbox", "name": provider, "config": map[string]any{"workspace": outerWorkspace}},
		}})
	}
	raw, _ := json.Marshal(rows) // JSON is a YAML subset accepted by the native patch loader.
	patch := filepath.Join(dir, "adapter.patch.yml")
	if err = os.WriteFile(patch, raw, 0600); err != nil {
		return RuntimeOptions{}, err
	}
	env := []string{
		"PATH=" + filepath.Join(runtime, "node/bin") + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + dir, "DSH_HOME=" + filepath.Join(dir, "harness"),
		"DSH_TELEMETRY_MODE=DISABLED", "DSH_PERMISSION_MODE=workspace-write",
		"INFERCAT_AGENT_RUNTIME=" + runtime,
	}
	if outerWorkspace != "" {
		env = append(env, "INFERCAT_CONFINED_WORKSPACE="+outerWorkspace)
	}
	if exa := os.Getenv("EXA_API_KEY"); exa != "" {
		env = append(env, "INFERCAT_EXA_FD=4")
	}
	return RuntimeOptions{SearchKey: os.Getenv("EXA_API_KEY"), Dir: dir, Env: env, Command: []string{
		filepath.Join(runtime, "node/bin/node"), filepath.Join(runtime, "node_modules/@deepseek-ai/dsh/lib/bin.js"),
		"--profile", "headless", "--patch", patch,
	}}, nil
}

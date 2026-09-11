package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Materialize executes no shell. Only built-in commands are used for managed
// setup; custom profile commands remain data for compatibility checks.
func Materialize(p Profile, m Member, im InstalledMember, artifacts map[string]string, dir string) ([]string, []string, string, error) {
	work := filepath.Join(dir, "profiles", "members", m.ID)
	if e := os.MkdirAll(work, 0700); e != nil {
		return nil, nil, "", e
	}
	executable := ""
	for _, a := range p.Artifacts {
		if a.ID == m.Artifact && a.Executable != "" {
			executable = filepath.Join(artifacts[a.ID], a.Executable)
		}
	}
	if executable == "" || !filepath.IsAbs(executable) {
		return nil, nil, "", fmt.Errorf("missing pinned executable for %s", m.ID)
	}
	primary := im.Paths[m.Model.Assets[0].ID]
	replacements := map[string]string{"{model}": primary, "{model_dir}": filepath.Dir(primary), "{port}": strconv.Itoa(m.Port), "{model_name}": m.Model.Name, "{config}": filepath.Join(work, "engine.json")}
	for id, path := range im.Paths {
		replacements["{asset:"+id+"}"] = path
	}
	for id, path := range artifacts {
		replacements["{artifact:"+id+"}"] = path
	}
	expand := func(s string) (string, error) {
		for key, value := range replacements {
			s = strings.ReplaceAll(s, key, value)
		}
		if strings.ContainsAny(s, "{}\x00\r\n") {
			return "", fmt.Errorf("unknown command placeholder")
		}
		return s, nil
	}
	if m.Engine == "audio.cpp" {
		if st, e := os.Lstat(replacements["{config}"]); e == nil && !st.Mode().IsRegular() {
			return nil, nil, "", fmt.Errorf("engine config must be a regular file")
		}
		b, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": m.Port, "backend": p.Hardware.GPU, "threads": 4, "lazy_load": false, "idle_unload_ms": 0, "ui": false, "ui_management": false, "models": []any{map[string]any{"id": m.Model.Name, "family": "fun_asr_nano", "path": primary, "task": "asr", "mode": "offline"}}})
		if e := os.WriteFile(replacements["{config}"], b, 0600); e != nil {
			return nil, nil, "", e
		}
	}
	command := []string{executable}
	for _, arg := range append(append([]string{}, m.Command[1:]...), m.Model.ExtraArgs...) {
		v, e := expand(arg)
		if e != nil {
			return nil, nil, "", e
		}
		command = append(command, v)
	}
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + work}
	names := []string{}
	for name := range m.Env {
		if name == "" || strings.ContainsAny(name, "=\x00\r\n") {
			return nil, nil, "", fmt.Errorf("invalid engine environment")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v, e := expand(m.Env[name])
		if e != nil {
			return nil, nil, "", e
		}
		env = append(env, name+"="+v)
	}
	return command, env, work, nil
}

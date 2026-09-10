// Package agentconfig owns only checksum-verified provider spans, never agent selection.
package agentconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Config struct{ Dir, DSH string }
type Models struct {
	IDs             []string
	Context, Output int
	Vision          map[string]*bool
}
type receipt struct {
	Target, Owner, URL, Block, Hash, Before string
	Existed                                 bool
}

var names = []string{"opencode", "dsh"}

func Default() (Config, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	var err error
	if dir == "" || runtime.GOOS == "windows" {
		dir, err = os.UserConfigDir()
		if err != nil {
			return Config{}, err
		}
	}
	home := os.Getenv("DSH_HOME")
	if home == "" {
		home, err = os.UserHomeDir()
		home = filepath.Join(home, ".dsh")
	}
	if err == nil && (!filepath.IsAbs(dir) || !filepath.IsAbs(home)) {
		err = errors.New("agent config directories must be absolute")
	}
	return Config{filepath.Join(dir, "infercat", "agents"), filepath.Join(home, "settings.yaml")}, err
}
func Parse(s string) ([]string, error) {
	var out []string
	for _, name := range strings.Split(s, ",") {
		if name == "codex" {
			return nil, errors.New("Codex needs the Responses API; not yet supported")
		}
		if name != "opencode" && name != "dsh" {
			return nil, fmt.Errorf("unknown agent %q (opencode,dsh)", name)
		}
		if !strings.Contains(","+strings.Join(out, ",")+",", ","+name+",") {
			out = append(out, name)
		}
	}
	return out, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func safe(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused: %s", p)
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}
func read(path string) ([]byte, bool, error) {
	if err := safe(path); err != nil {
		return nil, false, err
	}
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 2<<20 {
		return nil, false, fmt.Errorf("not a bounded regular file: %s", path)
	}
	b, err := os.ReadFile(path)
	return b, true, err
}
func write(path string, raw []byte) error {
	if len(raw) > 2<<20 {
		return errors.New("managed configuration exceeds 2 MiB")
	}
	if err := safe(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".infercat-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}
func (c Config) locked(fn func() error) error {
	if err := safe(c.Dir); err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0700); err != nil {
		return err
	}
	p := filepath.Join(c.Dir, "lock")
	if err := safe(p); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = lock(f); err != nil {
		return fmt.Errorf("agent configuration is busy: %w", err)
	}
	return fn()
}
func (c Config) load(name string) (receipt, bool, error) {
	var r receipt
	b, exists, err := read(filepath.Join(c.Dir, name+".json"))
	if err == nil && exists {
		err = json.Unmarshal(b, &r)
	}
	if err == nil && exists && (r.Hash != digest([]byte(r.Block)) || r.Block == "" || r.Target == "") {
		err = errors.New("invalid managed receipt")
	}
	return r, exists, err
}
func (c Config) Configure(selected []string, owner, url string, m Models) (lines []string, err error) {
	if len(m.IDs) == 0 {
		return nil, errors.New("host advertised no models")
	}
	err = c.locked(func() error {
		for _, name := range selected {
			target := filepath.Join(c.Dir, "opencode.jsonc")
			if name == "dsh" {
				target = c.DSH
			}
			old, exists, e := c.load(name)
			if e != nil {
				return e
			}
			if exists && (old.Owner != owner || old.URL != url || old.Target != target) {
				return fmt.Errorf("%s already owned; use connect --unconfigure first", name)
			}
			raw, was, e := read(target)
			if e != nil {
				return e
			}
			if exists {
				if bytes.Count(raw, []byte(old.Block)) != 1 {
					return fmt.Errorf("%s managed block changed or pending; unconfigure first", name)
				}
			} else {
				provider := map[string]any{"baseURL": url, "apiKeyEnv": "INFERCAT_API_KEY", "api": "openai-completions", "models": []any{}}
				om := map[string]any{}
				for _, id := range m.IDs {
					model := map[string]any{"id": id, "input": []string{"text"}}
					if yes := m.Vision[id]; yes != nil && *yes {
						model["input"] = []string{"text", "image"}
					}
					if m.Context > 0 {
						model["contextWindow"] = m.Context
					}
					if m.Output > 0 {
						model["maxTokens"] = m.Output
					}
					provider["models"] = append(provider["models"].([]any), model)
					entry := map[string]any{"name": id}
					if m.Context > 0 && m.Output > 0 {
						entry["limit"] = map[string]int{"context": m.Context, "output": m.Output}
					}
					om[id] = entry
				}
				pb, _ := json.Marshal(provider)
				var block []byte
				at := len(raw)
				if name == "dsh" {
					block, at, e = dshBlock(raw, pb)
				} else {
					if was {
						return errors.New("unowned OpenCode extra file exists")
					}
					b, _ := json.Marshal(map[string]any{"provider": map[string]any{"infercat": map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]string{"baseURL": url, "apiKey": "unused"}, "models": om}}})
					block = []byte("// BEGIN infercat managed provider\n" + string(b) + "\n// END infercat managed provider\n")
				}
				if e != nil {
					return e
				}
				backup := filepath.Join(c.Dir, name+"-"+digest([]byte(target))+".original")
				if _, ok, e := read(backup); e != nil {
					return e
				} else if !ok {
					if e = write(backup, raw); e != nil {
						return e
					}
				}
				old = receipt{target, owner, url, string(block), digest(block), digest(raw), was}
				b, _ := json.Marshal(old)
				if e = write(filepath.Join(c.Dir, name+".json"), b); e != nil {
					return e
				} // pending before target mutation
				current, ok, e := read(target)
				if e != nil {
					return e
				}
				if ok != was || !bytes.Equal(current, raw) {
					return errors.New("config changed during insertion; unconfigure pending receipt")
				}
				out := append(append(append([]byte{}, raw[:at]...), block...), raw[at:]...)
				if e = write(target, out); e != nil {
					return e
				}
			}
			if name == "opencode" {
				lines = append(lines, "OPENCODE_CONFIG="+quote(target)+" opencode --model "+quote("infercat/"+m.IDs[0]))
			} else {
				lines = append(lines, "INFERCAT_API_KEY=unused dsh # then /model → infercat → "+fmt.Sprintf("%q", m.IDs[0]))
			}
		}
		return nil
	})
	return
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (c Config) Remove(owner string) error {
	return c.locked(func() error {
		var errs []error
		for _, name := range names {
			r, ok, err := c.load(name)
			if err == nil && ok && (owner == "" || owner == r.Owner) {
				err = c.remove(name, r)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
		return errors.Join(errs...)
	})
}
func (c Config) remove(name string, r receipt) error {
	raw, exists, err := read(r.Target)
	if err != nil {
		return err
	}
	block := []byte(r.Block)
	if bytes.Count(raw, block) > 1 {
		return errors.New("duplicate managed block; refused")
	}
	if bytes.Contains(raw, block) {
		if name == "dsh" {
			if err = removalSafe(raw, block); err != nil {
				return err
			}
		}
		out := bytes.Replace(raw, block, nil, 1)
		current, _, e := read(r.Target)
		if e != nil {
			return e
		}
		if !bytes.Equal(current, raw) {
			return errors.New("config changed during removal; file preserved")
		}
		if len(out) == 0 && !r.Existed {
			err = os.Remove(r.Target)
		} else {
			err = write(r.Target, out)
		}
		if err != nil {
			return err
		}
	} else if exists && digest(raw) != r.Before {
		return errors.New("managed block checksum changed; file preserved")
	}
	return os.Remove(filepath.Join(c.Dir, name+".json"))
}
func (c Config) List() (out []string, err error) {
	if _, e := os.Stat(c.Dir); errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	err = c.locked(func() error {
		for _, name := range names {
			r, ok, e := c.load(name)
			if e != nil {
				return e
			}
			if !ok {
				continue
			}
			b, _, e := read(r.Target)
			if e != nil {
				return e
			}
			label := name
			if bytes.Count(b, []byte(r.Block)) != 1 {
				label += " (changed or pending)"
			}
			out = append(out, label)
		}
		return nil
	})
	return
}

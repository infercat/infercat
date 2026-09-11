package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/infercat/infercat/internal/dirlock"
	"github.com/infercat/infercat/internal/profile"
)

const setupHelp = `Usage: infercat setup [--profile ID | --custom FILE] [--model-path MEMBER=PATH] [--data-dir DIR]

Inspect hardware, fetch verified built-in members, dry-start/probe/stop, then save settings.
--model-path is repeatable; asset ids also work. No system installation.
--custom checks compatibility only; it makes no performance promise.
`

func (e *env) cmdSetup(ctx context.Context, pre string, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dir := fs.String("data-dir", pre, dataDirUsage)
	id := fs.String("profile", "", "embedded profile id")
	custom := fs.String("custom", "", "custom profile JSON")
	paths := map[string]string{}
	fs.Func("model-path", "MEMBER=PATH or ASSET=PATH", func(s string) error {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" || v == "" || paths[k] != "" {
			return fmt.Errorf("expected a unique MEMBER=PATH")
		}
		paths[k] = v
		return nil
	})
	if err := e.parse(fs, setupHelp, args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *id != "" && *custom != "" {
		fmt.Fprint(e.errw, setupHelp)
		return errUsage
	}
	dataDir, err := resolveDataDir(*dir)
	if err != nil {
		return err
	}
	machine := profile.Detect(ctx, runtime.GOOS, runtime.GOARCH, dataDir, profile.Run)
	var p profile.Profile
	if *custom != "" {
		f, x := os.Open(*custom)
		if x != nil {
			return x
		}
		b, x := io.ReadAll(io.LimitReader(f, (256<<10)+1))
		f.Close()
		if x != nil {
			return x
		}
		p, err = profile.Parse(b)
	} else {
		if *id == "" {
			*id = machine.Propose()
		}
		if *id == "" {
			return fmt.Errorf("no matching profile; use --custom FILE for compatibility checking")
		}
		p, err = profile.Builtin(*id)
	}
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	var roots []string
	if home != "" {
		roots = profile.Roots(home, os.Getenv)
	}
	guard, err := dirlock.Acquire(dataDir)
	if err != nil {
		return err
	}
	defer guard.Close()
	return e.setup(ctx, dataDir, p, *custom != "", machine, paths, roots)
}
func (e *env) setup(ctx context.Context, dir string, p profile.Profile, custom bool, machine profile.Machine, paths map[string]string, roots []string) error {
	if err := machine.Compatible(p); err != nil {
		return err
	}
	keys := map[string]bool{}
	for _, m := range p.Members {
		if len(m.Model.Assets) > 0 && paths[m.ID] != "" && paths[m.Model.Assets[0].ID] != "" {
			return fmt.Errorf("specify either %s or its primary asset path", m.ID)
		}
		keys[m.ID] = true
		models := append([]profile.Model{m.Model}, m.Candidates...)
		for _, v := range models {
			for _, a := range v.Assets {
				keys[a.ID] = true
			}
		}
	}
	for k := range paths {
		if !keys[k] {
			return fmt.Errorf("unknown model-path id %q", k)
		}
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		return err
	}
	size := func(n uint64) string {
		if n == 0 {
			return "unknown"
		}
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	}
	vram := size(machine.VRAMBytes)
	if machine.GPU == "metal" {
		vram = "shared"
	}
	fmt.Fprintf(e.out, "%s/%s · RAM %s · GPU %s · VRAM %s · disk free %s\nProfile %s\n", machine.OS, machine.Arch, size(machine.RAMBytes), machine.GPUName, vram, size(machine.DiskBytes), p.ID)
	if custom {
		fmt.Fprintln(e.out, "Custom compatibility check; no performance promise.")
	} else {
		fmt.Fprintln(e.out, p.Promise)
	}
	var installation profile.Installation
	if !custom {
		for _, m := range p.Members {
			endpoint, key, model := setupFields(&cfg, m.Class)
			if endpoint != nil && (*endpoint != "" && *endpoint != m.URL() || *key != "" || *model != "" && *model != m.Model.Name) {
				return fmt.Errorf("existing %s upstream settings conflict; config unchanged", m.Class)
			}
		}
		installation, err = profile.Prepare(ctx, p, dir, paths, roots, nil, e.out)
		if err != nil {
			return err
		}
	}
	var blocked error
	for _, m := range p.Members {
		r := profile.Result{Member: m, State: "unavailable"}
		if custom {
			r = profile.Check(ctx, m, paths, roots)
		} else {
			for _, im := range installation.Members {
				if im.ID == m.ID {
					r.State = "checked"
					r.Paths = im.Paths
					if im.External {
						r.State = "running"
					}
					break
				}
			}
		}
		fmt.Fprintf(e.out, "%-9s %-7s pins %d/%d · profile RSS %s", m.ID, r.State, len(r.Paths), len(m.Model.Assets), size(m.Model.Measurement.RSSBytes))
		if m.Unavailable != "" {
			fmt.Fprintf(e.out, " · %s", m.Unavailable)
		}
		if m.Pending != "" {
			fmt.Fprintf(e.out, " · %s", m.Pending)
		}
		fmt.Fprintln(e.out)
		for _, a := range r.Missing {
			fmt.Fprintf(e.out, "  uncached: %s — %s\n", a.File, a.URL)
		}
		if r.Err != nil {
			fmt.Fprintln(e.out, "  dry check:", r.Err)
			blocked = r.Err
			continue
		}
		if m.Class == "text" && (m.Pending != "" || r.State != "running" && r.State != "checked") {
			blocked = fmt.Errorf("anchor unavailable or pending; config unchanged")
		}
		endpoint, key, model := setupFields(&cfg, m.Class)
		if endpoint == nil {
			continue
		}
		if *endpoint != "" && *endpoint != m.URL() || *key != "" || *model != "" && *model != m.Model.Name || m.Unavailable != "" && *endpoint != "" {
			blocked = fmt.Errorf("existing %s upstream settings conflict; config unchanged (use a separate --data-dir)", m.Class)
			continue
		}
		if r.State != "running" && r.State != "checked" || m.Unavailable != "" {
			continue
		}
		*endpoint = m.URL()
		*model = m.Model.Name
		if m.Class == "text" {
			cfg.Slots = m.Concurrency
		}
	}
	if custom {
		fmt.Fprintln(e.out, "Pins verify local files; health does not attest the engine's active weights. No members started or fetched.")
	}
	if blocked != nil {
		return blocked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !custom {
		name, err := profile.SaveInstallation(dir, installation)
		if err != nil {
			return err
		}
		cfg.ProfileInstall = name
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := saveConfig(dir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "Saved %s; infercat serve reuses these settings.\n", configPath(dir))
	return nil
}

func setupFields(cfg *config, class string) (endpoint, key, model *string) {
	switch class {
	case "text":
		endpoint, key, model = &cfg.Upstream, &cfg.UpstreamKey, &cfg.Models
	case "transcribe":
		endpoint, key, model = &cfg.UpstreamTranscribe, &cfg.UpstreamTranscribeKey, &cfg.UpstreamTranscribeModel
	case "speech":
		endpoint, key, model = &cfg.UpstreamSpeech, &cfg.UpstreamSpeechKey, &cfg.UpstreamSpeechModel
	case "image":
		endpoint, key, model = &cfg.UpstreamImages, &cfg.UpstreamImagesKey, &cfg.UpstreamImagesModel
	default:
		return nil, nil, nil
	}
	return
}

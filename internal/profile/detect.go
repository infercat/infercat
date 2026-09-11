package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Machine struct {
	Hardware
	GPUName   string
	DiskBytes uint64
}
type Runner func(context.Context, string, ...string) []byte

func Run(ctx context.Context, name string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// These are fixed hardware queries, never commands supplied by a profile.
	b, _ := exec.CommandContext(ctx, name, args...).Output()
	if len(b) > 1<<20 {
		return nil
	}
	return b
}
func Detect(ctx context.Context, system, arch, dir string, run Runner) Machine {
	m := Machine{Hardware: Hardware{OS: system, Arch: arch, GPU: "cpu"}}
	number := func(b []byte) uint64 { n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); return n }
	switch system {
	case "darwin":
		m.RAMBytes = number(run(ctx, "sysctl", "-n", "hw.memsize"))
		if arch == "arm64" {
			m.GPU, m.VRAMBytes = "metal", m.RAMBytes
		}
		var v struct {
			Displays []struct {
				Model string `json:"sppci_model"`
			} `json:"SPDisplaysDataType"`
		}
		nameCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_ = json.Unmarshal(run(nameCtx, "system_profiler", "SPDisplaysDataType", "-json"), &v)
		cancel()
		if len(v.Displays) > 0 {
			m.GPUName = v.Displays[0].Model
			if strings.Contains(m.GPUName, "Apple") {
				m.GPU = "metal"
			}
		}
		if arch == "arm64" && m.GPUName == "" {
			for _, name := range []string{"machdep.cpu.brand_string", "hw.model"} {
				m.GPUName = strings.TrimSpace(string(run(ctx, "sysctl", "-n", name)))
				if m.GPUName != "" {
					break
				}
			}
		}
	case "linux":
		for _, l := range strings.Split(string(run(ctx, "cat", "/proc/meminfo")), "\n") {
			var kb uint64
			if _, e := fmt.Sscanf(l, "MemTotal: %d kB", &kb); e == nil {
				m.RAMBytes = kb * 1024
				break
			}
		}
	case "windows":
		m.RAMBytes = number(run(ctx, "powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory"))
	}
	if system != "darwin" {
		for _, l := range strings.Split(string(run(ctx, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")), "\n") {
			name, n, ok := strings.Cut(l, ",")
			v := number([]byte(n)) << 20
			if ok && v > m.VRAMBytes {
				m.GPU = "nvidia"
				m.GPUName = strings.TrimSpace(name)
				m.VRAMBytes = v
			}
		}
	}
	for {
		if _, e := os.Stat(dir); e == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if system == "windows" {
		m.DiskBytes = number(run(ctx, "powershell", "-NoProfile", "-Command", "(Get-Item -LiteralPath '"+strings.ReplaceAll(dir, "'", "''")+"').PSDrive.Free"))
	} else {
		lines := strings.Split(strings.TrimSpace(string(run(ctx, "df", "-Pk", dir))), "\n")
		if len(lines) > 1 {
			f := strings.Fields(lines[len(lines)-1])
			if len(f) > 3 {
				m.DiskBytes = number([]byte(f[3])) * 1024
			}
		}
	}
	return m
}
func (m Machine) Compatible(p Profile) error {
	h := p.Hardware
	if m.OS != h.OS || m.Arch != h.Arch || m.GPU != h.GPU || m.RAMBytes < h.RAMBytes || m.VRAMBytes < h.VRAMBytes {
		return fmt.Errorf("profile %s requires %s/%s, %s, RAM %.1f GiB and VRAM %.1f GiB", p.ID, h.OS, h.Arch, h.GPU, float64(h.RAMBytes)/(1<<30), float64(h.VRAMBytes)/(1<<30))
	}
	return nil
}
func (m Machine) Propose() string {
	for _, id := range []string{"apple-64g", "apple-16g", "nvidia-12g"} {
		p, e := Builtin(id)
		if e == nil && m.Compatible(p) == nil {
			return id
		}
	}
	return ""
}

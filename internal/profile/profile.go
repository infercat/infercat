// Package profile describes and installs pinned loadouts. Custom files are compatibility-only.
package profile

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

//go:embed data/*.json
var files embed.FS

type Hardware struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	GPU        string `json:"gpu"`
	RAMBytes   uint64 `json:"ram_bytes"`
	VRAMBytes  uint64 `json:"vram_bytes"`
	MeasuredOn string `json:"measured_on"`
}
type Asset struct {
	Archive  *Archive `json:"archive,omitempty"`
	ID       string   `json:"id"`
	File     string   `json:"file"`
	Bytes    int64    `json:"bytes"`
	SHA256   string   `json:"sha256"`
	URL      string   `json:"url"`
	License  string   `json:"license"`
	Revision string   `json:"revision"`
}
type Measurement struct {
	Status               string  `json:"status"`
	Date                 string  `json:"date"`
	Source               string  `json:"source"`
	RSSBytes             uint64  `json:"rss_bytes"`
	TokensPerSecond      float64 `json:"tokens_per_second"`
	MixedTokensPerSecond float64 `json:"mixed_tokens_per_second"`
	TTFTMillis           float64 `json:"ttft_ms"`
	Note                 string  `json:"note"`
}
type Model struct {
	Name         string      `json:"name"`
	Quantization string      `json:"quantization"`
	Assets       []Asset     `json:"assets"`
	ExtraArgs    []string    `json:"extra_args"`
	Measurement  Measurement `json:"measurement"`
}
type Policy struct {
	Kind        string `json:"kind"`
	IdleSeconds int    `json:"idle_seconds"`
}
type Member struct {
	Artifact    string            `json:"artifact,omitempty"`
	ID          string            `json:"id"`
	Class       string            `json:"class"`
	Engine      string            `json:"engine"`
	EnginePin   string            `json:"engine_pin"`
	Model       Model             `json:"model"`
	Candidates  []Model           `json:"candidates"`
	Pending     string            `json:"pending"`
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env"`
	Port        int               `json:"port"`
	Context     int               `json:"context"`
	Concurrency int               `json:"concurrency"`
	Policy      Policy            `json:"policy"`
	Unavailable string            `json:"unavailable"`
}
type Headroom struct {
	OSBytes        uint64 `json:"os_bytes"`
	KVBytesPerSlot uint64 `json:"kv_bytes_per_slot"`
	Friends        int    `json:"friends"`
	Draft          bool   `json:"draft"`
}
type Artifact struct {
	Asset
	Executable string `json:"executable,omitempty"`
}

type Profile struct {
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Version   int        `json:"version"`
	ID        string     `json:"id"`
	Hardware  Hardware   `json:"hardware"`
	Members   []Member   `json:"members"`
	Headroom  Headroom   `json:"headroom"`
	Promise   string     `json:"promise"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Builtin(id string) (Profile, error) {
	if !idPattern.MatchString(id) {
		return Profile{}, fmt.Errorf("invalid profile id")
	}
	b, err := files.ReadFile("data/" + id + ".json")
	if err != nil {
		return Profile{}, err
	}
	return Parse(b)
}
func Parse(b []byte) (p Profile, err error) {
	if len(b) > 256<<10 {
		return p, fmt.Errorf("profile exceeds 256 KiB")
	}
	if err = unique(json.NewDecoder(bytes.NewReader(b)), 0, ""); err != nil {
		return
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return
	}
	if d.Decode(new(any)) != io.EOF {
		return p, fmt.Errorf("expected one JSON profile")
	}
	err = p.Validate()
	return
}

// Reject duplicate keys and null rather than silently accepting an ambiguous file.
func unique(d *json.Decoder, depth int, parent string) error {
	if depth > 16 {
		return fmt.Errorf("profile nesting exceeds 16")
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	if text, ok := t.(string); ok && !clean(text) {
		return fmt.Errorf("control character in profile")
	}
	if t == nil {
		return fmt.Errorf("null is not a profile value")
	}
	if t == json.Delim('{') || t == json.Delim('[') {
		seen := map[string]bool{}
		for d.More() {
			field := ""
			if t == json.Delim('{') {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := k.(string)
				if !ok || seen[s] || !clean(s) {
					return fmt.Errorf("duplicate or invalid key %v", k)
				}
				if parent != "env" && parent != "files" && parent != "paths" && parent != "artifacts" && s != strings.ToLower(s) {
					return fmt.Errorf("profile fields are case sensitive: %s", s)
				}
				seen[s] = true
				field = s
			}
			if e = unique(d, depth+1, field); e != nil {
				return e
			}
		}
		_, e = d.Token()
		return e
	}
	return nil
}
func (p Profile) Validate() error {
	bad := func(s string) error { return fmt.Errorf("invalid profile: %s", s) }
	if len(p.Artifacts) > 16 || p.Version != 1 || !idPattern.MatchString(p.ID) || len(p.Members) == 0 || len(p.Members) > 16 || p.Promise == "" {
		return bad("version, id, members or promise")
	}
	h := p.Hardware
	if !slices.Contains([]string{"darwin", "linux", "windows"}, h.OS) || !slices.Contains([]string{"arm64", "amd64"}, h.Arch) || !slices.Contains([]string{"metal", "nvidia", "cpu"}, h.GPU) || h.RAMBytes == 0 {
		return bad("hardware")
	}
	if p.Headroom.OSBytes == 0 || p.Headroom.OSBytes >= h.RAMBytes || p.Headroom.KVBytesPerSlot == 0 || p.Headroom.Friends < 1 || p.Headroom.Friends > 64 || p.Headroom.KVBytesPerSlot > h.RAMBytes {
		return bad("headroom")
	}
	artifacts := map[string]bool{}
	for _, a := range p.Artifacts {
		if err := validAsset(a.Asset); err != nil {
			return err
		}
		if artifacts[a.ID] || a.Archive == nil || a.Executable != "" && (!filepath.IsLocal(a.Executable) || strings.Contains(a.Executable, "\\")) {
			return bad("engine artifact")
		}
		artifacts[a.ID] = true
	}
	ids, ports := map[string]bool{}, map[int]bool{}
	anchors := 0
	for _, m := range p.Members {
		if !idPattern.MatchString(m.ID) || ids[m.ID] || m.Port < 1 || m.Port > 65535 || ports[m.Port] || m.Concurrency < 1 || m.Concurrency > 64 || m.Context < 0 || m.Context > 1<<20 {
			return bad("member identity or limits")
		}
		if m.Artifact != "" && !artifacts[m.Artifact] {
			return bad("missing engine artifact")
		}
		ids[m.ID] = true
		ports[m.Port] = true
		if !slices.Contains([]string{"text", "transcribe", "speech", "embed", "image"}, m.Class) || !slices.Contains([]string{"llama.cpp", "llama-swap", "vLLM", "audio.cpp", "sd.cpp", "sherpa-onnx"}, m.Engine) || m.EnginePin == "" || len(m.Command) == 0 || m.Command[0] == "" {
			return bad("class, engine or command")
		}
		if m.Class == "text" {
			anchors++
			if m.Context == 0 || m.Policy.Kind != "resident" || m.Unavailable != "" {
				return bad("anchor policy")
			}
		}
		if !slices.Contains([]string{"resident", "on-demand", "cpu"}, m.Policy.Kind) || (m.Policy.Kind == "on-demand") != (m.Policy.IdleSeconds > 0) || m.Policy.IdleSeconds > 86400 {
			return bad("policy")
		}
		if m.Pending != "" || len(m.Candidates) != 0 {
			return bad("pending and candidates are unsupported; select a model")
		}
		model := m.Model
		if model.Name == "" || model.Quantization == "" || len(model.Assets) == 0 || len(model.Assets) > 16 {
			return bad("model")
		}
		v := model.Measurement
		if v.TokensPerSecond < 0 || v.MixedTokensPerSecond < 0 || v.TTFTMillis < 0 || !slices.Contains([]string{"measured", "unmeasured"}, v.Status) || v.Note == "" || v.RSSBytes == 0 && v.Status == "measured" || v.Status == "unmeasured" && (v.RSSBytes != 0 || v.TokensPerSecond != 0 || v.MixedTokensPerSecond != 0 || v.TTFTMillis != 0) {
			return bad("measurement")
		}
		if _, e := time.Parse("2006-01-02", v.Date); v.Status == "measured" && (e != nil || v.Source == "") {
			return bad("measurement provenance")
		}
		assets := map[string]bool{}
		for _, a := range model.Assets {
			if assets[a.ID] {
				return bad("duplicate asset")
			}
			if err := validAsset(a); err != nil {
				return err
			}
			assets[a.ID] = true
		}
	}
	if anchors != 1 {
		return bad("exactly one text anchor required")
	}
	return nil
}

func clean(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 127 })
}

func validAsset(a Asset) error {
	u, e := url.Parse(a.URL)
	if !idPattern.MatchString(a.ID) || a.File == "" || a.File == "." || a.File == ".." || filepath.Base(a.File) != a.File || strings.ContainsAny(a.File, "*?[]\\") || a.Bytes <= 0 || !hashPattern.MatchString(a.SHA256) || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || a.License == "" || a.Revision == "" || a.Archive != nil && !validArchive(*a.Archive) {
		return fmt.Errorf("invalid asset pin: %s", a.ID)
	}
	return nil
}

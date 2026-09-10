package profile

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Roots enumerates only model stores; an explicit path can live elsewhere.
func Roots(home string, getenv func(string) string) []string {
	hf := getenv("HF_HUB_CACHE")
	if hf == "" {
		h := getenv("HF_HOME")
		if h == "" {
			c := getenv("XDG_CACHE_HOME")
			if c == "" {
				c = filepath.Join(home, ".cache")
			}
			h = filepath.Join(c, "huggingface")
		}
		hf = filepath.Join(h, "hub")
	}
	ollama := getenv("OLLAMA_MODELS")
	if ollama == "" {
		ollama = filepath.Join(home, ".ollama", "models")
	}
	return []string{hf, ollama, filepath.Join(home, ".lmstudio", "models"), filepath.Join(home, ".cache", "lm-studio", "models")}
}
func Find(ctx context.Context, a Asset, explicit string, roots []string) (string, error) {
	paths := []string{explicit}
	if explicit == "" {
		paths = nil
		for _, root := range roots {
			for _, pattern := range []string{filepath.Join(root, "blobs", "sha256-"+a.SHA256), filepath.Join(root, "models--*", "blobs", a.SHA256), filepath.Join(root, "models--*", "snapshots", "*", a.File), filepath.Join(root, "*", "*", a.File)} {
				hits, _ := filepath.Glob(pattern)
				paths = append(paths, hits...)
			}
		}
	}
	if len(paths) > 1024 {
		return "", fmt.Errorf("too many cached candidates for %s", a.ID)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		real, e := filepath.EvalSymlinks(path)
		if e != nil {
			if explicit != "" {
				return "", e
			}
			continue
		}
		if seen[real] {
			continue
		}
		seen[real] = true
		f, e := os.Open(real)
		if e != nil {
			if explicit != "" {
				return "", e
			}
			continue
		}
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size() != a.Bytes {
			f.Close()
			if explicit != "" {
				return "", fmt.Errorf("%s: size or file type mismatch", a.ID)
			}
			continue
		}
		h := sha256.New()
		buf := make([]byte, 1<<20)
		for e == nil {
			if e = ctx.Err(); e != nil {
				break
			}
			var n int
			n, e = f.Read(buf)
			h.Write(buf[:n])
		}
		f.Close()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if e == io.EOF && fmt.Sprintf("%x", h.Sum(nil)) == a.SHA256 {
			return real, nil
		}
		if explicit != "" {
			return "", fmt.Errorf("%s: SHA-256 mismatch or interrupted read", a.ID)
		}
	}
	return "", nil
}

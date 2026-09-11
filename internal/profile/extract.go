package profile

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Archive struct {
	Format string `json:"format"`
	Strip  int    `json:"strip"`
	Bytes  int64  `json:"bytes"`
}

// Unpack is called only after pin verification. Links publish last; no entry can
// traverse one, and every resolved link must remain inside the completed tree.
func Unpack(ctx context.Context, archive, dest string, spec Archive) error {
	if !validArchive(spec) {
		return fmt.Errorf("invalid archive bounds")
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		return err
	}
	good := false
	defer func() {
		if !good {
			os.RemoveAll(dest)
		}
	}()
	seen := map[string]bool{}
	links := map[string]string{}
	var total int64
	entries := 0
	entry := func(name string, mode os.FileMode, size int64, target string, source io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 100000 {
			return fmt.Errorf("archive entry limit")
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("archive absolute path")
		}
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		for _, p := range parts {
			if p == ".." {
				return fmt.Errorf("archive traversal")
			}
		}
		if len(parts) <= spec.Strip {
			if mode.IsDir() {
				return nil
			}
			return fmt.Errorf("archive strip drops file")
		}
		rel := filepath.Clean(filepath.Join(parts[spec.Strip:]...))
		if !filepath.IsLocal(rel) || rel == "." || seen[rel] {
			return fmt.Errorf("archive duplicate or invalid path")
		}
		seen[rel] = true
		path := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		switch {
		case mode.IsDir():
			return os.MkdirAll(path, 0700)
		case mode&os.ModeSymlink != 0:
			if strings.Contains(target, "\\") || filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target)) {
				return fmt.Errorf("archive link escapes tree")
			}
			links[path] = target
			return nil
		case mode.IsRegular():
			if size < 0 || size > spec.Bytes-total {
				return fmt.Errorf("archive expanded size limit")
			}
			total += size
			f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0755|0600)
			if e != nil {
				return e
			}
			_, e = io.CopyN(f, source, size)
			ce := f.Close()
			if e != nil {
				return e
			}
			return ce
		default:
			return fmt.Errorf("unsupported archive entry")
		}
	}
	if spec.Format == "zip" {
		z, e := zip.OpenReader(archive)
		if e != nil {
			return e
		}
		defer z.Close()
		for _, f := range z.File {
			r, e := f.Open()
			if e != nil {
				return e
			}
			target := ""
			if f.Mode()&os.ModeSymlink != 0 {
				b, x := io.ReadAll(io.LimitReader(r, 4097))
				if x != nil || len(b) > 4096 {
					r.Close()
					return fmt.Errorf("archive link limit")
				}
				target = string(b)
			}
			e = entry(f.Name, f.Mode(), int64(f.UncompressedSize64), target, r)
			r.Close()
			if e != nil {
				return e
			}
		}
	} else {
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		var reader io.Reader = bzip2.NewReader(f)
		if spec.Format == "tar.gz" {
			gz, e := gzip.NewReader(f)
			if e != nil {
				return e
			}
			defer gz.Close()
			reader = gz
		}
		tr := tar.NewReader(reader)
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeSymlink {
				return fmt.Errorf("unsupported tar entry")
			}
			if e = entry(h.Name, h.FileInfo().Mode(), h.Size, h.Linkname, tr); e != nil {
				return e
			}
		}
	}
	for path, target := range links {
		if e := os.Symlink(target, path); e != nil {
			return e
		}
	}
	root, e := filepath.EvalSymlinks(dest)
	if e != nil {
		return e
	}
	for path := range links {
		real, e := filepath.EvalSymlinks(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, real)
		if e != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("archive link leaves tree")
		}
	}
	good = true
	return nil
}
func validArchive(a Archive) bool {
	return (a.Format == "tar.gz" || a.Format == "tar.bz2" || a.Format == "zip") && a.Strip >= 0 && a.Strip <= 4 && a.Bytes > 0 && a.Bytes <= 8<<30
}

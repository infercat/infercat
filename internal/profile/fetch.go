package profile

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Fetch resumes a partial file, but only an exact size and hash may become usable.
func Fetch(ctx context.Context, a Asset, dir string, client *http.Client, out io.Writer) (string, error) {
	if err := validAsset(a); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, a.SHA256+"-"+a.File)
	part := path + ".part"
	for _, name := range []string{path, part} {
		if st, e := os.Lstat(name); e == nil && !st.Mode().IsRegular() {
			return "", fmt.Errorf("download target is not a regular file")
		} else if e != nil && !os.IsNotExist(e) {
			return "", e
		}
	}
	if _, e := os.Stat(path); e == nil {
		return Find(ctx, a, path, nil)
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := st.Size()
	if offset > a.Bytes {
		return "", fmt.Errorf("partial download exceeds pinned size")
	}
	if offset < a.Bytes {
		fmt.Fprintf(out, "Fetching %s (%d bytes, resume %d)\n", a.File, a.Bytes, offset)
		req, e := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
		if e != nil {
			return "", e
		}
		req.Header.Set("Accept-Encoding", "identity")
		if offset > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		}
		c := http.Client{Timeout: time.Hour}
		if client != nil {
			c = *client
		}
		c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
			if r.URL.Scheme != "https" || r.URL.User != nil || len(via) > 5 {
				return fmt.Errorf("download redirect refused")
			}
			return nil
		}
		res, e := c.Do(req)
		if e != nil {
			return "", e
		}
		defer res.Body.Close()
		switch res.StatusCode {
		case 200:
			offset = 0
			if e = f.Truncate(0); e != nil {
				return "", e
			}
		case 206:
			if res.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, a.Bytes-1, a.Bytes) {
				return "", fmt.Errorf("download range does not match pin")
			}
		default:
			return "", fmt.Errorf("download HTTP %d", res.StatusCode)
		}
		if _, e = f.Seek(offset, 0); e != nil {
			return "", e
		}
		n, e := io.Copy(f, io.LimitReader(res.Body, a.Bytes-offset+1))
		if n > a.Bytes-offset {
			f.Close()
			os.Remove(part)
			return "", fmt.Errorf("download exceeds pinned size")
		}
		if e != nil {
			return "", e
		}
		if n+offset != a.Bytes {
			return "", io.ErrUnexpectedEOF
		}
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if _, err = Find(ctx, a, part, nil); err != nil {
		os.Remove(part)
		return "", err
	}
	if err = os.Rename(part, path); err != nil {
		return "", err
	}
	return path, nil
}
func fileHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil)), e
}

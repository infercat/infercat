// Command install-fixture serves local release fixtures for install_test.sh.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
func write(path string, data []byte) { must(os.WriteFile(path, data, 0600)) }
func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: install-fixture DIR")
	}
	root := os.Args[1]
	web := filepath.Join(root, "web")
	for _, kind := range []string{"good", "bad-checksum", "short-download", "bad-archive", "missing-checksum"} {
		folder := filepath.Join(web, kind, "download", "v0.1.0")
		must(os.MkdirAll(folder, 0700))
		latest, err := json.Marshal(map[string]any{"url": "https://api.github.com/repos/infercat/infercat/releases/1", "tag_name": "v0.1.0", "name": "Infercat 0.1.0", "body": strings.Repeat("Release notes. ", 2900), "assets": []any{}})
		must(err)
		write(filepath.Join(web, kind, "latest"), latest)
		var sums strings.Builder
		for _, platform := range []string{"linux_amd64", "darwin_arm64"} {
			name := "infercat_0.1.0_" + platform + ".tar.gz"
			data := []byte("#!/bin/sh\n[ \"$1\" = version ] || exit 1\nprintf \"infercat 0.1.0 fixture\\n\"\n")
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			must(tw.WriteHeader(&tar.Header{Name: "infercat", Mode: 0755, Size: int64(len(data))}))
			_, err := tw.Write(data)
			must(err)
			must(tw.Close())
			must(gz.Close())
			archive := buf.Bytes()
			digest := fmt.Sprintf("%x", sha256.Sum256(archive))
			if kind == "bad-checksum" {
				digest = strings.Repeat("0", 64)
			}
			if kind == "short-download" || kind == "bad-archive" {
				archive = archive[:16]
			}
			if kind == "bad-archive" {
				digest = fmt.Sprintf("%x", sha256.Sum256(archive))
			}
			write(filepath.Join(folder, name), archive)
			if kind != "missing-checksum" {
				fmt.Fprintf(&sums, "%s  %s\n", digest, name)
			}
		}
		write(filepath.Join(folder, "infercat_0.1.0_checksums.txt"), []byte(sums.String()))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	write(filepath.Join(root, "port"), []byte(fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)))
	must(http.Serve(ln, http.FileServer(http.Dir(web))))
}

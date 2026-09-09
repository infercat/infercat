package console

import (
	"embed"
	"io/fs"
)

//go:embed dist
var bundle embed.FS

func Files() fs.FS { f, _ := fs.Sub(bundle, "dist"); return f }

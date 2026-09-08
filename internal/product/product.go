// Package product holds the product name and version. Rename here and nowhere else.
package product

const (
	Name    = "Infercat" // founder decision 2026-09-05 (docs/PRINCIPLES.md)
	CLIName = "infercat"
	// InvitePrefix is the invite format version tag (docs/ARCHITECTURE.md).
	InvitePrefix = "ic1"
	// WebURL is where a friend opens the web app. Empty until hosting is decided; a host can
	// name their own with `serve --web-url`. When it is known, an invite is printed as a link
	// (<WebURL>#<invite>) that the friend can click instead of copying a code.
	WebURL = "https://infercat.ai"
)

// The build stamp. A release overwrites these at link time (.goreleaser.yaml passes
// -X github.com/infercat/infercat/internal/product.Version=<tag> and friends); a plain
// `go build` keeps the defaults, which is how a developer build says it is one. Version is
// also the single place the Makefile and the web bundle read the version from (docs/PRINCIPLES.md:
// the product name lives in one constant), so keep the literal on one line.
var (
	Version = "0.1.0"
	Commit  = "none"
	Date    = "unknown"
)

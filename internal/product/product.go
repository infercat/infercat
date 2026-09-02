// Package product holds the working product name and version. Rename here and nowhere else.
package product

const (
	Name    = "Bunny Network" // working name; not locked (pm/BELIEFS.md)
	CLIName = "bunny-network"
	// InvitePrefix is the invite format version tag (docs/ARCHITECTURE.md).
	InvitePrefix = "bn1"
	// WebURL is where a friend opens the web app. Empty until hosting is decided; a host can
	// name their own with `serve --web-url`. When it is known, an invite is printed as a link
	// (<WebURL>#<invite>) that the friend can click instead of copying a code.
	WebURL = ""
)

// The build stamp. A release overwrites these at link time (.goreleaser.yaml passes
// -X github.com/2185Lab/bunny-network/internal/product.Version=<tag> and friends); a plain
// `go build` keeps the defaults, which is how a developer build says it is one. Version is
// also the single place the Makefile and the web bundle read the version from (pm/BELIEFS.md:
// the product name lives in one constant), so keep the literal on one line.
var (
	Version = "0.0.1-dev"
	Commit  = "none"
	Date    = "unknown"
)

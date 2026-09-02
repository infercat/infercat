// Package product holds the working product name and version. Rename here and nowhere else.
package product

const (
	Name    = "Bunny Network" // working name; not locked (pm/BELIEFS.md)
	CLIName = "bunny-network"
	Version = "0.0.1-dev"
	// InvitePrefix is the invite format version tag (docs/ARCHITECTURE.md).
	InvitePrefix = "bn1"
	// WebURL is where a friend opens the web app. Empty until hosting is decided; a host can
	// name their own with `serve --web-url`. When it is known, an invite is printed as a link
	// (<WebURL>#<invite>) that the friend can click instead of copying a code.
	WebURL = ""
)

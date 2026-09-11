// Package invite encodes and decodes the one string a friend pastes:
//
//	ic1.<legacy tailcat address>.<secret>
//	ic2.<PSK tailcat address>.<secret>
//
// TypeScript mirrors: web/src/invite.ts and packages/client/src/invite.ts; all three
// MUST agree (docs/ARCHITECTURE.md §Invite format).
package invite

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/infercat/infercat/internal/product"
	"github.com/tailscale/tailcat"
)

// Invite is the decoded form. Addr is the tailcat address ("tc…"); Secret is the friend's key.
type Invite struct {
	Addr   string
	Secret string
}

// Decode errors. Compare with errors.Is; the messages are for humans. An "ic<N>" prefix with N > 2
// wraps ErrPrefix with a message saying the invite needs a newer app.
var (
	ErrPrefix      = errors.New("not an " + product.Name + " invite (missing or wrong prefix)")
	ErrParts       = errors.New("invite must have exactly three dot-separated parts")
	ErrEmptyPart   = errors.New("invite has an empty part")
	ErrAddr        = errors.New("invite address is not a tailcat address")
	ErrSecretChars = errors.New("invite secret has characters outside base64url")
)

// Encode builds the invite string. It does not validate its inputs; Decode does.
func Encode(addr, secret string) string {
	prefix := product.InvitePrefix
	if ci, err := tailcat.ParseAddr(tailcat.Addr(addr)); err == nil && !ci.PresharedKey.IsZero() {
		prefix = prefixFamily + "2"
	}
	return prefix + "." + addr + "." + secret
}

// Decode parses s. Surrounding whitespace is trimmed; everything else is case-sensitive.
func Decode(s string) (Invite, error) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	// The prefix is checked before the part count so an invite from a newer app says so even
	// if its layout differs.
	if err := checkPrefix(parts[0]); err != nil {
		return Invite{}, err
	}
	if len(parts) != 3 {
		return Invite{}, ErrParts
	}
	addr, secret := parts[1], parts[2]
	if addr == "" || secret == "" {
		return Invite{}, ErrEmptyPart
	}
	if !strings.HasPrefix(addr, "tc") || len(addr) == len("tc") || !isBase64URL(addr) {
		return Invite{}, ErrAddr
	}
	if !isBase64URL(secret) {
		return Invite{}, ErrSecretChars
	}
	return Invite{Addr: addr, Secret: secret}, nil
}

// prefixFamily is InvitePrefix without its version number ("ic" of "ic1"): the letters that say an
// invite is ours at all, so a newer format is recognised as ahead of us rather than as junk. It
// follows the constant, because the family changes with the product name (037).
var prefixFamily = strings.TrimRight(product.InvitePrefix, "0123456789")

func checkPrefix(p string) error {
	if p == product.InvitePrefix || p == prefixFamily+"2" {
		return nil
	}
	if digits, ok := strings.CutPrefix(p, prefixFamily); ok {
		if n, err := strconv.Atoi(digits); err == nil && n > 2 {
			return fmt.Errorf("%w: this invite needs a newer app (format %s)", ErrPrefix, p)
		}
	}
	return ErrPrefix
}

// isBase64URL reports whether s is non-empty and made only of the unpadded base64url alphabet.
func isBase64URL(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

package tunnel

import "github.com/tailscale/tailcat"

// Display is a human-only address: never use it to mint an invite or connect.
// Re-encode without the PSK; its bytes need not align with base64 character boundaries.
func Display(addr string) string {
	if addr == "" {
		return ""
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(addr))
	if err != nil {
		return "[invalid tunnel address]"
	}
	if ci.PresharedKey.IsZero() {
		return addr
	}
	ci.PresharedKey = tailcat.PresharedKey{}
	return string(ci.Addr()) + "…psk:••••"
}

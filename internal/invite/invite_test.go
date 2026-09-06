package invite

import (
	"encoding/base64"
	"errors"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// realAddr is a tailcat ConnBlob exactly as Server.ConnBlob would produce for a fresh key.
func realAddr(t *testing.T) string {
	t.Helper()
	priv := key.NewNode()
	ci := tailcat.ConnInfo{
		ServerPublic:      tailcat.NodePublic{NodePublic: priv.Public()},
		ServerDiscoPublic: tailcat.DiscoPublicForNode(priv),
		RegionID:          302,
	}
	return string(ci.ConnBlob())
}

func TestRoundTripRealShape(t *testing.T) {
	addr := realAddr(t)
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	s := Encode(addr, secret)
	if strings.Count(s, ".") != 2 {
		t.Fatalf("encoded invite has %d dots, want 2: %q", strings.Count(s, "."), s)
	}
	got, err := Decode(s)
	if err != nil {
		t.Fatalf("Decode(%q): %v", s, err)
	}
	if got.Addr != addr || got.Secret != secret {
		t.Fatalf("round trip = %+v; want addr %q secret %q", got, addr, secret)
	}
	if _, err := tailcat.ParseConnBlob(tailcat.ConnBlob(got.Addr)); err != nil {
		t.Fatalf("decoded addr is not a parseable ConnBlob: %v", err)
	}
	// Whitespace is trimmed, nothing else.
	if got2, err := Decode("  \n" + s + "\t\r\n"); err != nil || got2 != got {
		t.Fatalf("Decode with surrounding whitespace = %+v, %v; want %+v", got2, err, got)
	}
}

// TestRoundTripProperty: for any base64url secret and any base64url tail after "tc", Decode(Encode) is the identity.
func TestRoundTripProperty(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	gen := func(r *rand.Rand, min, max int) string {
		n := min + r.Intn(max-min+1)
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		return string(b)
	}
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		addr := "tc" + gen(r, 1, 400)
		secret := gen(r, 1, 100)
		got, err := Decode(Encode(addr, secret))
		return err == nil && got.Addr == addr && got.Secret == secret
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeErrors(t *testing.T) {
	addr := realAddr(t)
	ok := addr + ".s3cr3t-_OK"
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrPrefix},
		{"whitespace_only", " \n\t", ErrPrefix},
		{"no_dots", "ic1", ErrParts}, // one part, prefix ok → part count
		{"missing_prefix", addr + ".secret", ErrPrefix},
		{"wrong_prefix", "xx1." + ok, ErrPrefix},
		{"prefix_case", "IC1." + ok, ErrPrefix},
		{"prefix_ic_only", "ic." + ok, ErrPrefix},
		{"prefix_icx", "icx." + ok, ErrPrefix},
		{"prefix_ic0", "ic0." + ok, ErrPrefix},
		{"prefix_ic01", "ic01." + ok, ErrPrefix},
		{"prefix_huge", "ic99999999999999999999." + ok, ErrPrefix},
		{"newer_ic2", "ic2." + ok, ErrPrefix},
		{"newer_ic10_four_parts", "ic10." + ok + ".extra", ErrPrefix},
		{"two_parts", "ic1." + addr, ErrParts},
		{"four_parts", "ic1." + ok + ".extra", ErrParts},
		{"trailing_dot", "ic1." + ok + ".", ErrParts},
		{"leading_dot", ".ic1." + ok, ErrPrefix},
		{"empty_addr", "ic1..secret", ErrEmptyPart},
		{"empty_secret", "ic1." + addr + ".", ErrEmptyPart},
		{"both_empty", "ic1..", ErrEmptyPart},
		{"addr_no_tc", "ic1.ABC.secret", ErrAddr},
		{"addr_tc_only", "ic1.tc.secret", ErrAddr},
		{"addr_bad_char", "ic1.tcAB+C.secret", ErrAddr},
		{"addr_space", "ic1.tcAB C.secret", ErrAddr},
		{"addr_nul", "ic1.tcAB\x00C.secret", ErrAddr},
		{"addr_unicode", "ic1.tcABÇ.secret", ErrAddr},
		{"secret_plus", "ic1." + addr + ".sec+ret", ErrSecretChars},
		{"secret_slash", "ic1." + addr + ".sec/ret", ErrSecretChars},
		{"secret_padding", "ic1." + addr + ".secret=", ErrSecretChars},
		{"secret_inner_space", "ic1." + addr + ".sec ret", ErrSecretChars},
		{"secret_zero_width", "ic1." + addr + ".sec​ret", ErrSecretChars},
		{"secret_newline", "ic1." + addr + ".sec\nret", ErrSecretChars},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Decode(%q) = %+v, %v; want error %v", tt.in, got, err, tt.want)
			}
			if got != (Invite{}) {
				t.Fatalf("Decode(%q) returned a non-zero Invite alongside an error: %+v", tt.in, got)
			}
			if strings.HasPrefix(tt.name, "newer") && !strings.Contains(err.Error(), "needs a newer app") {
				t.Fatalf("Decode(%q) error %q should say the invite needs a newer app", tt.in, err)
			}
			if !strings.HasPrefix(tt.name, "newer") && strings.Contains(err.Error(), "newer app") {
				t.Fatalf("Decode(%q) error %q wrongly claims a newer app is needed", tt.in, err)
			}
		})
	}
}

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
		{"no_dots", "bn1", ErrParts}, // one part, prefix ok → part count
		{"missing_prefix", addr + ".secret", ErrPrefix},
		{"wrong_prefix", "xx1." + ok, ErrPrefix},
		{"prefix_case", "BN1." + ok, ErrPrefix},
		{"prefix_bn_only", "bn." + ok, ErrPrefix},
		{"prefix_bnx", "bnx." + ok, ErrPrefix},
		{"prefix_bn0", "bn0." + ok, ErrPrefix},
		{"prefix_bn01", "bn01." + ok, ErrPrefix},
		{"prefix_huge", "bn99999999999999999999." + ok, ErrPrefix},
		{"newer_bn2", "bn2." + ok, ErrPrefix},
		{"newer_bn10_four_parts", "bn10." + ok + ".extra", ErrPrefix},
		{"two_parts", "bn1." + addr, ErrParts},
		{"four_parts", "bn1." + ok + ".extra", ErrParts},
		{"trailing_dot", "bn1." + ok + ".", ErrParts},
		{"leading_dot", ".bn1." + ok, ErrPrefix},
		{"empty_addr", "bn1..secret", ErrEmptyPart},
		{"empty_secret", "bn1." + addr + ".", ErrEmptyPart},
		{"both_empty", "bn1..", ErrEmptyPart},
		{"addr_no_tc", "bn1.ABC.secret", ErrAddr},
		{"addr_tc_only", "bn1.tc.secret", ErrAddr},
		{"addr_bad_char", "bn1.tcAB+C.secret", ErrAddr},
		{"addr_space", "bn1.tcAB C.secret", ErrAddr},
		{"addr_nul", "bn1.tcAB\x00C.secret", ErrAddr},
		{"addr_unicode", "bn1.tcABÇ.secret", ErrAddr},
		{"secret_plus", "bn1." + addr + ".sec+ret", ErrSecretChars},
		{"secret_slash", "bn1." + addr + ".sec/ret", ErrSecretChars},
		{"secret_padding", "bn1." + addr + ".secret=", ErrSecretChars},
		{"secret_inner_space", "bn1." + addr + ".sec ret", ErrSecretChars},
		{"secret_zero_width", "bn1." + addr + ".sec​ret", ErrSecretChars},
		{"secret_newline", "bn1." + addr + ".sec\nret", ErrSecretChars},
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

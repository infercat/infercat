package speech

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHanOnlyNumberNormalization(t *testing.T) {
	for input, want := range map[string]string{
		"123": "123", "Call 13800138000 in 2025, price 123.45.": "Call 13800138000 in 2025, price 123.45.",
		"今天是2025年9月10日，价格是123.45元，电话号码是13800138000。":             "今天是二零二五年九月十日，价格是一百二十三点四五元，电话号码是幺三八零零幺三八零零零。",
		"日期2025-09-10，另一天2025/12/31。":                            "日期二零二五年九月十日，另一天二零二五年十二月三十一日。",
		"中文0 10 11 20 101 110 1001 1010 9999 10000 00123 -12.05": "中文零 十 十一 二十 一百零一 一百一十 一千零一 一千零一十 九千九百九十九 一零零零零 零零一二三 -十二点零五",
	} {
		t.Run(input, func(t *testing.T) {
			if got := normalizeHan(input); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
			s := httptest.NewServer(New(func(_ context.Context, text string, _ int, _ float32, emit func([]float32) error) error {
				if text != want {
					t.Errorf("native received %q", text)
				}
				return emit([]float32{0})
			}))
			defer s.Close()
			r := post(t, s.URL, fmt.Sprintf(`{"input":%q}`, input))
			io.Copy(io.Discard, r.Body)
			if r.StatusCode != 200 {
				t.Fatal(r.Status)
			}
		})
	}
}
func TestReflectedUnknownFieldIsElided(t *testing.T) {
	s := httptest.NewServer(New(nil))
	defer s.Close()
	field := strings.Repeat("名", 1000)
	r := post(t, s.URL, fmt.Sprintf(`{"input":"hi",%q:0}`, field))
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 400 || !strings.Contains(string(b), strings.Repeat("名", 63)+"…") || strings.Contains(string(b), strings.Repeat("名", 64)) {
		t.Fatalf("unbounded refusal: %d bytes", len(b))
	}
}
func TestReviewerLongInputsRefuseBeforeNative(t *testing.T) {
	acronyms := "TCP UDP DNS TLS SSL SDK CLI GPU CPU SSD USB PCI SQL CSS XML SSH FTP RPC DDR LCD LED PDF CSV TSV SVG PNG JPG GIF DVD BIOS UEFI NTFS EXT HTTP DHCP SMTP HTML XSLT LDAP "
	s := httptest.NewServer(New(func(context.Context, string, int, float32, func([]float32) error) error {
		t.Fatal("overlong input dispatched")
		return nil
	}))
	defer s.Close()
	for _, tc := range []struct {
		text  string
		speed int
	}{{strings.Repeat("HTTP HTTPS TCP UDP SMTP IMAP DNS DHCP TLS SSL API SDK ", 20)[:900], 1}, {strings.Repeat("a b c d e ", 100)[:900], 1}, {strings.Repeat("HTTP HTTPS TCP UDP SMTP IMAP DNS DHCP TLS SSL API SDK ", 40)[:1800], 2}, {strings.Repeat(acronyms, 20)[:1800], 2}} {
		r := post(t, s.URL, fmt.Sprintf(`{"input":%q,"speed":%d}`, tc.text, tc.speed))
		if r.StatusCode != 400 {
			t.Fatal(r.Status)
		}
	}
}

package speech

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var hanDate = regexp.MustCompile(`([0-9]{4})[-/]([0-9]{1,2})[-/]([0-9]{1,2})`)
var hanNumber = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:年)?`)

// Normalize only Han-containing input. In particular, English digits must reach
// Kokoro's English phonemizer unchanged; global Chinese FSTs cannot do that.
func normalizeHan(text string) string {
	if !strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		return text
	}
	text = hanDate.ReplaceAllStringFunc(text, func(date string) string {
		parts := hanDate.FindStringSubmatch(date)
		return parts[1] + "年" + strings.TrimLeft(parts[2], "0") + "月" + strings.TrimLeft(parts[3], "0") + "日"
	})
	return hanNumber.ReplaceAllStringFunc(text, func(value string) string {
		if year, ok := strings.CutSuffix(value, "年"); ok {
			return hanDigits(year, false) + "年"
		}
		if len(value) == 11 && value[0] == '1' && value[1] >= '3' && value[1] <= '9' {
			return hanDigits(value, true)
		}
		whole, decimal, hasDecimal := strings.Cut(value, ".")
		result := hanInteger(whole)
		if hasDecimal {
			result += "点" + hanDigits(decimal, false)
		}
		return result
	})
}

func hanDigits(s string, phone bool) string {
	digits := []rune("零一二三四五六七八九")
	if phone {
		digits[1] = '幺'
	}
	var b strings.Builder
	for _, c := range s {
		b.WriteRune(digits[c-'0'])
	}
	return b.String()
}

func hanInteger(s string) string {
	// Long identifiers and leading zeroes are read digit by digit, never lost
	// through integer parsing. Cardinal expansion is bounded to four digits.
	if len(s) > 4 || s[0] == '0' {
		return hanDigits(s, false)
	}
	n, _ := strconv.Atoi(s)
	var b strings.Builder
	zero := false
	for i, place := range []int{1000, 100, 10, 1} {
		digit := n / place
		n %= place
		if digit == 0 {
			zero = b.Len() > 0
			continue
		}
		if zero {
			b.WriteString("零")
			zero = false
		}
		if digit != 1 || place != 10 || b.Len() > 0 {
			b.WriteString(hanDigits(strconv.Itoa(digit), false))
		}
		b.WriteString([]string{"千", "百", "十", ""}[i])
	}
	return b.String()
}

// IsChinese counts Han letters using the shared 30-percent speech-language threshold.
func IsChinese(text string) bool {
	han, letters := 0, 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			han++
			letters++
		} else if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters > 0 && han*10 >= letters*3
}

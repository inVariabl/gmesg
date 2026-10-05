package tui

import "testing"

// Regression test: emoji sequences in a conversation name or snippet must
// occupy the same number of columns under bubbletea's grapheme measurement
// as under a per-code-point wcwidth(3) terminal such as st. A mismatch
// wrapped the row and left duplicate rows on the screen during j/k
// navigation. The expected values below were measured with glibc wcwidth.
func TestNormalizeColumnWidth(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"👨‍👩‍👧 Family", "👨👩👧 Family"},
		{"❤️ love", "❤ love"},
		{"👍\U0001F3FD ok", "👍 ok"},
		{"1️⃣ one", "1 one"},
		{"🏴\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F eng", "🏴 eng"},
		{"🇺🇸 flag", "🇺🇸 flag"},
		{"日本語 café", "日本語 café"},
	}
	for _, c := range cases {
		if got := normalizeColumnWidth(c.in); got != c.want {
			t.Errorf("normalizeColumnWidth(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

package logsafe

import (
	"strings"
	"testing"
)

func TestSanitizeStripsControlChars(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain path", "plain path"},
		{"a\nb", "a b"},     // newline (log-injection vector) -> space
		{"a\rb", "a b"},     // carriage return -> space
		{"a\tb", "a b"},     // tab -> space
		{"a\x00b", "a b"},   // NUL -> space
		{"a\x7fb", "a b"},   // DEL -> space
		{"a\u0085b", "a b"}, // NEL (C1 control) -> space
		{"a\u2028b", "a b"}, // line separator -> space
		{"a\u2029b", "a b"}, // paragraph separator -> space
		{"no controls", "no controls"},
		{"", ""},
		{"unicode é ü preserved", "unicode é ü preserved"},
	}
	for _, tc := range tests {
		if got := Sanitize(tc.in); got != tc.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeTruncates(t *testing.T) {
	long := strings.Repeat("a", 1000)
	got := Sanitize(long)
	if len([]rune(got)) != 257 { // 256 + the ellipsis
		t.Errorf("len = %d runes, want %d", len([]rune(got)), 257)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got[len(got)-10:])
	}
	// Short strings are not truncated or altered.
	if got := Sanitize("short"); got != "short" {
		t.Errorf("Sanitize(short) = %q", got)
	}
}

// A value that would forge a second log line must not survive sanitization.
func TestSanitizeDefeatsLogForging(t *testing.T) {
	forged := "status=200\nINJECTED key=value"
	if strings.Contains(Sanitize(forged), "\n") {
		t.Error("Sanitize left a newline in place; log forging possible")
	}
}

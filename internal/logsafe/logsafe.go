// Package logsafe makes attacker-controlled request values (URL paths,
// remote addresses, header values) safe to write to logs, preventing
// log-line forging (control characters) and log flooding (oversized values).
package logsafe

import (
	"strings"
	"unicode"
)

// isLineBreak matches C0 and C1 control characters, DEL, and the Unicode
// line/paragraph separators, which all act as line breaks in some parsers.
func isLineBreak(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}

// maxLen caps a logged field so one request cannot flood the log with an
// oversized value (e.g. a very long path or header).
const maxLen = 256

// Sanitize replaces line-breaking and control characters with spaces and
// truncates the result to maxLen runes. Printable content, including UTF-8,
// is preserved.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isLineBreak(r) {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	s = b.String()
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen]) + "…"
	}
	return s
}

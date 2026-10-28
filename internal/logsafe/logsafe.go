// Package logsafe makes attacker-controlled strings safe to write to logs.
//
// HTTP request data (URL paths, methods, remote addresses, header-derived
// values) is untrusted input. Writing it raw to a log allows log-line
// forging (embedded newlines), log-parser corruption, and log flooding.
// Sanitize should be applied to any untrusted value before it reaches a
// logger; it is a no-op for well-formed values.
package logsafe

import "regexp"

// controlRe matches the ASCII C0 control set plus DEL. Newlines and carriage
// returns are the primary log-injection vector; the rest are stripped so a
// single value cannot corrupt the log's structure.
var controlRe = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// maxLen caps a logged field so one request cannot flood the log with an
// oversized value (e.g. a very long path or header).
const maxLen = 256

// Sanitize returns s in a form that is safe to write to a log: ASCII control
// characters are replaced with spaces and the result is truncated to maxLen
// runes. Printable content, including UTF-8, is preserved.
func Sanitize(s string) string {
	s = controlRe.ReplaceAllString(s, " ")
	r := []rune(s)
	if len(r) > maxLen {
		s = string(r[:maxLen]) + "…"
	}
	return s
}

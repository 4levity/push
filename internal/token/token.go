// Package token handles raw tokens and their sha256 hashes.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// Hash returns the lowercase hex sha256 digest of raw.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Matches reports whether raw hashes to hexHash, using a constant-time compare.
func Matches(hexHash, raw string) bool {
	sum := sha256.Sum256([]byte(raw))
	got := hex.EncodeToString(sum[:])
	want := strings.ToLower(hexHash)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// Generate returns a new random 256-bit token as lowercase hex.
func Generate() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

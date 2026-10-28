package token

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestHashKnownVector(t *testing.T) {
	// sha256("abc")
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := Hash("abc"); got != want {
		t.Errorf("Hash(abc) = %q, want %q", got, want)
	}
}

func TestMatches(t *testing.T) {
	h := Hash("s3cret")
	if !Matches(h, "s3cret") {
		t.Error("Matches should accept the correct token")
	}
	if Matches(h, "wrong") {
		t.Error("Matches accepted a wrong token")
	}
	if Matches(Hash("other"), "s3cret") {
		t.Error("Matches accepted a token for a different hash")
	}
	// uppercase stored hash should still match (config normalizes anyway)
	if !Matches(strings.ToUpper(h), "s3cret") {
		t.Error("Matches should accept uppercase hash")
	}
	if Matches("not-a-hash", "s3cret") {
		t.Error("Matches accepted a malformed hash")
	}
}

func TestGenerate(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two generated tokens are identical")
	}
	if len(a) != 64 {
		t.Fatalf("token length = %d, want 64", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("token is not hex: %v", err)
	}
	if Hash(a) != Hash(a) {
		t.Error("Hash not deterministic")
	}
	if Matches(Hash(a), b) {
		t.Error("token b matched hash of token a")
	}
}

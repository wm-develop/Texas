package security

import (
	"bytes"
	"testing"
)

func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher, err := NewPasswordHasher(1_000, bytes.NewReader(make([]byte, 64)))
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if encoded == "correct horse battery staple" || !hasher.Verify("correct horse battery staple", encoded) {
		t.Fatal("password did not verify securely")
	}
	if hasher.Verify("wrong password", encoded) || hasher.Verify("correct horse battery staple", "invalid") {
		t.Fatal("invalid password or hash verified")
	}
}

func TestPasswordHasherRejectsShortPassword(t *testing.T) {
	hasher, err := NewPasswordHasher(1_000, bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	if _, err := hasher.Hash("short"); err == nil {
		t.Fatal("expected short password to fail")
	}
}

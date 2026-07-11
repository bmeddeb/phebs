package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTripAndBounds(t *testing.T) {
	const password = "correct horse battery staple"
	first, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !verifyPassword(first, password) || verifyPassword(first, "wrong password") {
		t.Fatalf("Argon2 round trip failed: equal=%v correct=%v wrong=%v",
			first == second, verifyPassword(first, password), verifyPassword(first, "wrong password"))
	}
	if _, err := hashPassword("too-short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := hashPassword(strings.Repeat("x", passwordMaxBytes+1)); err == nil {
		t.Fatal("overlong password accepted")
	}
	// A malicious/corrupt database hash cannot force unbounded Argon2 memory.
	if verifyPassword("$argon2id$v=19$m=4294967295,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U", password) {
		t.Fatal("out-of-bounds Argon2 parameters accepted")
	}
}

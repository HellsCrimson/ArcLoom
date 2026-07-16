package auth

import (
	"strings"
	"testing"
)

func TestHashVerify_RoundTrip(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := Verify(h, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("expected password to verify")
	}
	ok, err = Verify(h, "wrong password")
	if err != nil {
		t.Fatalf("Verify(wrong): %v", err)
	}
	if ok {
		t.Fatal("wrong password must not verify")
	}
}

func TestHash_RandomSalt(t *testing.T) {
	h1, _ := Hash("same")
	h2, _ := Hash("same")
	if h1 == h2 {
		t.Fatal("two hashes of the same input must differ (random salt)")
	}
}

func TestHash_NotPlaintext(t *testing.T) {
	const pw = "s3cr3t-value"
	h, _ := Hash(pw)
	if strings.Contains(h, pw) {
		t.Fatalf("encoded hash must not contain the plaintext: %q", h)
	}
	if !strings.HasPrefix(h, "argon2id$") {
		t.Fatalf("unexpected encoding: %q", h)
	}
}

func TestVerify_MalformedEncoding(t *testing.T) {
	for _, bad := range []string{"", "not-a-hash", "argon2id$v=19$bad", "bcrypt$x$y$z$w"} {
		if _, err := Verify(bad, "x"); err == nil {
			t.Errorf("expected error for malformed encoding %q", bad)
		}
	}
}

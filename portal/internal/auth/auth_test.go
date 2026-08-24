package auth

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery staple")) != nil {
		t.Fatal("hash should verify against the original password")
	}
	if CheckPassword(hash, "wrong password") {
		t.Fatal("wrong password must not verify")
	}
}

func TestRateLimiterWindow(t *testing.T) {
	rl := NewRateLimiter(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if !rl.Allow("k", 3) {
			t.Fatalf("hit %d should be allowed", i+1)
		}
	}
	if rl.Allow("k", 3) {
		t.Fatal("4th hit within window should be denied")
	}
	if !rl.Allow("other", 3) {
		t.Fatal("different key should have its own budget")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow("k", 3) {
		t.Fatal("after window expiry the key should be allowed again")
	}
}

func TestSecureEquals(t *testing.T) {
	if !SecureEquals("abc", "abc") {
		t.Fatal("equal strings must match")
	}
	if SecureEquals("abc", "abd") || SecureEquals("abc", "abcd") || SecureEquals("", "x") {
		t.Fatal("unequal strings must not match")
	}
}

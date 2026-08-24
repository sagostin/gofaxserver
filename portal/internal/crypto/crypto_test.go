package crypto

import (
	"bytes"
	"testing"
)

func TestBoxRoundTrip(t *testing.T) {
	box, err := NewBox("a-very-secret-passphrase")
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	secret := []byte("svc-password-123")

	sealed, err := box.Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(sealed, secret) {
		t.Fatal("sealed output equals plaintext")
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, secret) {
		t.Fatalf("round trip mismatch: %q", opened)
	}
}

func TestBoxWrongKeyFails(t *testing.T) {
	box1, _ := NewBox("key-one")
	box2, _ := NewBox("key-two")
	sealed, _ := box1.Seal([]byte("secret"))
	if _, err := box2.Open(sealed); err == nil {
		t.Fatal("opening with wrong key should fail")
	}
}

func TestSealOpenString(t *testing.T) {
	box, _ := NewBox("k")
	sealed, err := SealString(box, "hello")
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenString(box, sealed)
	if err != nil || got != "hello" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestRandomTokenUniqueAndSized(t *testing.T) {
	a := RandomToken(16)
	b := RandomToken(16)
	if a == b {
		t.Fatal("tokens should differ")
	}
	if len(a) != 32 { // 16 bytes hex-encoded
		t.Fatalf("unexpected token length %d", len(a))
	}
}

func TestHashTokenStable(t *testing.T) {
	if HashToken("abc") != HashToken("abc") {
		t.Fatal("hash should be deterministic")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Fatal("different inputs should hash differently")
	}
}

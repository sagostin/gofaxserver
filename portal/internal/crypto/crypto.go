package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Box seals small secrets (service-account passwords) with AES-256-GCM.
// The AES key is derived as SHA-256 of the configured secret string, so any
// sufficiently strong passphrase works without format constraints.
type Box struct {
	aead cipher.AEAD
}

func NewBox(secret string) (*Box, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	if len(sealed) < b.aead.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	ns := b.aead.NonceSize()
	return b.aead.Open(nil, sealed[:ns], sealed[ns:], nil)
}

func SealString(b *Box, s string) ([]byte, error) { return b.Seal([]byte(s)) }
func OpenString(b *Box, data []byte) (string, error) {
	pt, err := b.Open(data)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// RandomToken returns n random bytes hex-encoded.
func RandomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(buf)
}

// HashToken hashes a session token for storage/lookup.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// B64 is a helper for base64 transport encoding.
func B64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

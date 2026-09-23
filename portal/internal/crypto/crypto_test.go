// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Copyright (C) 2025-2026 Shaun Agostinho
//
// This program is free software; you can redistribute it and/or
// modify it under the terms of the GNU General Public License
// as published by the Free Software Foundation; version 2
// of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program; if not, write to the Free Software
// Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

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

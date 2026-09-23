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

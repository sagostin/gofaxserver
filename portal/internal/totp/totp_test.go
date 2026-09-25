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

package totp

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	gototp "github.com/pquerna/otp/totp"
)

func TestGenerateProducesUsableEnrollment(t *testing.T) {
	enr, err := Generate("alice@example.com")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if enr.Secret == "" {
		t.Fatal("empty secret")
	}
	if !strings.HasPrefix(enr.URL, "otpauth://totp/") {
		t.Errorf("unexpected otpauth url: %q", enr.URL)
	}
	if !strings.Contains(enr.URL, "issuer=GoFax+Portal") && !strings.Contains(enr.URL, "issuer=GoFax%20Portal") {
		t.Errorf("url missing issuer: %q", enr.URL)
	}
	if !strings.HasPrefix(enr.QRPNG, "data:image/png;base64,") {
		t.Errorf("qr not a png data url: %.40q", enr.QRPNG)
	}
}

func TestValidateAcceptsCurrentAndAdjacentCodes(t *testing.T) {
	enr, err := Generate("bob")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	now := time.Now()
	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code, err := gototp.GenerateCodeCustom(enr.Secret, now.Add(offset), gototp.ValidateOpts{
			Period:    30,
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			t.Fatalf("GenerateCodeCustom(%v): %v", offset, err)
		}
		if !Validate(code, enr.Secret) {
			t.Errorf("Validate rejected code from offset %v", offset)
		}
	}
}

func TestValidateRejectsBadCodes(t *testing.T) {
	enr, err := Generate("carol")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if Validate("", enr.Secret) {
		t.Error("accepted empty code")
	}
	if Validate("12345", enr.Secret) {
		t.Error("accepted 5-digit code")
	}
	if Validate("abcdef", enr.Secret) {
		t.Error("accepted non-numeric code")
	}
	// Code from 5 minutes ago is far outside ±1 period.
	stale, err := gototp.GenerateCodeCustom(enr.Secret, time.Now().Add(-5*time.Minute), gototp.ValidateOpts{
		Period:    30,
		Skew:      0,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	if Validate(stale, enr.Secret) {
		t.Error("accepted stale code outside skew window")
	}
	if Validate("000000", "not-a-real-secret") {
		t.Error("accepted code against invalid secret")
	}
}

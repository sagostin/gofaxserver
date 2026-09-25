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

// Package totp is a thin wrapper around github.com/pquerna/otp pinning the
// portal's TOTP policy (SHA1, 6 digits, 30s period, ±1 period skew) and
// providing the QR code used during authenticator enrollment.
package totp

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Issuer labels codes in authenticator apps.
const Issuer = "GoFax Portal"

// skewPeriods tolerates one 30-second step of clock drift on either side,
// the standard accommodation for authenticator devices.
const skewPeriods = 1

// Enrollment is everything the frontend needs to onboard an authenticator:
// the base32 secret for manual entry and a PNG QR code as a data URL.
type Enrollment struct {
	Secret string `json:"secret"`
	URL    string `json:"otpauth_url"`
	QRPNG  string `json:"qr_png"` // data:image/png;base64,...
}

// Generate creates a new TOTP secret for the account. The returned secret is
// plaintext base32 — callers seal it before storage.
func Generate(accountName string) (*Enrollment, error) {
	return build(accountName, nil)
}

// EnrollmentFromSecret rebuilds the enrollment payload (URL + QR) for an
// existing base32 secret, so the setup screen can be re-rendered without
// rotating the user's secret.
func EnrollmentFromSecret(accountName, secret string) (*Enrollment, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return nil, fmt.Errorf("decode totp secret: %w", err)
	}
	return build(accountName, raw)
}

func build(accountName string, secret []byte) (*Enrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: accountName,
		Period:      30,
		Secret:      secret,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("generate totp key: %w", err)
	}
	img, err := key.Image(256, 256)
	if err != nil {
		return nil, fmt.Errorf("render totp qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode totp qr: %w", err)
	}
	return &Enrollment{
		Secret: key.Secret(),
		URL:    key.URL(),
		QRPNG:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// Validate checks a 6-digit passcode against the base32 secret, tolerating
// ±1 period of clock skew. Empty or malformed codes fail closed.
func Validate(code, secret string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 || secret == "" {
		return false
	}
	ok, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      skewPeriods,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

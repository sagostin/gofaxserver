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

package api

import (
	"encoding/json"
	"strings"

	"gofaxportal/internal/crypto"
	"gofaxportal/internal/models"
	"gofaxportal/internal/totp"

	"github.com/kataras/iris/v12"
)

// prepareEnrollment ensures the user has a sealed TOTP secret ready for
// enrollment. An existing unconfirmed secret is reused so repeated setup
// screens render the same QR code; it is only regenerated after a completed
// enrollment or an admin reset.
func (s *Server) prepareEnrollment(user *models.PortalUser) error {
	if len(user.TOTPSecretEnc) > 0 {
		return nil
	}
	enr, err := totp.Generate(user.Username)
	if err != nil {
		return err
	}
	sealed, err := crypto.SealString(s.TOTPBox, enr.Secret)
	if err != nil {
		return err
	}
	user.TOTPSecretEnc = sealed
	user.TOTPEnabled = false
	return s.DB.Model(user).Select("TOTPSecretEnc", "TOTPEnabled").Updates(user).Error
}

type totpSetupReq struct {
	MFAToken string `json:"mfa_token"`
}

// handleTOTPSetup returns the enrollment payload (QR + manual secret) for an
// enrollment-pending login. Public, but gated by the single-purpose pending
// token minted after password verification.
func (s *Server) handleTOTPSetup(ctx iris.Context) {
	var req totpSetupReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.MFAToken) == "" {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "mfa_token required"})
		return
	}
	pa, err := s.Auth.LookupPendingAuth(strings.TrimSpace(req.MFAToken))
	if err != nil || pa.Purpose != models.PendingAuthPurposeEnroll {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid or expired 2FA session — log in again"})
		return
	}
	var user models.PortalUser
	if err := s.DB.First(&user, pa.UserID).Error; err != nil {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid or expired 2FA session — log in again"})
		return
	}
	secret, err := crypto.OpenString(s.TOTPBox, user.TOTPSecretEnc)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to unlock 2FA enrollment"})
		return
	}
	enr, err := totp.EnrollmentFromSecret(user.Username, secret)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to render 2FA enrollment"})
		return
	}
	ctx.JSON(enr)
}

type totpVerifyReq struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

// handleTOTPVerify completes an MFA login or enrollment. On success the
// pending token is consumed (single-use) and a real session is issued.
func (s *Server) handleTOTPVerify(ctx iris.Context) {
	var req totpVerifyReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.MFAToken) == "" || strings.TrimSpace(req.Code) == "" {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "mfa_token and code required"})
		return
	}
	req.MFAToken = strings.TrimSpace(req.MFAToken)

	key := ctx.RemoteAddr() + "|totp|" + req.MFAToken
	if !s.LoginLimit.Allow(key, s.Cfg.LoginRatePerMinute) {
		ctx.StatusCode(iris.StatusTooManyRequests)
		ctx.JSON(map[string]string{"error": "too many attempts, try again later"})
		return
	}

	pa, err := s.Auth.LookupPendingAuth(req.MFAToken)
	if err != nil {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid or expired 2FA session — log in again"})
		return
	}
	var user models.PortalUser
	if err := s.DB.First(&user, pa.UserID).Error; err != nil || !user.Active {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid or expired 2FA session — log in again"})
		return
	}

	secret, err := crypto.OpenString(s.TOTPBox, user.TOTPSecretEnc)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to unlock 2FA secret"})
		return
	}
	if !totp.Validate(req.Code, secret) {
		s.auditAs(ctx, &user, "AUTH_TOTP_FAILED", user.Username, nil)
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid code"})
		return
	}
	// Single-use: a presented code can never be replayed with this token.
	s.Auth.ConsumePendingAuth(pa)

	if pa.Purpose == models.PendingAuthPurposeEnroll {
		user.TOTPEnabled = true
		if err := s.DB.Model(&user).Update("totp_enabled", true).Error; err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.JSON(map[string]string{"error": "failed to finalize enrollment"})
			return
		}
		s.auditAs(ctx, &user, "AUTH_TOTP_ENROLLED", user.Username, nil)
	}
	s.issueSession(ctx, &user)
}

// issueSession mints the post-auth session, sets the cookie and writes the
// standard /auth/me payload. Shared by password-only login and TOTP verify.
func (s *Server) issueSession(ctx iris.Context, user *models.PortalUser) {
	token, csrf, expires, err := s.Auth.CreateSession(user.ID)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to create session"})
		return
	}
	setSessionCookie(ctx, s.Cfg.CookieSecure, token, expires)
	s.auditAs(ctx, user, "AUTH_LOGIN", user.Username, nil)
	s.writeMePayload(ctx, user, csrf)
}

// auditAs records an event attributed to a user who is not yet attached to
// the request context (the MFA handshake happens pre-session).
func (s *Server) auditAs(ctx iris.Context, user *models.PortalUser, action, target string, detail any) {
	entry := models.AuditLog{Action: action, Target: target, IP: ctx.RemoteAddr()}
	if user != nil {
		uid := user.ID
		entry.ActorID = &uid
		entry.ActorUsername = user.Username
	}
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			entry.Detail = string(b)
		}
	}
	_ = s.DB.Create(&entry).Error
}

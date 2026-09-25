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
	"net/http"
	"strings"
	"time"

	"gofaxportal/internal/auth"
	"gofaxportal/internal/models"

	"github.com/kataras/iris/v12"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(ctx iris.Context) {
	var req loginReq
	if err := ctx.ReadJSON(&req); err != nil || req.Username == "" || req.Password == "" {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "username and password required"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)

	key := s.clientIP(ctx) + "|" + strings.ToLower(req.Username)
	if !s.LoginLimit.Allow(key, s.Cfg.LoginRatePerMinute) {
		ctx.StatusCode(iris.StatusTooManyRequests)
		ctx.JSON(map[string]string{"error": "too many login attempts, try again later"})
		return
	}

	var user models.PortalUser
	err := s.DB.Where("username = ?", req.Username).First(&user).Error
	if err != nil || !auth.CheckPassword(user.PasswordHash, req.Password) {
		s.audit(ctx, "AUTH_LOGIN_FAILED", req.Username, nil)
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid credentials"})
		return
	}
	if !user.Active {
		s.audit(ctx, "AUTH_LOGIN_BLOCKED", req.Username, map[string]bool{"inactive_user": true})
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "account is deactivated"})
		return
	}
	if user.OrgID != nil {
		var org models.Org
		if s.DB.Select("active", "totp_required").First(&org, *user.OrgID).Error != nil || !org.Active {
			s.audit(ctx, "AUTH_LOGIN_BLOCKED", req.Username, map[string]bool{"inactive_org": true})
			ctx.StatusCode(iris.StatusForbidden)
			ctx.JSON(map[string]string{"error": "organization is inactive"})
			return
		}
		// Org-enforced TOTP: no session is issued until the second factor is
		// satisfied. Unenrolled users are forced through enrollment first.
		// When the org toggle is off, TOTP is fully bypassed for its users.
		if user.Role == models.RoleUser && org.TOTPRequired {
			if !user.TOTPEnabled {
				if err := s.prepareEnrollment(&user); err != nil {
					ctx.StatusCode(iris.StatusInternalServerError)
					ctx.JSON(map[string]string{"error": "failed to start 2FA enrollment"})
					return
				}
				pending, perr := s.Auth.CreatePendingAuth(user.ID, models.PendingAuthPurposeEnroll)
				if perr != nil {
					ctx.StatusCode(iris.StatusInternalServerError)
					ctx.JSON(map[string]string{"error": "failed to start 2FA enrollment"})
					return
				}
				s.audit(ctx, "AUTH_MFA_ENROLL_PENDING", user.Username, nil)
				ctx.JSON(map[string]string{"mfa": "enroll_required", "mfa_token": pending})
				return
			}
			pending, perr := s.Auth.CreatePendingAuth(user.ID, models.PendingAuthPurposeLogin)
			if perr != nil {
				ctx.StatusCode(iris.StatusInternalServerError)
				ctx.JSON(map[string]string{"error": "failed to start 2FA challenge"})
				return
			}
			s.audit(ctx, "AUTH_MFA_PENDING", user.Username, nil)
			ctx.JSON(map[string]string{"mfa": "totp_required", "mfa_token": pending})
			return
		}
	}

	s.issueSession(ctx, &user)
}

// setSessionCookie writes the session cookie scoped to the /portal prefix.
func setSessionCookie(ctx iris.Context, secure bool, token string, expires time.Time) {
	http.SetCookie(ctx.ResponseWriter(), &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    token,
		Path:     "/portal", // scoped: the portal lives under the /portal prefix
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

func (s *Server) handleLogout(ctx iris.Context) {
	if c, err := ctx.Request().Cookie(auth.SessionCookie); err == nil {
		_ = s.Auth.Destroy(c.Value)
	}
	http.SetCookie(ctx.ResponseWriter(), &http.Cookie{
		Name: auth.SessionCookie, Value: "", Path: "/portal", HttpOnly: true,
		Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	s.audit(ctx, "AUTH_LOGOUT", "", nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// meResponse is the shape returned by /auth/login and /auth/me.
func (s *Server) writeMePayload(ctx iris.Context, user *models.PortalUser, csrf string) {
	resp := map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"email":        user.Email,
		"role":         user.Role,
		"org_id":       user.OrgID,
		"totp_enabled": user.TOTPEnabled,
	}
	if csrf != "" {
		resp["csrf_token"] = csrf
	}
	if user.OrgID != nil {
		var org models.Org
		if err := s.DB.First(&org, *user.OrgID).Error; err == nil {
			resp["org_name"] = org.Name
		}
	}
	if user.Role == models.RoleUser && user.OrgID != nil {
		nums, _ := s.myNumbers(user.ID, *user.OrgID)
		resp["numbers"] = nums
	}
	ctx.JSON(resp)
}

func (s *Server) handleMe(ctx iris.Context) {
	sess, user, ok := currentUser(ctx)
	if !ok {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "authentication required"})
		return
	}
	s.writeMePayload(ctx, user, sess.CSRFToken)
}

// myNumbers returns the active outbound numbers assigned to the user.
func (s *Server) myNumbers(userID, orgID uint) ([]map[string]any, error) {
	rows := []struct {
		ID     uint
		Number string
		Name   string
		Header string
	}{}
	err := s.DB.Table("numbers").
		Joins("JOIN user_numbers ON user_numbers.number_id = numbers.id").
		Where("user_numbers.user_id = ? AND numbers.org_id = ? AND numbers.active = ?", userID, orgID, true).
		Order("numbers.number ASC").
		Scan(&rows).Error
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.ID, "number": r.Number, "name": r.Name, "header": r.Header})
	}
	return out, err
}

func (s *Server) handleMyNumbers(ctx iris.Context) {
	userID, orgID, ok := s.userScope(ctx)
	if !ok {
		return // response already written by middleware chain
	}
	nums, err := s.myNumbers(userID, orgID)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to load numbers"})
		return
	}
	ctx.JSON(nums)
}

// userScope extracts the authenticated user context for the user realm.
func (s *Server) userScope(ctx iris.Context) (userID, orgID uint, ok bool) {
	_, user, uok := currentUser(ctx)
	org, ook := currentOrgID(ctx)
	if !uok || !ook {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "authentication required"})
		return 0, 0, false
	}
	return user.ID, org, true
}

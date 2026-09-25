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
	"crypto/subtle"
	"fmt"
	"sync"
	"time"

	"gofaxportal/internal/crypto"
	"gofaxportal/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	SessionCookie  = "gofaxportal_session"
	sessionTTL     = 24 * time.Hour
	pendingAuthTTL = 10 * time.Minute
	bcryptCost     = 12
)

type Service struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{DB: db}
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// CreateSession mints a new session for the user and returns the bearer token
// plus the per-session CSRF token (only the SHA-256 of the token is stored).
func (s *Service) CreateSession(userID uint) (token, csrf string, expires time.Time, err error) {
	token = crypto.RandomToken(32)
	csrf = crypto.RandomToken(32)
	expires = time.Now().Add(sessionTTL)
	sess := &models.Session{TokenHash: crypto.HashToken(token), CSRFToken: csrf, UserID: userID, ExpiresAt: expires}
	if err = s.DB.Create(sess).Error; err != nil {
		return "", "", time.Time{}, err
	}
	return token, csrf, expires, nil
}

// Lookup validates a raw session token and returns the session + owning user.
// Expired sessions are treated as missing. Sessions past the halfway point of
// their lifetime are renewed (sliding expiry).
func (s *Service) Lookup(token string) (*models.Session, *models.PortalUser, error) {
	if token == "" {
		return nil, nil, fmt.Errorf("empty token")
	}
	var sess models.Session
	if err := s.DB.Where("token_hash = ?", crypto.HashToken(token)).First(&sess).Error; err != nil {
		return nil, nil, fmt.Errorf("session not found")
	}
	now := time.Now()
	if now.After(sess.ExpiresAt) {
		_ = s.DB.Delete(&sess).Error
		return nil, nil, fmt.Errorf("session expired")
	}
	var user models.PortalUser
	if err := s.DB.First(&user, sess.UserID).Error; err != nil {
		return nil, nil, fmt.Errorf("user not found")
	}
	if !user.Active {
		_ = s.DB.Delete(&sess).Error
		return nil, nil, fmt.Errorf("user deactivated")
	}
	if now.Sub(sess.CreatedAt) > sessionTTL/2 {
		sess.ExpiresAt = now.Add(sessionTTL)
		_ = s.DB.Model(&models.Session{}).Where("id = ?", sess.ID).Update("expires_at", sess.ExpiresAt).Error
	}
	return &sess, &user, nil
}

func (s *Service) Destroy(token string) error {
	return s.DB.Where("token_hash = ?", crypto.HashToken(token)).Delete(&models.Session{}).Error
}

// CreatePendingAuth mints a short-lived bridge token between password
// verification and TOTP verification during login. Only the SHA-256 of the
// token is stored.
func (s *Service) CreatePendingAuth(userID uint, purpose string) (token string, err error) {
	token = crypto.RandomToken(32)
	pa := &models.PendingAuth{
		TokenHash: crypto.HashToken(token),
		UserID:    userID,
		Purpose:   purpose,
		ExpiresAt: time.Now().Add(pendingAuthTTL),
	}
	if err = s.DB.Create(pa).Error; err != nil {
		return "", err
	}
	return token, nil
}

// LookupPendingAuth validates a raw pending-auth token; the caller checks
// Purpose. Expired tokens are treated as missing and deleted.
func (s *Service) LookupPendingAuth(token string) (*models.PendingAuth, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}
	var pa models.PendingAuth
	if err := s.DB.Where("token_hash = ?", crypto.HashToken(token)).First(&pa).Error; err != nil {
		return nil, fmt.Errorf("pending auth not found")
	}
	if time.Now().After(pa.ExpiresAt) {
		_ = s.DB.Delete(&pa).Error
		return nil, fmt.Errorf("pending auth expired")
	}
	return &pa, nil
}

// ConsumePendingAuth deletes a pending-auth token, making it single-use.
func (s *Service) ConsumePendingAuth(pa *models.PendingAuth) {
	_ = s.DB.Delete(pa).Error
}

// DestroyUserSessions revokes every active session of a user.
func (s *Service) DestroyUserSessions(userID uint) error {
	return s.DB.Where("user_id = ?", userID).Delete(&models.Session{}).Error
}

// Constant-time compare helper for CSRF tokens.
func SecureEquals(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// PurgeExpired removes expired sessions and pending-auth tokens; run periodically.
func (s *Service) PurgeExpired() {
	now := time.Now()
	s.DB.Where("expires_at < ?", now).Delete(&models.Session{})
	s.DB.Where("expires_at < ?", now).Delete(&models.PendingAuth{})
}

// StartJanitor purges expired sessions hourly until stop is closed.
func (s *Service) StartJanitor(stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.PurgeExpired()
			}
		}
	}()
}

// RateLimiter is a fixed-window in-memory limiter keyed by arbitrary strings.
type RateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	hits   map[string][]time.Time
	lastGC time.Time
}

func NewRateLimiter(window time.Duration) *RateLimiter {
	return &RateLimiter{window: window, hits: map[string][]time.Time{}, lastGC: time.Now()}
}

// Allow records a hit for key if it is under limit within the window.
func (r *RateLimiter) Allow(key string, limit int) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.lastGC) > 10*r.window {
		for k, ts := range r.hits {
			kept := ts[:0]
			for _, t := range ts {
				if now.Sub(t) < r.window {
					kept = append(kept, t)
				}
			}
			if len(kept) == 0 {
				delete(r.hits, k)
			} else {
				r.hits[k] = kept
			}
		}
		r.lastGC = now
	}
	ts := r.hits[key]
	kept := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < r.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		r.hits[key] = kept
		return false
	}
	r.hits[key] = append(kept, now)
	return true
}

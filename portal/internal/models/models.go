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

package models

import "time"

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	JobQueued  = "queued"  // submitted upstream, no attempt rows yet
	JobSending = "sending" // seen active in tracker or attempts in flight
	JobSuccess = "success"
	JobFailed  = "failed"
)

type Org struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	Name           string `gorm:"uniqueIndex;not null" json:"name"`
	GofaxTenantID  uint   `gorm:"index;not null" json:"gofax_tenant_id"`
	SvcUsername    string `gorm:"not null" json:"svc_username"`
	SvcPasswordEnc []byte `gorm:"not null" json:"-"` // AES-256-GCM sealed
	// TenantNotify holds optional custom tenant-level notify rules pushed to
	// gofaxserver verbatim (e.g. "email_full->ops@acme.tld"). Empty means the
	// portal does not manage the tenant notify field and preserves whatever
	// is set upstream.
	TenantNotify string `json:"tenant_notify"`
	// TOTPRequired forces every portal user of this org to enroll in TOTP
	// 2FA before a session is issued. When off, TOTP is fully bypassed for
	// the org's users (secrets stay stored, so re-enabling re-enforces).
	TOTPRequired bool      `gorm:"not null;default:false" json:"totp_required"`
	Active       bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type PortalUser struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Username     string `gorm:"uniqueIndex;not null" json:"username"`
	Email        string `gorm:"not null;default:''" json:"email"`
	PasswordHash string `gorm:"not null" json:"-"`
	Role         string `gorm:"not null;default:'user'" json:"role"`
	OrgID        *uint  `gorm:"index" json:"org_id"` // nil for admins
	Active       bool   `gorm:"not null;default:true" json:"active"`
	// EmailNotify controls whether this user's email is included in the
	// email_report notify list for their assigned numbers. Users with it off
	// keep portal/inbox access but receive no fax receipt emails.
	EmailNotify bool `gorm:"not null;default:true" json:"email_notify"`
	// TOTPSecretEnc is the sealed (AES-256-GCM, domain-separated key) TOTP
	// shared secret; never serialized. TOTPEnabled flips true only after the
	// user confirms enrollment with a valid code. Both are portal-local —
	// gofaxserver never sees them.
	TOTPSecretEnc []byte    `json:"-"`
	TOTPEnabled   bool      `gorm:"not null;default:false" json:"totp_enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Number mirrors one tenant_numbers row on gofaxserver for an org.
type Number struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	Number        string `gorm:"uniqueIndex;not null" json:"number"`
	Name          string `json:"name"`
	Header        string `json:"header"`
	GofaxNumberID uint   `gorm:"index;not null" json:"gofax_number_id"`
	OrgID         uint   `gorm:"index;not null" json:"org_id"`
	Active        bool   `gorm:"not null;default:true" json:"active"`
	// InboundEnabled controls whether received faxes for this number are
	// delivered into the portal inbox. When true the portal maintains a
	// "portal"-type endpoint on gofaxserver for this number.
	InboundEnabled   bool `gorm:"not null;default:true" json:"inbound_enabled"`
	PortalEndpointID uint `json:"portal_endpoint_id"` // gofaxserver endpoint id auto-provisioned for inbound delivery (0 = none)
	// CustomNotify holds operator-defined notify segments appended to the
	// derived notify string (assigned-user emails + portal status push) on
	// every upstream sync, so re-syncs never remove them.
	CustomNotify string    `json:"custom_notify"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserNumber is the per-user outbound allowlist.
type UserNumber struct {
	UserID   uint `gorm:"uniqueIndex:idx_user_number;not null" json:"user_id"`
	NumberID uint `gorm:"uniqueIndex:idx_user_number;not null" json:"number_id"`
}

type FaxJob struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	JobUUID          string     `gorm:"index;not null" json:"job_uuid"`
	OrgID            uint       `gorm:"index;not null" json:"org_id"`
	UserID           uint       `gorm:"index;not null" json:"user_id"`
	CallerNumber     string     `gorm:"not null" json:"caller_number"`
	CalleeNumber     string     `gorm:"not null" json:"callee_number"`
	OriginalFilename string     `json:"original_filename"`
	Status           string     `gorm:"index;not null;default:'queued'" json:"status"`
	Pages            int        `json:"pages"`
	Attempts         int        `json:"attempts"`
	ResultText       string     `json:"result_text"`
	LastError        string     `json:"last_error"`
	SeenActive       bool       `json:"-"` // poller: job observed in /admin/faxes
	SubmittedAt      time.Time  `json:"submitted_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// InboundFax is a fax received on one of an org's numbers, delivered by
// gofaxserver to the portal's inbound endpoint. The PDF is encrypted at
// rest (AES-256-GCM, domain-separated key derived from encryption_key);
// FileSHA256/FileBytes describe the plaintext for integrity + display.
type InboundFax struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	JobUUID      string `gorm:"uniqueIndex:idx_inbound_org_uuid;not null" json:"job_uuid"`
	OrgID        uint   `gorm:"uniqueIndex:idx_inbound_org_uuid;index;not null" json:"org_id"`
	NumberID     *uint  `gorm:"index" json:"number_id"` // nil = callee not mirrored in portal (admin-only visibility)
	CallerNumber string `json:"caller_number"`
	CallerName   string `json:"caller_name"`
	CalleeNumber string `gorm:"index" json:"callee_number"`
	Pages        int    `json:"pages"`
	// FileEnc is the sealed PDF; never serialized.
	FileEnc    []byte    `gorm:"not null" json:"-"`
	FileSHA256 string    `json:"-"`
	FileBytes  int64     `json:"file_bytes"`
	ReceivedAt time.Time `gorm:"index" json:"received_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Session struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TokenHash string    `gorm:"uniqueIndex;not null" json:"-"`
	CSRFToken string    `gorm:"not null" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	ExpiresAt time.Time `gorm:"index;not null" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	PendingAuthPurposeLogin  = "login"  // password OK, TOTP code still owed
	PendingAuthPurposeEnroll = "enroll" // password OK, TOTP enrollment still owed
)

// PendingAuth bridges password verification and TOTP verification during
// login. Only the SHA-256 of the token is stored; rows are single-use and
// expire after a few minutes.
type PendingAuth struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TokenHash string    `gorm:"uniqueIndex;not null" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Purpose   string    `gorm:"not null" json:"purpose"`
	ExpiresAt time.Time `gorm:"index;not null" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditLog struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ActorID       *uint     `json:"actor_id"`
	ActorUsername string    `json:"actor_username"`
	Action        string    `gorm:"index;not null" json:"action"`
	Target        string    `gorm:"index" json:"target"`
	Detail        string    `gorm:"type:text" json:"detail"`
	IP            string    `json:"ip"`
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

func AllModels() []any {
	return []any{&Org{}, &PortalUser{}, &Number{}, &UserNumber{}, &FaxJob{}, &InboundFax{}, &Session{}, &PendingAuth{}, &AuditLog{}}
}

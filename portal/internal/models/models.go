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
	ID             uint      `gorm:"primaryKey" json:"id"`
	Name           string    `gorm:"uniqueIndex;not null" json:"name"`
	GofaxTenantID  uint      `gorm:"index;not null" json:"gofax_tenant_id"`
	SvcUsername    string    `gorm:"not null" json:"svc_username"`
	SvcPasswordEnc []byte    `gorm:"not null" json:"-"` // AES-256-GCM sealed
	Active         bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PortalUser struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"not null;default:''" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"not null;default:'user'" json:"role"`
	OrgID        *uint     `gorm:"index" json:"org_id"` // nil for admins
	Active       bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Number mirrors one tenant_numbers row on gofaxserver for an org.
type Number struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Number        string    `gorm:"uniqueIndex;not null" json:"number"`
	Name          string    `json:"name"`
	Header        string    `json:"header"`
	GofaxNumberID uint      `gorm:"index;not null" json:"gofax_number_id"`
	OrgID         uint      `gorm:"index;not null" json:"org_id"`
	Active        bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

type Session struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TokenHash string    `gorm:"uniqueIndex;not null" json:"-"`
	CSRFToken string    `gorm:"not null" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
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
	return []any{&Org{}, &PortalUser{}, &Number{}, &UserNumber{}, &FaxJob{}, &Session{}, &AuditLog{}}
}

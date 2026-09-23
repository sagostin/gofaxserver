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
	"errors"
	"time"

	"gofaxportal/internal/auth"
	"gofaxportal/internal/models"

	"github.com/kataras/iris/v12"
	"gorm.io/gorm"
)

// statusNotifyPayload mirrors gofaxserver's PortalStatusPayload (notify
// destination type "portal"): the compact final outcome of a fax job. Unknown
// fields are ignored.
type statusNotifyPayload struct {
	UUID              string    `json:"uuid"`
	Success           bool      `json:"success"`
	AllAttemptsFailed bool      `json:"all_attempts_failed"`
	Attempts          int       `json:"attempts"`
	TransferredPages  uint      `json:"transferred_pages"`
	ResultText        string    `json:"result_text"`
	HangupCause       string    `json:"hangup_cause"`
	CallerIdNumber    string    `json:"caller_id_number"`
	CalleeNumber      string    `json:"callee_number"`
	StartTs           time.Time `json:"start_ts"`
	EndTs             time.Time `json:"end_ts"`
}

// handleStatusNotify receives a final job outcome pushed by gofaxserver's
// notify system so the portal can flip a job to success/failed immediately
// instead of waiting for the next poller tick. The poller remains the
// fallback for pushes lost to downtime or network errors.
//
// Auth mirrors inbound fax delivery: per-org service-account path plus the
// optional pre-shared X-API-Key (portal.api_key on gofaxserver must match
// inbound_api_key here). Notify is fire-and-forget upstream, so status codes
// only matter for logging — unknown jobs are 200 no-ops on purpose (inbound
// faxes and faxes submitted outside the portal also trigger the notify).
func (s *Server) handleStatusNotify(ctx iris.Context) {
	if s.Cfg.InboundAPIKey != "" && !auth.SecureEquals(ctx.GetHeader("X-API-Key"), s.Cfg.InboundAPIKey) {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "invalid api key"})
		return
	}

	svcUsername := ctx.Params().Get("svc_username")
	var org models.Org
	orgErr := s.DB.Where("svc_username = ?", svcUsername).First(&org).Error
	if orgErr != nil {
		if errors.Is(orgErr, gorm.ErrRecordNotFound) {
			ctx.StatusCode(iris.StatusNotFound)
			ctx.JSON(map[string]string{"error": "unknown service account"})
			return
		}
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "service account lookup failed"})
		return
	}
	if !org.Active {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "unknown service account"})
		return
	}

	var p statusNotifyPayload
	if err := ctx.ReadJSON(&p); err != nil || p.UUID == "" {
		ctx.StatusCode(iris.StatusUnprocessableEntity)
		ctx.JSON(map[string]string{"error": "invalid notify payload"})
		return
	}

	var job models.FaxJob
	err := s.DB.Where("org_id = ? AND job_uuid = ?", org.ID, p.UUID).First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Not a portal-submitted job (e.g. an inbound fax triggered the
			// same notify string) — nothing to update.
			ctx.JSON(map[string]any{"ok": true, "ignored": true})
			return
		}
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "job lookup failed"})
		return
	}

	// Never regress a terminal state (duplicate or late push).
	if job.Status == models.JobSuccess || job.Status == models.JobFailed {
		ctx.JSON(map[string]any{"ok": true, "duplicate": true})
		return
	}

	updates := map[string]any{
		"attempts": p.Attempts,
		"pages":    int(p.TransferredPages),
	}
	completed := p.EndTs
	if completed.IsZero() {
		completed = time.Now().UTC()
	}
	switch {
	case p.Success:
		updates["status"] = models.JobSuccess
		updates["result_text"] = p.ResultText
		updates["completed_at"] = completed.UTC()
	case p.AllAttemptsFailed:
		errText := p.ResultText
		if errText == "" {
			errText = p.HangupCause
		}
		updates["status"] = models.JobFailed
		updates["result_text"] = p.ResultText
		updates["last_error"] = errText
		updates["completed_at"] = completed.UTC()
	default:
		// Neither success nor all-attempts-failed: nothing terminal to
		// record (defensive — gofaxserver only notifies at completion).
		updates["status"] = models.JobSending
	}
	if err := s.DB.Model(&job).Updates(updates).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to update job"})
		return
	}

	s.audit(ctx, "FAX_STATUS", p.UUID, map[string]any{
		"org_id": org.ID, "svc_username": svcUsername, "status": updates["status"],
		"attempts": p.Attempts, "pages": p.TransferredPages,
	})
	ctx.JSON(map[string]any{"ok": true, "status": updates["status"]})
}

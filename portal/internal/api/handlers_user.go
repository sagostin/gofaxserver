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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gofaxportal/internal/crypto"
	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"

	"github.com/kataras/iris/v12"
)

// allowedExts/MIMEs mirror gofaxserver's own upload validation.
var allowedExts = map[string]bool{".pdf": true, ".tif": true, ".tiff": true}
var allowedMIMEs = map[string]bool{"application/pdf": true, "image/tiff": true, "image/x-tiff": true}

func sanitizeNumber(raw string) string {
	cleaned := strings.TrimSpace(raw)
	var b strings.Builder
	for _, r := range cleaned {
		if (r >= '0' && r <= '9') || r == '+' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Server) handleSendFax(ctx iris.Context) {
	userID, orgID, ok := s.userScope(ctx)
	if !ok {
		return
	}
	if !s.SendLimit.Allow("send:"+strconv.FormatUint(uint64(userID), 10), s.Cfg.SendRatePerHour) {
		s.audit(ctx, "FAX_SEND_RATELIMIT", "", nil)
		ctx.StatusCode(iris.StatusTooManyRequests)
		ctx.JSON(map[string]string{"error": "sending rate limit exceeded"})
		return
	}

	// Two sources for the document:
	//  - prepared_id: output of POST /faxes/prepare (converted, previewed,
	//    optional cover page). Sealed in the portal DB; always a normalized PDF.
	//  - file: raw one-shot upload (pdf/tiff only), the legacy path.
	var filename string
	var data []byte
	var prepared *models.PreparedFax
	if pid := strings.TrimSpace(ctx.FormValue("prepared_id")); pid != "" {
		prepared = s.loadOwnedPrepared(ctx, pid, userID)
		if prepared == nil {
			return
		}
		b, err := s.PrepBox.Open(prepared.DocEnc)
		if err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.JSON(map[string]string{"error": "failed to unseal prepared document"})
			return
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != prepared.DocSHA256 {
			s.audit(ctx, "FAX_SEND_INTEGRITY", prepared.ID, nil)
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.JSON(map[string]string{"error": "prepared document failed integrity check"})
			return
		}
		data = b
		base := strings.TrimSuffix(filepath.Base(prepared.Filename), filepath.Ext(prepared.Filename))
		filename = base + ".pdf"
	} else {
		f, d, ok := s.readUpload(ctx)
		if !ok {
			return
		}
		ext := strings.ToLower(filepath.Ext(f))
		if !allowedExts[ext] {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.JSON(map[string]string{"error": "unsupported file type (pdf, tif, tiff only — use the prepare flow for other formats)"})
			return
		}
		if ct := http.DetectContentType(d); !allowedMIMEs[ct] {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.JSON(map[string]string{"error": "unsupported content type: " + ct})
			return
		}
		filename, data = f, d
	}

	caller := sanitizeNumber(ctx.FormValue("caller_number"))
	callee := sanitizeNumber(ctx.FormValue("callee_number"))
	if caller == "" || len(sanitizeNumber(callee)) < 7 {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "valid caller_number and callee_number required"})
		return
	}

	// Enforce the per-user outbound allowlist (gofaxserver only knows tenant scope).
	nums, err := s.myNumbers(userID, orgID)
	if err != nil || len(nums) == 0 {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "no outbound numbers assigned to your account"})
		return
	}
	permitted := false
	for _, num := range nums {
		if num["number"] == caller {
			permitted = true
			break
		}
	}
	if !permitted {
		s.audit(ctx, "FAX_SEND_DENIED", caller, map[string]string{"reason": "number not assigned to user"})
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "caller_number is not assigned to your account"})
		return
	}

	var org models.Org
	if err := s.DB.First(&org, orgID).Error; err != nil || !org.Active {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "organization is inactive"})
		return
	}
	svcPass, err := crypto.OpenString(s.Box, org.SvcPasswordEnc)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to unlock organization credentials"})
		return
	}

	jobUUID, ferr := s.FX.SendFax(org.SvcUsername, svcPass, filename, data, caller, callee)
	if ferr != nil {
		status := iris.StatusInternalServerError
		var ae *fsclient.APIError
		if errors.As(ferr, &ae) && ae.Status >= 400 && ae.Status < 500 {
			status = ae.Status
		}
		s.audit(ctx, "FAX_SEND_UPSTREAM_ERROR", caller, map[string]string{"error": ferr.Error()})
		ctx.StatusCode(status)
		ctx.JSON(map[string]string{"error": "upstream send failed: " + ferr.Error()})
		return
	}

	job := &models.FaxJob{
		JobUUID:          jobUUID,
		OrgID:            orgID,
		UserID:           userID,
		CallerNumber:     caller,
		CalleeNumber:     callee,
		OriginalFilename: filepath.Base(filename),
		Status:           models.JobQueued,
		SubmittedAt:      time.Now().UTC(),
	}
	if err := s.DB.Create(job).Error; err != nil {
		// Upstream accepted the fax but the local record failed — surface loudly;
		// the receipt email still arrives via gofaxserver notify.
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "fax submitted upstream but failed to record job", "job_uuid": jobUUID})
		return
	}
	// The prepared copy has been handed off; delete it instead of letting it
	// sit sealed until the TTL sweep.
	if prepared != nil {
		s.deletePrepared(prepared.ID)
	}
	s.audit(ctx, "FAX_SEND", jobUUID, map[string]any{
		"caller": caller, "callee": callee, "filename": job.OriginalFilename, "bytes": len(data),
		"prepared": prepared != nil,
	})
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(job)
}

func (s *Server) handleListJobs(ctx iris.Context) {
	userID, _, ok := s.userScope(ctx)
	if !ok {
		return
	}
	limit := clampLimit(ctx.URLParamIntDefault("limit", 50), 200)
	offset := ctx.URLParamIntDefault("offset", 0)
	jobs := []models.FaxJob{}
	q := s.DB.Where("user_id = ?", userID).Order("submitted_at DESC").Limit(limit).Offset(offset)
	if st := ctx.URLParam("status"); st != "" {
		q = q.Where("status = ?", st)
	}
	if err := q.Find(&jobs).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to load jobs"})
		return
	}
	ctx.JSON(jobs)
}

func (s *Server) handleGetJob(ctx iris.Context) {
	userID, _, ok := s.userScope(ctx)
	if !ok {
		return
	}
	job := &models.FaxJob{}
	if err := s.DB.First(job, ctx.Params().GetUintDefault("id", 0)).Error; err != nil {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "job not found"})
		return
	}
	if job.UserID != userID {
		ctx.StatusCode(iris.StatusNotFound) // don't reveal other users' jobs
		ctx.JSON(map[string]string{"error": "job not found"})
		return
	}
	ctx.JSON(job)
}

func clampLimit(v, max int) int {
	if v <= 0 {
		return 50
	}
	if v > max {
		return max
	}
	return v
}

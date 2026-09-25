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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"gofaxportal/internal/auth"
	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"

	"github.com/kataras/iris/v12"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// inboundPayload mirrors the subset of gofaxserver's FaxJobWithFile delivery
// payload (webhook/"portal" endpoint types) that the portal needs: the FaxJob
// fields are flattened into the JSON object, file_data is the base64 PDF.
// Unknown fields are ignored.
type inboundPayload struct {
	UUID         string    `json:"uuid"`
	CallerNumber string    `json:"cidnum"`
	CallerName   string    `json:"cidname"`
	CalleeNumber string    `json:"number"`
	Pages        int       `json:"npages"`
	Ts           time.Time `json:"ts"`
	FileData     string    `json:"file_data"`
}

// handleInboundFax receives a delivered fax from gofaxserver and stores it
// encrypted at rest. Auth is the per-org service-account path plus the
// optional pre-shared X-API-Key (portal.api_key on gofaxserver must match
// inbound_api_key here). Status codes are chosen to match gofaxserver's
// retry semantics: 5xx/408/429 are retried, other 4xx fail fast.
func (s *Server) handleInboundFax(ctx iris.Context) {
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
			// Unknown service account (e.g. a backend tenant not managed by
			// the portal) — non-retriable.
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

	maxFile := s.Cfg.UploadMaxMB << 20
	// JSON overhead: base64 is ~4/3 plus the envelope; cap the body generously.
	maxBody := maxFile*4/3 + (1 << 20) + 1
	body, err := io.ReadAll(io.LimitReader(ctx.Request().Body, maxBody))
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed reading delivery payload"})
		return
	}
	// LimitReader truncates at maxBody; a full-length read means the body
	// was at (or beyond) the cap.
	if int64(len(body)) >= maxBody {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "fax too large"})
		return
	}

	var p inboundPayload
	if err := json.Unmarshal(body, &p); err != nil || p.UUID == "" || p.FileData == "" {
		ctx.StatusCode(iris.StatusUnprocessableEntity)
		ctx.JSON(map[string]string{"error": "invalid delivery payload"})
		return
	}

	pdf, err := base64.StdEncoding.DecodeString(p.FileData)
	if err != nil {
		ctx.StatusCode(iris.StatusUnprocessableEntity)
		ctx.JSON(map[string]string{"error": "invalid file_data encoding"})
		return
	}
	if int64(len(pdf)) > maxFile {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "fax too large"})
		return
	}

	sum := sha256.Sum256(pdf)
	sumHex := hex.EncodeToString(sum[:])
	if hdr := ctx.GetHeader("X-File-SHA256"); hdr != "" && hdr != sumHex {
		ctx.StatusCode(iris.StatusUnprocessableEntity)
		ctx.JSON(map[string]string{"error": "file checksum mismatch"})
		return
	}

	// Match the callee to one of the org's mirrored numbers; unmatched faxes
	// are still stored (nil NumberID) but only visible to admins.
	var numberID *uint
	var num models.Number
	if err := s.DB.Where("org_id = ? AND number = ?", org.ID, p.CalleeNumber).First(&num).Error; err == nil {
		numberID = &num.ID
	}

	if s.FaxBox == nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "inbound storage not configured"})
		return
	}
	sealed, err := s.FaxBox.Seal(pdf)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to seal fax"})
		return
	}

	receivedAt := p.Ts
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	fax := &models.InboundFax{
		JobUUID:      p.UUID,
		OrgID:        org.ID,
		NumberID:     numberID,
		CallerNumber: p.CallerNumber,
		CallerName:   p.CallerName,
		CalleeNumber: p.CalleeNumber,
		Pages:        p.Pages,
		FileEnc:      sealed,
		FileSHA256:   sumHex,
		FileBytes:    int64(len(pdf)),
		ReceivedAt:   receivedAt,
	}
	// Idempotent on (org_id, job_uuid): gofaxserver retries delivery after
	// timeouts, and a retry of an already-stored fax must not duplicate.
	res := s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "org_id"}, {Name: "job_uuid"}},
		DoNothing: true,
	}).Create(fax)
	if res.Error != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to store fax"})
		return
	}
	if res.RowsAffected == 0 {
		ctx.JSON(map[string]any{"ok": true, "duplicate": true})
		return
	}

	s.audit(ctx, "FAX_RECEIVE", p.UUID, map[string]any{
		"org_id": org.ID, "svc_username": svcUsername, "callee": p.CalleeNumber,
		"caller": p.CallerNumber, "pages": p.Pages, "bytes": len(pdf), "number_matched": numberID != nil,
	})
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(map[string]any{"ok": true, "id": fax.ID})
}

// ---------- user inbox ----------

type inboxResp struct {
	models.InboundFax
	Number     string `json:"number"`
	NumberName string `json:"number_name"`
}

// decorateInbox attaches the mirrored number/name for display.
func (s *Server) decorateInbox(faxes []models.InboundFax) []inboxResp {
	orgIDs := map[uint]bool{}
	for _, f := range faxes {
		orgIDs[f.OrgID] = true
	}
	numbers := []models.Number{}
	ids := make([]uint, 0, len(orgIDs))
	for id := range orgIDs {
		ids = append(ids, id)
	}
	byID := map[uint]models.Number{}
	if len(ids) > 0 {
		s.DB.Where("org_id IN ?", ids).Find(&numbers)
		for _, n := range numbers {
			byID[n.ID] = n
		}
	}
	out := make([]inboxResp, 0, len(faxes))
	for _, f := range faxes {
		r := inboxResp{InboundFax: f}
		if f.NumberID != nil {
			if n, ok := byID[*f.NumberID]; ok {
				r.Number = n.Number
				r.NumberName = n.Name
			}
		}
		out = append(out, r)
	}
	return out
}

// userCanSeeInbound enforces per-user visibility: a fax is visible when its
// number is on the user's assignment list (same list as outbound sending).
func (s *Server) userCanSeeInbound(userID, orgID uint, fax *models.InboundFax) bool {
	if fax.OrgID != orgID || fax.NumberID == nil {
		return false
	}
	var cnt int64
	s.DB.Model(&models.UserNumber{}).Where("user_id = ? AND number_id = ?", userID, *fax.NumberID).Count(&cnt)
	return cnt > 0
}

func (s *Server) handleListInbox(ctx iris.Context) {
	userID, orgID, ok := s.userScope(ctx)
	if !ok {
		return
	}
	numberIDs := []uint{}
	s.DB.Model(&models.UserNumber{}).Where("user_id = ?", userID).Pluck("number_id", &numberIDs)
	if len(numberIDs) == 0 {
		ctx.JSON([]inboxResp{})
		return
	}
	limit := clampLimit(ctx.URLParamIntDefault("limit", 50), 200)
	offset := ctx.URLParamIntDefault("offset", 0)
	faxes := []models.InboundFax{}
	q := s.DB.Select("id", "job_uuid", "org_id", "number_id", "caller_number", "caller_name",
		"callee_number", "pages", "file_sha256", "file_bytes", "received_at", "created_at", "updated_at").
		Where("org_id = ? AND number_id IN ?", orgID, numberIDs).
		Order("received_at DESC").Limit(limit).Offset(offset)
	if err := q.Find(&faxes).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to load inbox"})
		return
	}
	ctx.JSON(s.decorateInbox(faxes))
}

func (s *Server) handleGetInbound(ctx iris.Context) {
	userID, orgID, ok := s.userScope(ctx)
	if !ok {
		return
	}
	fax := &models.InboundFax{}
	if err := s.DB.Select("id", "job_uuid", "org_id", "number_id", "caller_number", "caller_name",
		"callee_number", "pages", "file_sha256", "file_bytes", "received_at", "created_at", "updated_at").
		First(fax, ctx.Params().GetUintDefault("id", 0)).Error; err != nil || !s.userCanSeeInbound(userID, orgID, fax) {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "fax not found"})
		return
	}
	ctx.JSON(s.decorateInbox([]models.InboundFax{*fax})[0])
}

// serveInboundFile decrypts and streams the stored PDF.
func (s *Server) serveInboundFile(ctx iris.Context, fax *models.InboundFax) {
	if s.FaxBox == nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "inbound storage not configured"})
		return
	}
	pdf, err := s.FaxBox.Open(fax.FileEnc)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to decrypt fax"})
		return
	}
	sum := sha256.Sum256(pdf)
	if hex.EncodeToString(sum[:]) != fax.FileSHA256 {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "stored fax failed integrity check"})
		return
	}
	ctx.Header("Content-Type", "application/pdf")
	ctx.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"fax-%d.pdf\"", fax.ID))
	ctx.Header("X-Content-Type-Options", "nosniff")
	_, _ = ctx.Write(pdf)
}

func (s *Server) handleGetInboundFile(ctx iris.Context) {
	userID, orgID, ok := s.userScope(ctx)
	if !ok {
		return
	}
	fax := &models.InboundFax{}
	if err := s.DB.First(fax, ctx.Params().GetUintDefault("id", 0)).Error; err != nil || !s.userCanSeeInbound(userID, orgID, fax) {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "fax not found"})
		return
	}
	s.serveInboundFile(ctx, fax)
}

// ---------- admin inbox ----------

// adminInboxList is the paginated admin inbox envelope. Metadata only:
// admins can list and delete received faxes (audited) but can NEVER read the
// decrypted PDF — that surface is user-realm only (PHI minimum-necessary).
type adminInboxList struct {
	Total int64       `json:"total"`
	Items []inboxResp `json:"items"`
}

func (s *Server) adminListInbound(ctx iris.Context) {
	limit := clampLimit(ctx.URLParamIntDefault("limit", 50), 200)
	offset := ctx.URLParamIntDefault("offset", 0)

	base := s.DB.Model(&models.InboundFax{})
	if oid := ctx.URLParamIntDefault("org_id", 0); oid > 0 {
		base = base.Where("org_id = ?", oid)
	}
	if ju := strings.TrimSpace(ctx.URLParam("uuid")); ju != "" {
		base = base.Where("job_uuid = ?", ju)
	}
	if n := strings.TrimSpace(ctx.URLParam("number")); n != "" {
		like := "%" + n + "%"
		base = base.Where("caller_number LIKE ? OR callee_number LIKE ?", like, like)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to count inbound faxes"})
		return
	}

	faxes := []models.InboundFax{}
	q := base.Select("id", "job_uuid", "org_id", "number_id", "caller_number", "caller_name",
		"callee_number", "pages", "file_sha256", "file_bytes", "received_at", "created_at", "updated_at").
		Order("received_at DESC").Limit(limit).Offset(offset)
	if err := q.Find(&faxes).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to load inbound faxes"})
		return
	}
	ctx.JSON(adminInboxList{Total: total, Items: s.decorateInbox(faxes)})
}

// adminInboundAttempts returns the upstream call-attempt metadata for one
// received fax (correlated by job UUID) — result type, success, pages,
// hangup cause, T.38 status. Metadata only; no fax content.
func (s *Server) adminInboundAttempts(ctx iris.Context) {
	fax := &models.InboundFax{}
	if err := s.DB.Select("id", "job_uuid").First(fax, ctx.Params().GetUintDefault("id", 0)).Error; err != nil {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "fax not found"})
		return
	}
	out, err := s.FX.ListFaxResults(url.Values{"job_uuid": {fax.JobUUID}})
	if err != nil {
		ctx.StatusCode(iris.StatusBadGateway)
		ctx.JSON(map[string]string{"error": "failed to fetch upstream attempts: " + err.Error()})
		return
	}
	// Upstream returns jobs grouped by job UUID; the UI wants the flat leg
	// (attempt) list of this one job.
	legs := []fsclient.FaxResultRow{}
	if len(out.Items) > 0 {
		legs = out.Items[0].Legs
	}
	ctx.JSON(legs)
}

func (s *Server) adminDeleteInbound(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	fax := &models.InboundFax{}
	if err := s.DB.First(fax, id).Error; err != nil {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "fax not found"})
		return
	}
	if err := s.DB.Delete(fax).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to delete fax"})
		return
	}
	s.audit(ctx, "INBOUND_DELETE", fax.JobUUID, map[string]any{"org_id": fax.OrgID, "callee": fax.CalleeNumber})
	ctx.JSON(map[string]bool{"ok": true})
}

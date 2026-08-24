package api

import (
	"errors"
	"io"
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

	file, fh, err := ctx.FormFile("file")
	if err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "file field required"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowedExts[ext] {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "unsupported file type (pdf, tif, tiff only)"})
		return
	}
	maxBytes := s.Cfg.UploadMaxMB << 20
	if fh.Size > maxBytes {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "file too large"})
		return
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	ct := http.DetectContentType(head[:n])
	if n > 0 && !allowedMIMEs[ct] {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "unsupported content type: " + ct})
		return
	}
	data := make([]byte, 0, fh.Size)
	data = append(data, head[:n]...)
	extra, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed reading upload"})
		return
	}
	data = append(data, extra...)
	if int64(len(data)) > maxBytes {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "file too large"})
		return
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

	jobUUID, ferr := s.FX.SendFax(org.SvcUsername, svcPass, fh.Filename, data, caller, callee)
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
		OriginalFilename: filepath.Base(fh.Filename),
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
	s.audit(ctx, "FAX_SEND", jobUUID, map[string]any{
		"caller": caller, "callee": callee, "filename": job.OriginalFilename, "bytes": len(data),
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

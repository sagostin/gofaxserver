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
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"gofaxportal/internal/convert"

	"github.com/kataras/iris/v12"
)

// prepareMIMEs maps each allowed extension to the content types
// http.DetectContentType may legitimately return for it. Stricter than the
// raw-send path: the extension and the sniffed bytes must agree.
var prepareMIMEs = map[string][]string{
	".pdf":  {"application/pdf"},
	".tif":  {"image/tiff", "image/x-tiff"},
	".tiff": {"image/tiff", "image/x-tiff"},
	".png":  {"image/png"},
	".jpg":  {"image/jpeg"},
	".jpeg": {"image/jpeg"},
	// docx is a ZIP container; doc is OLE2 compound storage.
	".docx": {"application/zip"},
	".doc":  {"application/x-ole-storage", "application/msword"},
}

func mimeAllowed(ext, sniffed string) bool {
	for _, m := range prepareMIMEs[ext] {
		if m == sniffed {
			return true
		}
	}
	return false
}

// readUpload streams the multipart file into memory with the configured cap.
// Shared by the prepare flow and the raw send flow.
func (s *Server) readUpload(ctx iris.Context) (filename string, data []byte, ok bool) {
	file, fh, err := ctx.FormFile("file")
	if err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "file field required"})
		return "", nil, false
	}
	defer file.Close()

	maxBytes := s.Cfg.UploadMaxMB << 20
	if fh.Size > maxBytes {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "file too large"})
		return "", nil, false
	}
	data, err = io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed reading upload"})
		return "", nil, false
	}
	if int64(len(data)) > maxBytes {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "file too large"})
		return "", nil, false
	}
	if len(data) == 0 {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "empty file"})
		return "", nil, false
	}
	return fh.Filename, data, true
}

// handlePrepareFax converts an uploaded document (docx/doc/png/jpeg/pdf/tiff)
// to a fax-ready PDF with optional cover page, and returns preview URLs.
// Two-step flow: the user reviews the previews, then POSTs /faxes with the
// prepared_id.
func (s *Server) handlePrepareFax(ctx iris.Context) {
	userID, orgID, ok := s.userScope(ctx)
	if !ok {
		return
	}
	if !s.Cfg.Converter.Enabled {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "conversion is disabled"})
		return
	}
	if !s.PrepareLimit.Allow("prepare:"+strconv.FormatUint(uint64(userID), 10), s.Cfg.PrepareRatePerHour) {
		s.audit(ctx, "FAX_PREPARE_RATELIMIT", "", nil)
		ctx.StatusCode(iris.StatusTooManyRequests)
		ctx.JSON(map[string]string{"error": "prepare rate limit exceeded"})
		return
	}

	filename, data, ok := s.readUpload(ctx)
	if !ok {
		return
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !convert.AllowedExts[ext] {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "unsupported file type (pdf, tif, tiff, png, jpg, docx, doc)"})
		return
	}
	if sn := http.DetectContentType(data); !mimeAllowed(ext, sn) {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "file content does not match its extension (" + sn + ")"})
		return
	}

	fitMode, err := convert.ParseFitMode(ctx.FormValue("fit_mode"), convert.FitMode(s.Cfg.Converter.DefaultFitMode))
	if err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": err.Error()})
		return
	}

	var cover *convert.CoverFields
	if ce := ctx.FormValue("cover_enabled"); ce == "true" || ce == "1" || ce == "on" {
		cover = &convert.CoverFields{
			To:       ctx.FormValue("cover_to"),
			From:     ctx.FormValue("cover_from"),
			Subject:  ctx.FormValue("cover_subject"),
			Comments: ctx.FormValue("cover_comments"),
		}
		if cover.Empty() {
			cover = nil // toggled on but nothing filled in: skip silently
		}
	}

	doc, err := s.Converter.Prepare(ctx.Request().Context(), filename, data, userID, orgID, convert.Options{
		FitMode: fitMode,
		Cover:   cover,
	})
	if err != nil {
		status := iris.StatusInternalServerError
		msg := "conversion failed: " + err.Error()
		switch {
		case errors.Is(err, convert.ErrUnsupportedType):
			status = iris.StatusBadRequest
		case errors.Is(err, convert.ErrTooManyPages):
			status = iris.StatusRequestEntityTooLarge
			msg = err.Error()
		}
		s.audit(ctx, "FAX_PREPARE_ERROR", filepath.Base(filename), map[string]string{"error": err.Error()})
		ctx.StatusCode(status)
		ctx.JSON(map[string]string{"error": msg})
		return
	}

	s.audit(ctx, "FAX_PREPARE", doc.ID, map[string]any{
		"filename": doc.Filename, "pages": doc.Pages, "cover": doc.Cover, "fit_mode": doc.FitMode,
	})

	previews := make([]string, 0, doc.PreviewPages)
	for i := 1; i <= doc.PreviewPages; i++ {
		previews = append(previews, "/faxes/prepare/"+doc.ID+"/preview/"+strconv.Itoa(i))
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(map[string]any{
		"id":         doc.ID,
		"filename":   doc.Filename,
		"pages":      doc.Pages,
		"cover":      doc.Cover,
		"fit_mode":   doc.FitMode,
		"previews":   previews,
		"expires_at": doc.ExpiresAt,
	})
}

// loadOwnedPrepared loads a prepared doc and enforces ownership. Writes the
// error response and returns nil on failure.
func (s *Server) loadOwnedPrepared(ctx iris.Context, id string, userID uint) *convert.PreparedDoc {
	doc, err := s.Converter.Load(id)
	if err != nil {
		ctx.StatusCode(iris.StatusGone)
		ctx.JSON(map[string]string{"error": "prepared document not found or expired"})
		return nil
	}
	if doc.UserID != userID {
		// Not the owner's document — same response as not-found: no oracle.
		ctx.StatusCode(iris.StatusGone)
		ctx.JSON(map[string]string{"error": "prepared document not found or expired"})
		return nil
	}
	return doc
}

// handleGetPreparedPreview streams one B&W preview page PNG.
func (s *Server) handleGetPreparedPreview(ctx iris.Context) {
	userID, _, ok := s.userScope(ctx)
	if !ok {
		return
	}
	doc := s.loadOwnedPrepared(ctx, ctx.Params().Get("id"), userID)
	if doc == nil {
		return
	}
	page, err := ctx.Params().GetInt("page")
	if err != nil || page < 1 || page > doc.PreviewPages {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "invalid page"})
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Type", "image/png")
	ctx.ServeFile(doc.PreviewPath(page))
}

// handleDeletePrepared discards a prepared document (user bailed out).
func (s *Server) handleDeletePrepared(ctx iris.Context) {
	userID, _, ok := s.userScope(ctx)
	if !ok {
		return
	}
	doc := s.loadOwnedPrepared(ctx, ctx.Params().Get("id"), userID)
	if doc == nil {
		return
	}
	_ = s.Converter.Delete(doc.ID)
	s.audit(ctx, "FAX_PREPARE_DISCARD", doc.ID, nil)
	ctx.StatusCode(iris.StatusNoContent)
}

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
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gofaxportal/internal/models"

	"github.com/kataras/iris/v12"
)

const (
	brandingSingletonID   = 1
	defaultPortalName     = "Fax Portal"
	brandingMaxImageBytes = 1 << 20 // 1 MiB — logos/favicons should be tiny
)

var accentColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Raster-only allowlists: SVG is deliberately excluded because a publicly
// served SVG can carry script; <img>-embedded raster formats cannot.
var logoMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

var faviconMIMEs = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/gif":                true,
	"image/webp":               true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

// loadBranding returns the singleton branding row, falling back to built-in
// defaults when the row is missing or the database is unavailable (the public
// branding endpoints must never error the login page).
func (s *Server) loadBranding() models.Branding {
	b := models.Branding{ID: brandingSingletonID, PortalName: defaultPortalName}
	if s.DB == nil {
		return b
	}
	if err := s.DB.First(&b, brandingSingletonID).Error; err != nil {
		return models.Branding{ID: brandingSingletonID, PortalName: defaultPortalName}
	}
	return b
}

// ---------- public ----------

// handleGetBranding serves the public branding descriptor. Unauthenticated by
// design: the login page renders the portal name/logo before any session
// exists. The "v" field is an asset cache-buster derived from UpdatedAt.
func (s *Server) handleGetBranding(ctx iris.Context) {
	b := s.loadBranding()
	ctx.JSON(map[string]any{
		"name":         b.PortalName,
		"accent_color": b.AccentColor,
		"has_logo":     len(b.Logo) > 0,
		"has_favicon":  len(b.Favicon) > 0,
		"v":            b.UpdatedAt.Unix(),
	})
}

// serveBrandingImage streams a stored image blob with its recorded MIME type.
func serveBrandingImage(ctx iris.Context, data []byte, mime string, updated time.Time) {
	if len(data) == 0 || mime == "" {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "no image set"})
		return
	}
	h := ctx.ResponseWriter().Header()
	h.Set("Content-Type", mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("ETag", strconv.FormatInt(updated.Unix(), 10))
	ctx.StatusCode(iris.StatusOK)
	_, _ = ctx.Write(data)
}

func (s *Server) handleGetBrandingLogo(ctx iris.Context) {
	b := s.loadBranding()
	serveBrandingImage(ctx, b.Logo, b.LogoMIME, b.UpdatedAt)
}

func (s *Server) handleGetBrandingFavicon(ctx iris.Context) {
	b := s.loadBranding()
	serveBrandingImage(ctx, b.Favicon, b.FaviconMIME, b.UpdatedAt)
}

// ---------- admin ----------

type updateBrandingReq struct {
	Name        *string `json:"name"`
	AccentColor *string `json:"accent_color"`
}

func (s *Server) adminUpdateBranding(ctx iris.Context) {
	var req updateBrandingReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "invalid request body"})
		return
	}
	b := s.loadBranding()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 80 {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.JSON(map[string]string{"error": "name must be 1-80 characters"})
			return
		}
		b.PortalName = name
	}
	if req.AccentColor != nil {
		c := strings.TrimSpace(*req.AccentColor)
		if c != "" && !accentColorRe.MatchString(c) {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.JSON(map[string]string{"error": "accent_color must be #rrggbb or empty"})
			return
		}
		b.AccentColor = strings.ToLower(c)
	}
	if err := s.DB.Save(&b).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to save branding"})
		return
	}
	s.audit(ctx, "BRANDING_UPDATE", "", map[string]string{"name": b.PortalName, "accent_color": b.AccentColor})
	ctx.JSON(map[string]any{
		"name":         b.PortalName,
		"accent_color": b.AccentColor,
		"has_logo":     len(b.Logo) > 0,
		"has_favicon":  len(b.Favicon) > 0,
		"v":            b.UpdatedAt.Unix(),
	})
}

// uploadBrandingImage reads a multipart "file" field, enforces the size cap
// and a sniffed-MIME allowlist, and returns the bytes + detected type.
func uploadBrandingImage(ctx iris.Context, allowed map[string]bool) ([]byte, string, bool) {
	file, fh, err := ctx.FormFile("file")
	if err != nil {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "file field required"})
		return nil, "", false
	}
	defer file.Close()
	if fh.Size > brandingMaxImageBytes {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "image too large (max 1 MiB)"})
		return nil, "", false
	}
	data, err := io.ReadAll(io.LimitReader(file, brandingMaxImageBytes+1))
	if err != nil || int64(len(data)) > brandingMaxImageBytes {
		ctx.StatusCode(iris.StatusRequestEntityTooLarge)
		ctx.JSON(map[string]string{"error": "image too large (max 1 MiB)"})
		return nil, "", false
	}
	ct := http.DetectContentType(data)
	if !allowed[ct] {
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.JSON(map[string]string{"error": "unsupported image type: " + ct})
		return nil, "", false
	}
	return data, ct, true
}

func (s *Server) adminUploadBrandingLogo(ctx iris.Context) {
	data, ct, ok := uploadBrandingImage(ctx, logoMIMEs)
	if !ok {
		return
	}
	b := s.loadBranding()
	b.Logo, b.LogoMIME = data, ct
	if err := s.DB.Save(&b).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to save logo"})
		return
	}
	s.audit(ctx, "BRANDING_LOGO_SET", "", map[string]any{"bytes": len(data), "mime": ct})
	ctx.JSON(map[string]any{"ok": true, "v": b.UpdatedAt.Unix()})
}

func (s *Server) adminUploadBrandingFavicon(ctx iris.Context) {
	data, ct, ok := uploadBrandingImage(ctx, faviconMIMEs)
	if !ok {
		return
	}
	b := s.loadBranding()
	b.Favicon, b.FaviconMIME = data, ct
	if err := s.DB.Save(&b).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to save favicon"})
		return
	}
	s.audit(ctx, "BRANDING_FAVICON_SET", "", map[string]any{"bytes": len(data), "mime": ct})
	ctx.JSON(map[string]any{"ok": true, "v": b.UpdatedAt.Unix()})
}

func (s *Server) adminDeleteBrandingLogo(ctx iris.Context) {
	b := s.loadBranding()
	b.Logo, b.LogoMIME = nil, ""
	if err := s.DB.Save(&b).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to clear logo"})
		return
	}
	s.audit(ctx, "BRANDING_LOGO_CLEAR", "", nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminDeleteBrandingFavicon(ctx iris.Context) {
	b := s.loadBranding()
	b.Favicon, b.FaviconMIME = nil, ""
	if err := s.DB.Save(&b).Error; err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.JSON(map[string]string{"error": "failed to clear favicon"})
		return
	}
	s.audit(ctx, "BRANDING_FAVICON_CLEAR", "", nil)
	ctx.JSON(map[string]bool{"ok": true})
}

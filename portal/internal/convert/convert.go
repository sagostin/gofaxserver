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

// Package convert normalizes user uploads (docx/doc/png/jpeg/pdf/tiff) into a
// single fax-ready PDF, entirely portal-side: gofaxserver's own pipeline is
// untouched and keeps receiving only PDF/TIFF as before.
//
// Engines per input type:
//   - png/jpeg: pure-Go rasterization onto a Letter page with a fit mode
//     (constrain/fill/stretch), embedded via fpdf.
//   - docx/doc: a Gotenberg sidecar (LibreOffice) over loopback HTTP.
//   - tiff: ImageMagick (handles multi-page CCITT fax TIFFs natively).
//   - pdf: pass-through (validated + page-counted only).
//
// An optional generated cover page is prepended (pdfcpu merge), and
// Ghostscript renders fax-accurate B&W previews (204x196 dpi mono — the same
// geometry gofaxserver uses for its tiffg3 conversion, so what the user sees
// is what the receiving fax machine prints).
package convert

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// FitMode controls how an image is placed on the fax page.
type FitMode string

const (
	// FitConstrain scales preserving aspect ratio to fit inside the page,
	// letterboxing the remainder (default — nothing cropped or distorted).
	FitConstrain FitMode = "constrain"
	// FitFill scales preserving aspect ratio to cover the whole page,
	// cropping the overflow.
	FitFill FitMode = "fill"
	// FitStretch distorts the image to exactly fill the page.
	FitStretch FitMode = "stretch"
)

// ParseFitMode validates a user-supplied fit mode, returning def for empty.
func ParseFitMode(s string, def FitMode) (FitMode, error) {
	if s == "" {
		return def, nil
	}
	switch FitMode(strings.ToLower(strings.TrimSpace(s))) {
	case FitConstrain:
		return FitConstrain, nil
	case FitFill:
		return FitFill, nil
	case FitStretch:
		return FitStretch, nil
	}
	return "", fmt.Errorf("invalid fit mode %q (constrain, fill, stretch)", s)
}

// CoverFields are the user-fillable parts of the generated cover page.
type CoverFields struct {
	To       string
	From     string
	Subject  string
	Comments string
}

// Empty reports whether no cover content was provided.
func (c CoverFields) Empty() bool {
	return c.To == "" && c.From == "" && c.Subject == "" && c.Comments == ""
}

// maxCoverFieldLen caps each cover field so the layout can't be blown out.
const maxCoverFieldLen = 500

// Sanitize trims and length-caps all fields.
func (c CoverFields) Sanitize() CoverFields {
	clip := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > maxCoverFieldLen {
			s = s[:maxCoverFieldLen]
		}
		return s
	}
	return CoverFields{To: clip(c.To), From: clip(c.From), Subject: clip(c.Subject), Comments: clip(c.Comments)}
}

// Options control a single Prepare run.
type Options struct {
	FitMode FitMode
	Cover   *CoverFields // nil = no cover page
}

// Config mirrors the portal config's converter section. Kept local so the
// package has no dependency on the portal's config package.
type Config struct {
	Enabled        bool
	GotenbergURL   string
	GhostscriptBin string
	ImageMagickBin string
	TempDir        string
	PrepareTTL     time.Duration
	MaxPages       int
	DefaultFitMode FitMode
}

// ApplyDefaults fills zero values; called by the portal config loader.
func (c *Config) ApplyDefaults() {
	if c.GotenbergURL == "" {
		c.GotenbergURL = "http://127.0.0.1:3200"
	}
	if c.GhostscriptBin == "" {
		c.GhostscriptBin = "gs"
	}
	if c.ImageMagickBin == "" {
		c.ImageMagickBin = "magick"
	}
	if c.TempDir == "" {
		c.TempDir = filepath.Join(osTempDir(), "gofaxportal-convert")
	}
	if c.PrepareTTL <= 0 {
		c.PrepareTTL = time.Hour
	}
	if c.MaxPages <= 0 {
		c.MaxPages = 50
	}
	if c.DefaultFitMode == "" {
		c.DefaultFitMode = FitConstrain
	}
}

// Converter prepares uploaded documents for faxing.
type Converter struct {
	cfg  Config
	http *http.Client
	now  func() time.Time // injectable for tests
}

func New(cfg Config) *Converter {
	cfg.ApplyDefaults()
	return &Converter{
		cfg:  cfg,
		http: &http.Client{Timeout: 120 * time.Second}, // LibreOffice on big docs can be slow
		now:  time.Now,
	}
}

// Cfg exposes the (defaulted) config for callers that need e.g. TempDir.
func (c *Converter) Cfg() Config { return c.cfg }

// ErrTooManyPages is returned when a document exceeds Config.MaxPages.
var ErrTooManyPages = errors.New("document has too many pages")

// ErrUnsupportedType is returned for extensions outside the allowlist.
var ErrUnsupportedType = errors.New("unsupported file type")

// AllowedExts is the prepare-flow allowlist (superset of the raw-send one).
var AllowedExts = map[string]bool{
	".pdf": true, ".tif": true, ".tiff": true,
	".png": true, ".jpg": true, ".jpeg": true,
	".docx": true, ".doc": true,
}

// Prepare converts data (named filename, used for its extension) into a
// normalized PDF plus fax-accurate previews, stores it, and returns the
// prepared document descriptor.
func (c *Converter) Prepare(ctx context.Context, filename string, data []byte, userID, orgID uint, opts Options) (*PreparedDoc, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if !AllowedExts[ext] {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedType, ext)
	}
	if opts.FitMode == "" {
		opts.FitMode = c.cfg.DefaultFitMode
	}

	doc := newPreparedDoc(c.cfg.TempDir, filename, userID, orgID, c.now(), c.cfg.PrepareTTL)
	if err := doc.initDir(); err != nil {
		return nil, err
	}
	// Any failure after this point must not leave half-written dirs behind.
	ok := false
	defer func() {
		if !ok {
			_ = c.Delete(doc.ID)
		}
	}()

	pdfPath := doc.PDFPath()
	var err error
	switch ext {
	case ".pdf":
		err = writeFileAtomic(pdfPath, data)
	case ".tif", ".tiff":
		err = c.tiffToPDF(ctx, data, pdfPath)
	case ".png", ".jpg", ".jpeg":
		err = c.imageToPDF(data, pdfPath, opts.FitMode)
	case ".docx", ".doc":
		err = c.officeToPDF(ctx, filename, data, pdfPath)
	}
	if err != nil {
		return nil, err
	}

	// Cover page: generate, prepend, re-count.
	if opts.Cover != nil && !opts.Cover.Empty() {
		fields := opts.Cover.Sanitize()
		bodyPages, cerr := pdfPageCount(pdfPath)
		if cerr != nil {
			return nil, fmt.Errorf("count pages: %w", cerr)
		}
		coverPath := filepath.Join(doc.Dir(), "cover.pdf")
		if cerr := renderCoverPage(coverPath, fields, bodyPages); cerr != nil {
			return nil, fmt.Errorf("render cover page: %w", cerr)
		}
		if cerr := mergePDFs([]string{coverPath, pdfPath}, pdfPath+".merged"); cerr != nil {
			return nil, fmt.Errorf("merge cover page: %w", cerr)
		}
		if cerr := replaceFile(pdfPath+".merged", pdfPath); cerr != nil {
			return nil, fmt.Errorf("merge cover page: %w", cerr)
		}
		doc.Cover = true
	}

	pages, err := pdfPageCount(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("count pages: %w", err)
	}
	if pages > c.cfg.MaxPages {
		return nil, fmt.Errorf("%w (%d > %d)", ErrTooManyPages, pages, c.cfg.MaxPages)
	}
	doc.Pages = pages

	// Fax-accurate B&W previews. Non-fatal: a missing gs binary must not
	// block sending — the response simply carries no preview URLs.
	if n, perr := c.renderPreviews(ctx, pdfPath, doc.Dir(), pages); perr != nil {
		log.Printf("[convert] preview render failed for %s: %v", doc.ID, perr)
	} else {
		doc.PreviewPages = n
	}

	if err := doc.saveMeta(); err != nil {
		return nil, err
	}
	ok = true
	return doc, nil
}

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

package convert

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFitMode(t *testing.T) {
	if got, err := ParseFitMode("", FitConstrain); err != nil || got != FitConstrain {
		t.Errorf("empty should return default, got %q err %v", got, err)
	}
	for _, m := range []FitMode{FitConstrain, FitFill, FitStretch} {
		if got, err := ParseFitMode(string(m), FitStretch); err != nil || got != m {
			t.Errorf("ParseFitMode(%q) = %q, %v", m, got, err)
		}
	}
	if _, err := ParseFitMode("squish", FitConstrain); err == nil {
		t.Error("invalid mode should error")
	}
}

func TestComputePlacement(t *testing.T) {
	// Stretch always fills the page exactly.
	if p := computePlacement(FitStretch, 100, 100); p.x != 0 || p.y != 0 || p.w != pageWPt || p.h != pageHPt {
		t.Errorf("stretch placement wrong: %+v", p)
	}
	// Constrain: a wide image (2000x500) fits by width, letterboxes vertically.
	p := computePlacement(FitConstrain, 2000, 500)
	if abs(p.w-pageWPt) > 0.001 {
		t.Errorf("constrain wide image should span full width, got %v", p.w)
	}
	wantH := pageWPt * 500 / 2000
	if abs(p.h-wantH) > 0.001 || abs(p.y-(pageHPt-wantH)/2) > 0.001 || abs(p.x) > 0.001 {
		t.Errorf("constrain placement wrong: %+v (want h=%v)", p, wantH)
	}
	// Constrain: a tall image fits by height.
	p = computePlacement(FitConstrain, 500, 2000)
	if p.h != pageHPt {
		t.Errorf("constrain tall image should span full height, got %v", p.h)
	}
	// Aspect ratio preserved.
	if abs(p.w/float64(500)-p.h/float64(2000)) > 0.001 {
		t.Errorf("constrain broke aspect ratio: %+v", p)
	}
	// Fill: wide image covers full height, overflows width (post-crop path
	// normally prevents this, but placement must still cover).
	p = computePlacement(FitFill, 2000, 500)
	if p.h != pageHPt || p.w < pageWPt {
		t.Errorf("fill should cover the page: %+v", p)
	}
}

func TestCropForFill(t *testing.T) {
	// Wide source: sides cropped, height kept.
	r := cropForFill(2000, 500)
	if r.Dy() != 500 {
		t.Errorf("wide crop should keep height, got %v", r)
	}
	// Cropped aspect ≈ page aspect.
	got := float64(r.Dx()) / float64(r.Dy())
	if abs(got-pageWPt/pageHPt) > 0.01 {
		t.Errorf("crop aspect %v != page aspect %v", got, pageWPt/pageHPt)
	}
	// Tall source: top/bottom cropped.
	r = cropForFill(500, 2000)
	if r.Dx() != 500 {
		t.Errorf("tall crop should keep width, got %v", r)
	}
	// Exact aspect: no crop.
	r = cropForFill(612, 792)
	if r != image.Rect(0, 0, 612, 792) {
		t.Errorf("exact aspect should not crop, got %v", r)
	}
}

func TestTargetRasterSize(t *testing.T) {
	// Full-page image lands at fax resolution.
	w, h := targetRasterSize(FitStretch, 4000, 4000)
	if w != pageWPx || h != pageHPx {
		t.Errorf("stretch target = %dx%d, want %dx%d", w, h, pageWPx, pageHPx)
	}
	// Small image is not upscaled by targetRasterSize's caller logic, but the
	// function itself maps points→px: constrain of 2000x500 → h=153pt → px.
	w, h = targetRasterSize(FitConstrain, 2000, 500)
	wantHpt := pageWPt * 500.0 / 2000.0 // 153pt (float var avoids const truncation rules)
	wantHpx := int(wantHpt / pageHPt * float64(pageHPx))
	if w != pageWPx || h != wantHpx {
		t.Errorf("constrain target = %dx%d, want %dx%d", w, h, pageWPx, wantHpx)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// makeTestImage returns a solid-color PNG of the given size.
func makeTestImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testConverter(t *testing.T) *Converter {
	t.Helper()
	return New(Config{
		Enabled:        true,
		TempDir:        t.TempDir(),
		PrepareTTL:     time.Hour,
		MaxPages:       50,
		DefaultFitMode: FitConstrain,
		// Point exec binaries at nothing: exec paths are exercised only in
		// skip-if-missing tests.
		GhostscriptBin: "gs",
		ImageMagickBin: "magick",
	})
}

func TestPrepareImagePDF(t *testing.T) {
	c := testConverter(t)
	data := makeTestImage(t, 800, 1000)
	doc, err := c.Prepare(context.Background(), "photo.png", data, 7, 3, Options{FitMode: FitConstrain})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer c.Delete(doc.ID)

	if doc.Pages != 1 {
		t.Errorf("pages = %d, want 1", doc.Pages)
	}
	if doc.UserID != 7 || doc.OrgID != 3 {
		t.Errorf("owner not recorded: %+v", doc)
	}
	b, err := doc.ReadPDF()
	if err != nil {
		t.Fatalf("ReadPDF: %v", err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Error("output is not a PDF")
	}
	// Must survive a reload through Load (meta round-trip).
	loaded, err := c.Load(doc.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != doc.ID || loaded.Pages != doc.Pages || loaded.UserID != 7 {
		t.Errorf("Load round-trip mismatch: %+v", loaded)
	}
}

func TestPrepareUnsupportedType(t *testing.T) {
	c := testConverter(t)
	_, err := c.Prepare(context.Background(), "evil.exe", []byte("MZ"), 1, 1, Options{})
	if err == nil {
		t.Fatal("expected error for .exe")
	}
}

func TestPrepareCoverPage(t *testing.T) {
	c := testConverter(t)
	data := makeTestImage(t, 400, 300)
	doc, err := c.Prepare(context.Background(), "pic.png", data, 1, 1, Options{
		FitMode: FitConstrain,
		Cover:   &CoverFields{To: "Alice", From: "Bob", Subject: "Hi", Comments: "see attached"},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer c.Delete(doc.ID)
	if !doc.Cover {
		t.Error("Cover flag not set")
	}
	if doc.Pages != 2 {
		t.Errorf("pages = %d, want 2 (cover + body)", doc.Pages)
	}
}

func TestStoreExpiry(t *testing.T) {
	c := testConverter(t)
	data := makeTestImage(t, 100, 100)
	doc, err := c.Prepare(context.Background(), "a.png", data, 1, 1, Options{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Wind the clock past the TTL: Load must report not-found and delete.
	c.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := c.Load(doc.ID); err != ErrNotFound {
		t.Errorf("expired Load = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(doc.Dir()); !os.IsNotExist(err) {
		t.Error("expired doc dir should be deleted")
	}
}

func TestStoreSweep(t *testing.T) {
	c := testConverter(t)
	data := makeTestImage(t, 100, 100)
	fresh, err := c.Prepare(context.Background(), "fresh.png", data, 1, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := c.Prepare(context.Background(), "stale.png", data, 1, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the stale doc's expiry and rewrite meta.
	stale.ExpiresAt = time.Now().Add(-time.Hour)
	if err := stale.saveMeta(); err != nil {
		t.Fatal(err)
	}
	c.Sweep()
	if _, err := os.Stat(fresh.Dir()); err != nil {
		t.Error("fresh doc should survive sweep")
	}
	if _, err := os.Stat(stale.Dir()); !os.IsNotExist(err) {
		t.Error("stale doc should be swept")
	}
}

func TestLoadRejectsTraversal(t *testing.T) {
	c := testConverter(t)
	for _, id := range []string{"../etc", "..", "not-a-uuid", "a", strings.Repeat("f", 36)} {
		if _, err := c.Load(id); err != ErrNotFound {
			t.Errorf("Load(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestOfficeToPDF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/forms/libreoffice/convert" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		f, fh, err := r.FormFile("files")
		if err != nil {
			t.Errorf("no files field: %v", err)
			return
		}
		defer f.Close()
		if fh.Filename != "letter.docx" {
			t.Errorf("filename = %q", fh.Filename)
		}
		if _, err := io.Copy(io.Discard, f); err != nil {
			t.Errorf("read upload: %v", err)
		}
		w.Write([]byte("%PDF-1.7 fake"))
	}))
	defer srv.Close()

	c := testConverter(t)
	c.cfg.GotenbergURL = srv.URL
	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := c.officeToPDF(context.Background(), "letter.docx", []byte("zipdata"), out); err != nil {
		t.Fatalf("officeToPDF: %v", err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "%PDF-1.7 fake" {
		t.Errorf("unexpected output %q", b)
	}
}

func TestOfficeToPDFFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "corrupt document")
	}))
	defer srv.Close()

	c := testConverter(t)
	c.cfg.GotenbergURL = srv.URL
	err := c.officeToPDF(context.Background(), "bad.docx", []byte("junk"), filepath.Join(t.TempDir(), "o.pdf"))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !bytes.Contains([]byte(got), []byte("corrupt document")) {
		t.Errorf("error should carry gotenberg's message, got %q", got)
	}
}

func TestOfficeToPDFRejectsNonPDF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not a pdf</html>"))
	}))
	defer srv.Close()
	c := testConverter(t)
	c.cfg.GotenbergURL = srv.URL
	if err := c.officeToPDF(context.Background(), "x.docx", []byte("junk"), filepath.Join(t.TempDir(), "o.pdf")); err == nil {
		t.Fatal("expected error for non-PDF output")
	}
}

// TestRenderPreviews exercises the gs path when ghostscript is installed;
// skipped otherwise (CI/dev machines without gs still test everything else).
func TestRenderPreviews(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("ghostscript not installed")
	}
	c := testConverter(t)
	doc, err := c.Prepare(context.Background(), "img.png", makeTestImage(t, 800, 1000), 1, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Delete(doc.ID)
	if doc.PreviewPages != doc.Pages {
		t.Errorf("previews = %d, want %d", doc.PreviewPages, doc.Pages)
	}
	f, err := os.Open(doc.PreviewPath(1))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("preview is not a PNG: %v", err)
	}
	if img.Bounds().Dx() != 1728 || img.Bounds().Dy() != 2156 {
		t.Errorf("preview size = %v, want 1728x2156", img.Bounds())
	}
}

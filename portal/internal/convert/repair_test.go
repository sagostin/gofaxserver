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
	"errors"
	"os"
	"os/exec"
	"testing"
)

// hpScanFixture is a real HP Scan Extended Application PDF whose trailer
// carries three duplicate /Info entries (the first pointing at the document
// catalog). Ghostscript ≤10.05 (e.g. the portal image's alpine:3.20 build)
// reports "circular reference to indirect object", renders nothing, and
// exits 0 — failing previews here and gofaxserver's tiffg3 conversion at
// send time. The fixture is a real scan and deliberately NOT committed
// (gitignored); tests using it skip when it's absent.
const hpScanFixture = "testdata/hp_scan_dup_info.pdf"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// readLocalFixture reads a gitignored (local-only) fixture, skipping when
// it's absent — same posture as the skip-if-missing gs tests.
func readLocalFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("fixture %s not present (gitignored, local only)", name)
	}
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestNormalizePDFRepairsDuplicateInfo(t *testing.T) {
	orig := readLocalFixture(t, hpScanFixture)
	if n := bytes.Count(orig, []byte("/Info")); n != 3 {
		t.Fatalf("fixture should have 3 /Info entries, has %d (regenerated fixture?)", n)
	}

	fixed := normalizePDF(context.Background(), orig)
	if bytes.Equal(fixed, orig) {
		t.Fatal("normalization returned the original bytes unchanged")
	}
	if !bytes.HasPrefix(fixed, []byte("%PDF-")) {
		t.Fatal("normalized output is not a PDF")
	}
	if n := bytes.Count(fixed, []byte("/Info")); n != 1 {
		t.Errorf("normalized PDF should have exactly 1 /Info entry, has %d", n)
	}
	if pages, err := pdfPageCount(fixed); err != nil || pages != 1 {
		t.Errorf("normalized page count = %d, %v; want 1, nil", pages, err)
	}
}

// TestNormalizePDFFallback: input pdfcpu cannot handle must come back
// byte-identical — downstream gates (page count, gs preview) decide.
func TestNormalizePDFFallback(t *testing.T) {
	junk := []byte("%PDF-1.7 not really a pdf at all")
	if got := normalizePDF(context.Background(), junk); !bytes.Equal(got, junk) {
		t.Error("unrepairable input should be returned unchanged")
	}
}

// TestConvertHPScanPDF: the fixture must survive the full prepare flow.
// With gs present this is the real regression test — before the repair pass,
// gs exited 0 with empty output and Convert failed hard with ErrEmptyRender.
func TestConvertHPScanPDF(t *testing.T) {
	c := testConverter(t)
	res, err := c.Convert(context.Background(), "scan.pdf", readLocalFixture(t, hpScanFixture), Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Pages != 1 {
		t.Errorf("pages = %d, want 1", res.Pages)
	}
	if n := bytes.Count(res.PDF, []byte("/Info")); n != 1 {
		t.Errorf("prepared PDF should carry the repaired trailer (1 /Info), has %d", n)
	}

	if _, err := exec.LookPath("gs"); err != nil {
		t.Log("ghostscript not installed; preview render regression not exercised")
		return
	}
	if len(res.Previews) != 1 {
		t.Fatalf("previews = %d, want 1 (gs rendered nothing for a repairable document)", len(res.Previews))
	}
	if !bytes.HasPrefix(res.Previews[0], []byte("\x89PNG")) {
		t.Error("preview is not a PNG")
	}
}

// TestConvertHealthyPDFUnharmed: a well-formed multi-page PDF (producer:
// pdfcpu's own import) must round-trip with content and page count intact.
func TestConvertHealthyPDFUnharmed(t *testing.T) {
	c := testConverter(t)
	res, err := c.Convert(context.Background(), "testfax-mp.pdf", readFixture(t, "testdata/testfax-mp.pdf"), Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Pages != 4 {
		t.Errorf("pages = %d, want 4", res.Pages)
	}
	if !bytes.HasPrefix(res.PDF, []byte("%PDF-")) {
		t.Error("output is not a PDF")
	}
}

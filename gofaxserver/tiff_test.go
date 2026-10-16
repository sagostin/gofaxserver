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

package gofaxserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verifyTiffOutput is the guard against ghostscript exiting 0 without
// producing a usable TIFF (the production failure that queued doomed jobs
// which later died at dial time with "stat ...: no such file or directory").
func TestVerifyTiffOutput(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing.tiff")
	err := verifyTiffOutput(missing, []byte("gs said something"))
	if err == nil || !strings.Contains(err.Error(), "no output file") {
		t.Errorf("missing file: expected 'no output file' error, got %v", err)
	}
	// The gs diagnostics must ride along — they are the only clue for why
	// a document failed to render.
	if err != nil && !strings.Contains(err.Error(), "gs said something") {
		t.Errorf("error should carry the gs output tail, got %q", err.Error())
	}

	empty := filepath.Join(dir, "empty.tiff")
	if werr := os.WriteFile(empty, nil, 0644); werr != nil {
		t.Fatal(werr)
	}
	if err := verifyTiffOutput(empty, nil); err == nil || !strings.Contains(err.Error(), "empty output file") {
		t.Errorf("zero-byte file: expected 'empty output file' error, got %v", err)
	}

	valid := filepath.Join(dir, "valid.tiff")
	if werr := os.WriteFile(valid, []byte("II*\x00not-a-real-tiff-but-nonempty"), 0644); werr != nil {
		t.Fatal(werr)
	}
	if err := verifyTiffOutput(valid, nil); err != nil {
		t.Errorf("non-empty file: unexpected error %v", err)
	}
}

func TestTruncateGSOutput(t *testing.T) {
	if got := truncateGSOutput([]byte("  short output  \n")); got != "short output" {
		t.Errorf("short output should pass through trimmed, got %q", got)
	}
	long := strings.Repeat("x", 1000)
	got := truncateGSOutput([]byte(long))
	if !strings.HasPrefix(got, "…") {
		t.Errorf("long output should be prefixed with an ellipsis")
	}
	if len(got) > 510 {
		t.Errorf("long output should be truncated to ~500 chars, got %d bytes", len(got))
	}
	if got := truncateGSOutput(nil); got != "" {
		t.Errorf("nil output should render empty, got %q", got)
	}
}

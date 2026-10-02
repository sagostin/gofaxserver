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
	"testing"
)

func TestMimeAllowed(t *testing.T) {
	valid := map[string]string{
		".pdf":  "application/pdf",
		".tiff": "image/tiff",
		".tif":  "image/x-tiff",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".docx": "application/zip",
		".doc":  "application/x-ole-storage",
	}
	for ext, mime := range valid {
		if !mimeAllowed(ext, mime) {
			t.Errorf("mimeAllowed(%q, %q) = false, want true", ext, mime)
		}
	}
	// Extension/content mismatches must be rejected.
	mismatches := [][2]string{
		{".docx", "application/x-ole-storage"}, // doc bytes renamed to docx
		{".doc", "application/zip"},
		{".png", "image/jpeg"},
		{".pdf", "application/octet-stream"},
		{".jpg", "image/png"},
		{".exe", "application/x-ole-storage"}, // unknown ext → no allowlist
	}
	for _, m := range mismatches {
		if mimeAllowed(m[0], m[1]) {
			t.Errorf("mimeAllowed(%q, %q) = true, want false", m[0], m[1])
		}
	}
}

// The prepare/preview routes live in the user realm and must reject
// anonymous requests before touching storage (no converter configured on the
// test server — a nil dereference here would be a regression).
func TestPrepareRoutesRejectAnonymous(t *testing.T) {
	e := testServer(t)
	e.POST("/portal/api/faxes/prepare").Expect().Status(401)
	e.GET("/portal/api/faxes/prepare/00000000-0000-0000-0000-000000000000/preview/1").
		Expect().Status(401)
	e.DELETE("/portal/api/faxes/prepare/00000000-0000-0000-0000-000000000000").
		Expect().Status(401)
}

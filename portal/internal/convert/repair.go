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
	"log"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// normalizePDF rewrites an uploaded PDF through pdfcpu (parse + write back,
// in memory). Real-world producers ship spec-violating files that pdfcpu
// tolerates but Ghostscript rejects in a treacherous way — e.g. HP Scan
// emits trailers with duplicate /Info entries whose first occurrence points
// at the document catalog; gs 10.x reports "circular reference to indirect
// object", renders NOTHING, and still exits 0, which would fail both the
// preview render here (ErrEmptyRender) and gofaxserver's tiffg3 conversion
// at send time. pdfcpu's dict parsing collapses duplicate keys (last wins),
// so the round-trip emits a clean trailer that gs renders normally.
//
// Best-effort: on any error (or empty output) the original bytes are
// returned unchanged, so files pdfcpu can't handle behave exactly as before
// — pdfcpu page-count and the gs preview render still gate faxability
// downstream.
func normalizePDF(ctx context.Context, data []byte) []byte {
	var buf bytes.Buffer
	if err := api.Optimize(ctx, bytes.NewReader(data), &buf, nil, nil); err != nil {
		log.Printf("[convert] pdf normalization skipped, keeping original: %v", err)
		return data
	}
	if out := buf.Bytes(); len(out) > 0 {
		return out
	}
	return data
}

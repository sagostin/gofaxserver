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
	"context"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// pdfPageCount returns the page count of a PDF (pure Go, via pdfcpu).
func pdfPageCount(path string) (int, error) {
	return api.PageCountFile(context.Background(), path)
}

// mergePDFs concatenates inFiles (in order) into outFile.
func mergePDFs(inFiles []string, outFile string) error {
	return api.MergeCreateFile(context.Background(), inFiles, outFile, false, nil)
}

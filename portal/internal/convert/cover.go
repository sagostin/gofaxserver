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
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// renderCoverPage renders a simple one-page Letter cover sheet. bodyPages is
// the page count of the document it precedes, so the sheet can report the
// total page count a fax recipient should expect.
func renderCoverPage(f CoverFields, bodyPages int) ([]byte, error) {
	pdf := fpdf.New("P", "pt", "Letter", "")
	pdf.SetMargins(72, 72, 72) // 1in
	pdf.SetAutoPageBreak(true, 72)
	pdf.AddPage()

	left := 72.0
	width := pageWPt - 2*72

	// Header
	pdf.SetFont("Helvetica", "B", 28)
	pdf.CellFormat(width, 36, "FAX", "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 36, time.Now().Format("January 2, 2006 15:04"), "", 1, "R", false, 0, "")
	pdf.SetDrawColor(0, 0, 0)
	pdf.SetLineWidth(1.5)
	pdf.Line(left, 116, left+width, 116)
	pdf.Ln(28)

	row := func(label, value string) {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(90, 22, label, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 12)
		pdf.CellFormat(0, 22, value, "B", 1, "L", false, 0, "")
		pdf.Ln(6)
	}

	pdf.SetY(140)
	row("To:", f.To)
	row("From:", f.From)
	row("Subject:", f.Subject)
	row("Pages:", fmt.Sprintf("%d (including this cover page)", bodyPages+1))

	pdf.Ln(14)
	if f.Comments != "" {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 20, "Comments:", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 11)
		pdf.MultiCell(0, 16, f.Comments, "T", "L", false)
	}

	out := &bytes.Buffer{}
	if err := pdf.Output(out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

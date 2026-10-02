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
	"os/exec"
	"time"
)

// execTimeout caps each external converter run so a wedged gs/magick can't
// pin a handler forever.
const execTimeout = 90 * time.Second

// runPiped feeds stdin to an external tool and returns its stdout. Both
// converter tools support explicit-format pipes, so plaintext documents never
// touch the filesystem — only sealed output is written (by the caller).
func runPiped(ctx context.Context, bin string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", bin, err, truncate(stderr.String(), 256))
	}
	return stdout.Bytes(), nil
}

// renderPreviews rasterizes every page of pdf to fax-accurate B&W PNGs
// (1728x2156, 204x196 dpi mono — the same geometry gofaxserver's own tiffg3
// pipeline produces, so the preview matches the transmitted fax).
//
// Ghostscript is invoked once per page with the PDF on stdin and the PNG on
// stdout — multi-page %d output patterns would force plaintext files.
func (c *Converter) renderPreviews(ctx context.Context, pdf []byte, pages int) ([][]byte, error) {
	if pages < 1 {
		return nil, nil
	}
	previews := make([][]byte, 0, pages)
	for page := 1; page <= pages; page++ {
		png, err := runPiped(ctx, c.cfg.GhostscriptBin, pdf,
			"-q", "-dNOPAUSE", "-dBATCH", "-dSAFER",
			"-sDEVICE=pngmono", "-r204x196", "-g1728x2156", "-dPDFFitPage",
			fmt.Sprintf("-dFirstPage=%d", page), fmt.Sprintf("-dLastPage=%d", page),
			"-sOutputFile=-", "-",
		)
		if err != nil {
			return nil, fmt.Errorf("ghostscript page %d: %w", page, err)
		}
		if !bytes.HasPrefix(png, []byte("\x89PNG")) {
			return nil, fmt.Errorf("ghostscript page %d: no PNG on stdout", page)
		}
		previews = append(previews, png)
	}
	return previews, nil
}

// tiffToPDF converts a (possibly multi-page) TIFF to PDF via ImageMagick,
// which handles CCITT fax compression natively. Density is pinned to fax
// resolution so the PDF page size matches the scan geometry. Input and
// output both travel over pipes.
func (c *Converter) tiffToPDF(ctx context.Context, data []byte) ([]byte, error) {
	pdf, err := runPiped(ctx, c.cfg.ImageMagickBin, data,
		"tiff:-", "-units", "PixelsPerInch", "-density", "204x196", "pdf:-",
	)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, fmt.Errorf("imagemagick produced non-PDF output")
	}
	return pdf, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

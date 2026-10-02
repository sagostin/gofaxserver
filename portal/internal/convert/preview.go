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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// execTimeout caps external converter runs so a wedged gs/magick can't
// pin a handler forever.
const execTimeout = 90 * time.Second

// renderPreviews rasterizes every page of pdfPath to fax-accurate B&W PNGs
// (1728x2156, 204x196 dpi mono — the same geometry gofaxserver's own tiffg3
// pipeline produces, so the preview matches the transmitted fax). Returns
// the number of preview pages written.
func (c *Converter) renderPreviews(ctx context.Context, pdfPath, dir string, pages int) (int, error) {
	if pages < 1 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	outPattern := filepath.Join(dir, "page-%d.png")
	cmd := exec.CommandContext(ctx, c.cfg.GhostscriptBin,
		"-q", "-dNOPAUSE", "-dBATCH", "-dSAFER",
		"-sDEVICE=pngmono", "-r204x196", "-g1728x2156", "-dPDFFitPage",
		"-sOutputFile="+outPattern, pdfPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("ghostscript: %w: %s", err, truncate(string(out), 256))
	}
	for i := 1; i <= pages; i++ {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("page-%d.png", i))); err != nil {
			return i - 1, fmt.Errorf("ghostscript produced %d/%d pages", i-1, pages)
		}
	}
	return pages, nil
}

// tiffToPDF converts a (possibly multi-page) TIFF to PDF via ImageMagick,
// which handles CCITT fax compression natively. Density is pinned to fax
// resolution so the PDF page size matches the scan geometry.
func (c *Converter) tiffToPDF(ctx context.Context, data []byte, outPath string) error {
	in, err := os.CreateTemp(filepath.Dir(outPath), "upload-*.tiff")
	if err != nil {
		return err
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(data); err != nil {
		in.Close()
		return err
	}
	if err := in.Close(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.cfg.ImageMagickBin,
		in.Name(), "-units", "PixelsPerInch", "-density", "204x196", outPath+".tmp.pdf",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("imagemagick: %w: %s", err, truncate(string(out), 256))
	}
	return os.Rename(outPath+".tmp.pdf", outPath)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

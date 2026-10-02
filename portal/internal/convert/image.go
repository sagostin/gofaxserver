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
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"

	"github.com/go-pdf/fpdf"
	xdraw "golang.org/x/image/draw"
)

// Fax page geometry: standard fax is 1728x2156 px at 204x196 dpi, i.e.
// US Letter portrait. PDF points are 1/72in → 612x792pt.
const (
	pageWPt     = 612.0
	pageHPt     = 792.0
	pageWPx     = 1728
	pageHPx     = 2156
	maxImgDim   = 4096 // reject absurd rasters before decoding pixels
	jpegQuality = 90
)

// placement describes where the resampled image lands on the page.
type placement struct {
	x, y, w, h float64 // points
}

// computePlacement is a pure function (unit-tested) resolving a fit mode to a
// destination rectangle on the Letter page. iw/ih are the (post-crop) pixel
// dimensions being placed.
func computePlacement(mode FitMode, iw, ih int) placement {
	switch mode {
	case FitStretch:
		return placement{0, 0, pageWPt, pageHPt}
	case FitFill, FitConstrain:
		w := float64(iw)
		h := float64(ih)
		var scale float64
		if mode == FitFill {
			scale = maxf(pageWPt/w, pageHPt/h)
		} else {
			scale = minf(pageWPt/w, pageHPt/h)
		}
		dw, dh := w*scale, h*scale
		return placement{(pageWPt - dw) / 2, (pageHPt - dh) / 2, dw, dh}
	}
	return placement{0, 0, pageWPt, pageHPt}
}

// cropForFill returns the centered source crop rect whose aspect ratio
// matches the page, so scaling it to the page covers without distortion.
// Pure function, unit-tested.
func cropForFill(iw, ih int) image.Rectangle {
	pageAspect := pageWPt / pageHPt
	srcAspect := float64(iw) / float64(ih)
	switch {
	case srcAspect > pageAspect: // too wide: crop sides
		cw := int(float64(ih) * pageAspect)
		x0 := (iw - cw) / 2
		return image.Rect(x0, 0, x0+cw, ih)
	case srcAspect < pageAspect: // too tall: crop top/bottom
		ch := int(float64(iw) / pageAspect)
		y0 := (ih - ch) / 2
		return image.Rect(0, y0, iw, y0+ch)
	default:
		return image.Rect(0, 0, iw, ih)
	}
}

// targetRasterSize caps the embedded raster at fax resolution (204x196 dpi)
// so a 24MP phone photo doesn't produce a 60MB PDF. Pure function.
func targetRasterSize(mode FitMode, iw, ih int) (int, int) {
	p := computePlacement(mode, iw, ih)
	// points → px at the fax dpi (204 horizontal, 196 vertical)
	w := int(p.w / pageWPt * pageWPx)
	h := int(p.h / pageHPt * pageHPx)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// imageToPDF renders a PNG/JPEG onto one Letter page per the fit mode and
// writes the result as a single-page PDF.
func (c *Converter) imageToPDF(data []byte, outPath string, mode FitMode) error {
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	b := src.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw < 1 || ih < 1 || iw > maxImgDim || ih > maxImgDim {
		return fmt.Errorf("image dimensions %dx%d out of range", iw, ih)
	}

	if mode == FitFill {
		// Decoded rasters normally implement SubImage; if an exotic decoder
		// doesn't, fall back to an uncropped cover (slight distortion).
		if si, okSrc := src.(interface {
			SubImage(image.Rectangle) image.Image
		}); okSrc {
			src = si.SubImage(cropForFill(iw, ih))
			b = src.Bounds()
			iw, ih = b.Dx(), b.Dy()
		}
	}

	// Flatten onto white (faxes are B&W; PNG alpha would render as black).
	flat := image.NewRGBA(image.Rect(0, 0, iw, ih))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, b.Min, draw.Over)

	// Resample down to fax resolution for a sane file size.
	tw, th := targetRasterSize(mode, iw, ih)
	if tw < iw || th < ih {
		scaled := image.NewRGBA(image.Rect(0, 0, tw, th))
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), flat, flat.Bounds(), xdraw.Over, nil)
		flat = scaled
	}

	// Encode: keep JPEG as JPEG (photos stay small), everything else PNG.
	var buf bytes.Buffer
	imgType := "PNG"
	if format == "jpeg" {
		if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return err
		}
		imgType = "JPG"
	} else {
		if err := png.Encode(&buf, flat); err != nil {
			return err
		}
	}

	p := computePlacement(mode, flat.Bounds().Dx(), flat.Bounds().Dy())
	pdf := fpdf.New("P", "pt", "Letter", "")
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddPage()
	name := "img"
	pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: imgType, ReadDpi: true}, &buf)
	pdf.ImageOptions(name, p.x, p.y, p.w, p.h, false, fpdf.ImageOptions{}, 0, "")

	out := &bytes.Buffer{}
	if err := pdf.Output(out); err != nil {
		return fmt.Errorf("render PDF: %w", err)
	}
	return writeFileAtomic(outPath, out.Bytes())
}

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
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gofaxserver/gofaxlib"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tiffToPdf takes an input TIFF file path, converts it to a PDF using ImageMagick,
// and returns the new PDF file's path. The original TIFF file is left unchanged.
func tiffToPdf(inputTiff string) (pdfPath string, err error) {
	// Derive the output PDF file name by replacing the TIFF extension with .pdf.
	baseName := strings.TrimSuffix(inputTiff, filepath.Ext(inputTiff))
	pdfPath = baseName + ".pdf"

	cmd := exec.Command("magick", inputTiff, pdfPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to convert TIFF to PDF: %v, output: %s", err, string(output))
	}

	return pdfPath, nil
}

func pdfToTiff(docID uuid.UUID, fileExtension string, file multipart.File, fileHeader *multipart.FileHeader) (fileName string, err error) {
	fileFormat := "temp_%s%s"

	// Save uploaded file to a temporary location.
	tempFile := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(fileFormat, docID, fileExtension))
	tmpFile, err := os.Create(tempFile)
	if err != nil {
		return "", errors.New("failed to create temp file: " + err.Error())
	}
	defer func() {
		tmpFile.Close()
		// Optionally remove the temp file later.
	}()

	if _, err = io.Copy(tmpFile, file); err != nil {
		return "", errors.New("failed to save document: " + err.Error())
	}

	// Determine output path for TIFF.
	destFile := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf(tempFileFormat, docID))
	// Convert the file to TIFF using ImageMagick's 'convert' command.
	//cmdStr := fmt.Sprintf("magick %s -density 204x196 -resize '1728x2156!' -background white -alpha background -compress Group4 %s", tempFile, destFile)
	cmdStr := fmt.Sprintf(
		"gs -q -r204x196 -g1728x2156 -dNOPAUSE -dBATCH -dSAFER -dPDFFitPage -sDEVICE=tiffg3 -sOutputFile=%s -- %s",
		destFile, tempFile,
	)
	cmd := exec.Command("/bin/bash", "-c", cmdStr)
	if err = cmd.Run(); err != nil {
		return "", errors.New("failed to convert document: " + err.Error())
	}
	os.Remove(tempFile)

	return destFile, nil
}

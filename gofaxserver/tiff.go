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
)

func pdfToTiff(docID uuid.UUID, fileExtension string, file multipart.File, fileHeader *multipart.FileHeader) (fileName string, err error) {
	fileFormat := "webhook_fax_%s%s"

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

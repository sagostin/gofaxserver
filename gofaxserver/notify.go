package gofaxserver

import (
	"encoding/json"
	"fmt"
	"github.com/go-pdf/fpdf"
	"gofaxserver/gofaxlib"
	"path/filepath"
	"sort"
	"strings"
)

type NotifyFaxResults struct {
	Results map[string]*FaxJob `json:"results,omitempty"`
	FaxJob  *FaxJob            `json:"fax_job,omitempty"`
}

func (nfr *NotifyFaxResults) GenerateFaxResultsPDF() (string, error) {
	// Construct output path using the FaxJob UUID.
	outputPath := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf("notify_%s.pdf", nfr.FaxJob.UUID.String()))

	// Create a new A4 portrait PDF document.
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	// Title and header.
	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(190, 10, "Fax Results Report", "", 1, "C", false, 0, "")
	pdf.SetFont("Arial", "", 8)
	pdf.CellFormat(190, 8, "ID: "+nfr.FaxJob.UUID.String(), "", 1, "C", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(190, 8, "Caller: "+nfr.FaxJob.CallerIdNumber, "", 1, "C", false, 0, "")
	pdf.CellFormat(190, 8, "Callee: "+nfr.FaxJob.CalleeNumber, "", 1, "C", false, 0, "")
	pdf.CellFormat(190, 8, "Timestamp (received): "+nfr.FaxJob.SourceInfo.Timestamp.Format("2006-01-02 15:04:05"), "", 1, "C", false, 0, "")
	pdf.Ln(4)

	// Define a map of column headers to their widths.
	columns := map[string]float64{
		"Call ID":   45,
		"Message":   50,
		"Timestamp": 50,
		"Status":    45,
	}
	// Define the desired order of the columns.
	order := []string{"Call ID", "Timestamp", "Status", "Message"}

	// Draw the table header.
	pdf.SetFont("Arial", "B", 12)
	for _, colName := range order {
		width := columns[colName]
		pdf.CellFormat(width, 10, fitText(pdf, colName, width), "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)

	var keys []string
	for k := range nfr.Results {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		jobI := nfr.Results[keys[i]]
		jobJ := nfr.Results[keys[j]]
		if jobI.Result == nil || jobJ.Result == nil {
			return keys[i] < keys[j]
		}
		return jobI.Result.EndTs.Before(jobJ.Result.EndTs)
	})

	// Draw table rows.
	pdf.SetFont("Arial", "", 12)
	for _, key := range keys {
		faxJob := nfr.Results[key]

		marshal, err := json.Marshal(faxJob)
		if err != nil {
			return "", err
		}
		fmt.Println(string(marshal))

		// For Call ID, display only the last segment of the UUID.
		callIDFull := faxJob.CallUUID.String()
		parts := strings.Split(callIDFull, "-")
		shortCallID := parts[len(parts)-1]

		// Format timestamp.
		timestamp := faxJob.Result.EndTs.Format("2006-01-02 15:04:05")
		// Use ResultText if available, otherwise fallback to Status.
		resultText := faxJob.Status
		if faxJob.Result.ResultText != "" {
			resultText = faxJob.Result.ResultText
		}

		endpointType := faxJob.Endpoints[0].EndpointType

		success := "failed"
		if faxJob.Result.Success {
			success = "success"
		}

		// Create a row data map.
		rowData := map[string]string{
			"Call ID":   shortCallID,
			"Timestamp": timestamp,
			"Status":    success + " (" + endpointType + ")",
			"Message":   resultText,
		}

		// Draw each cell in order.
		for _, colName := range order {
			width := columns[colName]
			cellText := fitText(pdf, rowData[colName], width)
			pdf.CellFormat(width, 10, cellText, "1", 0, "C", false, 0, "")
		}
		pdf.Ln(-1)
	}

	// Output the PDF file.
	err := pdf.OutputFileAndClose(outputPath)
	return outputPath, err
}

// fitText ensures that the given text fits within the specified width.
// If the text is too long, it truncates it and appends an ellipsis.
func fitText(pdf *fpdf.Fpdf, text string, width float64) string {
	if pdf.GetStringWidth(text) <= width {
		return text
	}
	ellipsis := "..."
	for pdf.GetStringWidth(text+ellipsis) > width && len(text) > 0 {
		text = text[:len(text)-1]
	}
	return text + ellipsis
}

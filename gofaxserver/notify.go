package gofaxserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/go-pdf/fpdf"
	"gofaxserver/gofaxlib"
	"net/smtp"
	"os"
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

// SendEmailWithAttachment sends an email with a plain text body and a file attachment.
func SendEmailWithAttachment(subject, to, body, attachmentPath string) error {
	// Read the attachment file from disk.
	attachmentBytes, err := os.ReadFile(attachmentPath)
	if err != nil {
		return fmt.Errorf("failed to read attachment: %w", err)
	}

	// Encode the attachment in base64.
	encodedAttachment := base64.StdEncoding.EncodeToString(attachmentBytes)

	// Create a MIME boundary.
	boundary := "myBoundary123456789"

	// Build the email message.
	// Use your SMTP config for the From field.
	from := fmt.Sprintf("%s <%s>", gofaxlib.Config.SMTP.FromName, gofaxlib.Config.SMTP.FromAddress)

	// Email headers.
	headers := map[string]string{
		"From":         from,
		"To":           to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": fmt.Sprintf("multipart/mixed; boundary=%s", boundary),
	}

	// Construct the header string.
	var msg strings.Builder
	for k, v := range headers {
		msg.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msg.WriteString("\r\n") // End headers

	// Plain text part.
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	msg.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body + "\r\n")

	// Attachment part.
	filename := filepath.Base(attachmentPath)
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString(fmt.Sprintf("Content-Type: application/octet-stream; name=\"%s\"\r\n", filename))
	msg.WriteString("Content-Transfer-Encoding: base64\r\n")
	msg.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", filename))
	msg.WriteString("\r\n")
	// For better readability, split the base64 string into lines of 76 characters.
	const maxLineLen = 76
	for i := 0; i < len(encodedAttachment); i += maxLineLen {
		end := i + maxLineLen
		if end > len(encodedAttachment) {
			end = len(encodedAttachment)
		}
		msg.WriteString(encodedAttachment[i:end] + "\r\n")
	}
	msg.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	// Set up SMTP connection.
	addr := fmt.Sprintf("%s:%d", gofaxlib.Config.SMTP.Host, gofaxlib.Config.SMTP.Port)
	auth := smtp.PlainAuth("", gofaxlib.Config.SMTP.Username, gofaxlib.Config.SMTP.Password, gofaxlib.Config.SMTP.Host)

	// Send the email.
	if err := smtp.SendMail(addr, auth, gofaxlib.Config.SMTP.FromAddress, []string{to}, []byte(msg.String())); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}
	return nil
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

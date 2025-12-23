package gofaxserver

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"gofaxserver/gofaxlib"
	"io"
	"mime/multipart"
	"net/http"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/sirupsen/logrus"
)

type NotifyFaxResults struct {
	Results map[string]*FaxJob `json:"results,omitempty"`
	FaxJob  *FaxJob            `json:"fax_job,omitempty"`
}

type NotifyDestination struct {
	Type        string `json:"type"`
	Destination string `json:"destination"`
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

		/*marshal, err := json.Marshal(faxJob)
		if err != nil {
			return "", err
		}
		fmt.Println(string(marshal))*/

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

func (q *Queue) processNotifyDestinations(f *FaxJob) ([]NotifyDestination, error) {
	var notifyDestinations []NotifyDestination

	// Helper function to parse and append notify string if not empty.
	appendNotify := func(notifyStr string) error {
		if notifyStr != "" {
			destinations, err := parseNotifyString(notifyStr)
			if err != nil {
				return fmt.Errorf("error parsing notify string: %w", err)
			}
			notifyDestinations = append(notifyDestinations, destinations...)
			/*for _, d := range destinations {
				fmt.Printf("Added Destination - Type: %s, Destination: %s\n", d.Type, d.Destination)
			}*/
		}
		return nil
	}

	// Process source tenant (srcT)
	srcT := q.server.Tenants[f.SrcTenantID]
	if srcT != nil {
		// Try getting notify settings from the number for the caller.
		number, err := q.server.getNumber(f.CallerIdNumber)
		if err != nil {
		}
		if number != nil && number.Notify != "" {
			if err := appendNotify(number.Notify); err != nil {
			}
		} else if srcT.Notify != "" {
			// Fallback to tenant-level notify settings.
			if err := appendNotify(srcT.Notify); err != nil {
			}
		}
	}

	// Process destination tenant (dstT)
	dstT := q.server.Tenants[f.DstTenantID]
	if dstT != nil {
		// Try getting notify settings from the number for the callee.
		number, err := q.server.getNumber(f.CalleeNumber)
		if err != nil {
		}
		if number != nil && number.Notify != "" {
			if err := appendNotify(number.Notify); err != nil {
			}
		} else if dstT.Notify != "" {
			// Fallback to tenant-level notify settings.
			if err := appendNotify(dstT.Notify); err != nil {
			}
		}
	}

	return notifyDestinations, nil
}

// format of: email->shaun.agostinho@topsoffice.ca;shaun@dec0de.xyz,webhook->https://example.org/endpoint,gateway->TODO

func parseNotifyString(notify string) ([]NotifyDestination, error) {
	var destinations []NotifyDestination

	// Split the string by commas to get individual segments.
	segments := strings.Split(notify, ",")
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		// Split each segment into type and destination using "->".
		parts := strings.SplitN(segment, "->", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid segment format: %s", segment)
		}
		destType := strings.TrimSpace(parts[0])
		destValue := strings.TrimSpace(parts[1])
		// Optionally, if multiple destinations are separated by semicolons, you could process them here.
		// For now, we store the entire string in Destination.
		destinations = append(destinations, NotifyDestination{
			Type:        destType,
			Destination: destValue,
		})
	}

	return destinations, nil
}

// SendEmailWithAttachment sends an email with a plain text body and a file attachment via SMTP.
// If the SMTP username is empty, it sends the email without authentication.
// The "to" parameter can be a semicolon-separated list of email addresses.
// SendEmailWithAttachment sends an email with a plain text body and multiple file attachments via SMTP.
// If the SMTP username is empty, it sends the email without authentication.
// The "to" parameter can be a semicolon-separated list of email addresses.
func SendEmailWithAttachment(subject, to, body string, attachmentPaths []string) error {
	// Create a MIME boundary.
	boundary := "myBoundary123456789"

	// Build the email message.
	from := fmt.Sprintf("%s <%s>", gofaxlib.Config.SMTP.FromName, gofaxlib.Config.SMTP.FromAddress)
	headers := map[string]string{
		"From":         from,
		"To":           to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": fmt.Sprintf("multipart/mixed; boundary=%s", boundary),
	}

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

	// Process each attachment.
	for _, attachmentPath := range attachmentPaths {
		// Read the attachment file from disk.
		attachmentBytes, err := os.ReadFile(attachmentPath)
		if err != nil {
			return fmt.Errorf("failed to read attachment %s: %w", attachmentPath, err)
		}

		// Encode the attachment in base64.
		encodedAttachment := base64.StdEncoding.EncodeToString(attachmentBytes)

		// Attachment part.
		filename := filepath.Base(attachmentPath)
		msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		msg.WriteString(fmt.Sprintf("Content-Type: application/octet-stream; name=\"%s\"\r\n", filename))
		msg.WriteString("Content-Transfer-Encoding: base64\r\n")
		msg.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", filename))
		msg.WriteString("\r\n")

		const maxLineLen = 76
		for i := 0; i < len(encodedAttachment); i += maxLineLen {
			end := i + maxLineLen
			if end > len(encodedAttachment) {
				end = len(encodedAttachment)
			}
			msg.WriteString(encodedAttachment[i:end] + "\r\n")
		}
	}

	// End boundary.
	msg.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	// Split the "to" field by semicolon and trim spaces.
	recipients := strings.Split(to, ";")
	for i, r := range recipients {
		recipients[i] = strings.TrimSpace(r)
	}

	addr := fmt.Sprintf("%s:%d", gofaxlib.Config.SMTP.Host, gofaxlib.Config.SMTP.Port)
	var auth smtp.Auth
	if gofaxlib.Config.SMTP.Username != "" {
		auth = smtp.PlainAuth("", gofaxlib.Config.SMTP.Username, gofaxlib.Config.SMTP.Password, gofaxlib.Config.SMTP.Host)
	}

	enc := strings.ToLower(gofaxlib.Config.SMTP.Encryption)
	if enc == "tls" || enc == "ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false, // adjust if necessary
			ServerName:         gofaxlib.Config.SMTP.Host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to dial TLS: %w", err)
		}
		client, err := smtp.NewClient(conn, gofaxlib.Config.SMTP.Host)
		if err != nil {
			return fmt.Errorf("failed to create SMTP client: %w", err)
		}
		if auth != nil {
			if err = client.Auth(auth); err != nil {
				return fmt.Errorf("failed to authenticate: %w", err)
			}
		}
		if err = client.Mail(gofaxlib.Config.SMTP.FromAddress); err != nil {
			return err
		}
		// Add each recipient.
		for _, r := range recipients {
			if err = client.Rcpt(r); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(msg.String()))
		if err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
		client.Quit()
	} else {
		if err := smtp.SendMail(addr, auth, gofaxlib.Config.SMTP.FromAddress, recipients, []byte(msg.String())); err != nil {
			return fmt.Errorf("failed to send email: %w", err)
		}
	}
	return nil
}

// processNotifyDestinationsAsync processes each NotifyDestination concurrently.
func (q *Queue) processNotifyDestinationsAsync(nFR NotifyFaxResults, destinations []NotifyDestination, firstPageTiffPDF string) {
	var notifyWg sync.WaitGroup

	// Log the start of notify processing with destination summary
	destTypes := make(map[string]int)
	for _, d := range destinations {
		destTypes[d.Type]++
	}
	q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
		"Notify",
		"starting notify dispatch",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":              nFR.FaxJob.UUID.String(),
			"destination_count": len(destinations),
			"destination_types": destTypes,
		},
	))

	if len(destinations) == 0 {
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
			"Notify",
			"no notify destinations configured, skipping",
			logrus.DebugLevel,
			map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
		))
		return
	}

	faxReport, err := nFR.GenerateFaxResultsPDF()
	if err != nil {
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
			"Notify",
			"failed to save fax result report",
			logrus.ErrorLevel,
			map[string]interface{}{"uuid": nFR.FaxJob.UUID.String(), "pdf_path": faxReport},
		))
		return
	}

	for _, nD := range destinations {
		// Capture the loop variable.
		dest := nD
		notifyWg.Add(1)
		go func() {
			defer notifyWg.Done()

			// Process each destination based on its type.
			switch dest.Type {
			case "email_report":
				// Send an email notification with the PDF report attached.
				// fmt.Printf("Processing email destination: %s\n", dest.Destination)
				subject := "Fax Report"
				body := "Please find the attached fax report."
				if err := SendEmailWithAttachment(subject, dest.Destination, body, []string{faxReport}); err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("failed to send email to %s: %v", dest.Destination, err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
				} else {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						"email_report sent successfully",
						logrus.InfoLevel,
						map[string]interface{}{
							"uuid":        nFR.FaxJob.UUID.String(),
							"destination": dest.Destination,
							"type":        "email_report",
						},
					))
				}
			case "email_full", "email_full_failure":
				// Send an email notification with the PDF report attached.
				// fmt.Printf("Processing email destination: %s\n", dest.Destination)

				// if the type is only for failures then
				if dest.Type == "email_full_failure" {
					if nFR.FaxJob.Result.Success {
						break
					}
				}

				subject := "Full Fax Report"
				body := "Please find the attached fax report & original fax."

				pdf, err := tiffToPdf(nFR.FaxJob.FileName)
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Queue",
						fmt.Sprintf("failed to convert tiff to pdf: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
				}

				if err := SendEmailWithAttachment(subject, dest.Destination, body, []string{faxReport, pdf}); err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("failed to send email to %s: %v", dest.Destination, err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
				} else {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						"email_full sent successfully",
						logrus.InfoLevel,
						map[string]interface{}{
							"uuid":        nFR.FaxJob.UUID.String(),
							"destination": dest.Destination,
							"type":        dest.Type,
						},
					))
				}

				defer func(name string) {
					err := os.Remove(name)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to remove full fax file: %s", pdf),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
					}
				}(pdf)
			case "webhook":
				// Read the fax file from disk.
				fileBytes, err := os.ReadFile(faxReport)
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("failed to read fax file for webhook: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID},
					))
					break
				}
				fileData := base64.StdEncoding.EncodeToString(fileBytes)

				// Generate the PDF report and read it.

				// Build payload struct that includes fax job details, fax file data, and PDF data.
				// todo improve the data format
				type WebhookPayload struct {
					NotifyFaxResults NotifyFaxResults `json:"fax_job_results"`
					FileData         string           `json:"file_data"`
				}
				payloadStruct := WebhookPayload{
					NotifyFaxResults: nFR,
					FileData:         fileData,
				}

				payload, err := json.Marshal(payloadStruct)
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("failed to marshal payload for webhook: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
					break
				}

				webhookURL := dest.Destination
				req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(payload))
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("error creating POST request for webhook: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
				} else {
					req.Header.Set("Content-Type", "application/json")
					client := &http.Client{Timeout: 10 * time.Second}
					resp, err := client.Do(req)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("error sending POST request to webhook: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
					} else {
						resp.Body.Close()
						if resp.StatusCode >= 200 && resp.StatusCode < 300 {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Notify",
								"webhook notification sent successfully",
								logrus.InfoLevel,
								map[string]interface{}{
									"uuid":        nFR.FaxJob.UUID.String(),
									"destination": dest.Destination,
									"status_code": resp.StatusCode,
									"type":        "webhook",
								},
							))
						} else {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Notify",
								fmt.Sprintf("webhook responded with status %d", resp.StatusCode),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
							))
						}
					}
				}
			case "webhook_form":
				{
					// Ensure temporary PDF is removed after sending.

					payload, err := json.Marshal(nFR)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to marshal payload for webhook: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}

					// Prepare multipart form data.
					body := &bytes.Buffer{}
					writer := multipart.NewWriter(body)

					// Add form fields (adjust these as needed).
					_ = writer.WriteField("fax_job_results", string(payload))
					// (Add more fields if necessary)

					// Open the first page PDF file.
					file, err := os.Open(firstPageTiffPDF)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to open first page pdf: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}
					defer file.Close()

					// Attach the file to the form; field name "firstpage_pdf".
					part, err := writer.CreateFormFile("firstpage_pdf", filepath.Base(firstPageTiffPDF))
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to create form file: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}
					_, err = io.Copy(part, file)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to copy file data: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}

					// Close the writer to flush the multipart data.
					if err := writer.Close(); err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to close writer: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}

					// Build and send the POST request.
					webhookURL := dest.Destination
					req, err := http.NewRequest("POST", webhookURL, body)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to create webhook_form request: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}
					req.Header.Set("Content-Type", writer.FormDataContentType())

					client := &http.Client{Timeout: 10 * time.Second}
					resp, err := client.Do(req)
					if err != nil {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("failed to send webhook_form request: %v", err),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}
					resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							"webhook_form notification sent successfully",
							logrus.InfoLevel,
							map[string]interface{}{
								"uuid":        nFR.FaxJob.UUID.String(),
								"destination": dest.Destination,
								"status_code": resp.StatusCode,
								"type":        "webhook_form",
							},
						))
					} else {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							fmt.Sprintf("webhook_form responded with status %d", resp.StatusCode),
							logrus.ErrorLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
					}
				}
			case "gateway":
				// Example: process a gateway notification.
				fmt.Printf("Processing gateway destination: %s\n", dest.Destination)
				// q.sendGatewayNotification(dest) // replace with actual call.
			default:
				fmt.Printf("Unknown destination type: %s, destination: %s\n", dest.Type, dest.Destination)
			}
		}()
	}
	notifyWg.Wait()
	os.Remove(faxReport)

	// Log completion of all notify dispatches
	q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
		"Notify",
		"notify dispatch completed",
		logrus.InfoLevel,
		map[string]interface{}{
			"uuid":              nFR.FaxJob.UUID.String(),
			"destination_count": len(destinations),
		},
	))
}

func firstPageTiff(uuid, inputPath string) (string, error) {

	outputPath := filepath.Join(gofaxlib.Config.Faxing.TempDir, fmt.Sprintf("first_%s.pdf", uuid))

	// Check if the file exists
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("TIFF file does not exist: %s", inputPath)
	}

	// Step 1: Convert entire TIFF to PDF
	cmd := exec.Command("convert",
		"-density", "300",
		"-compress", "lzw",
		"-quality", "100",
		"-background", "white",
		"-alpha", "remove",
		inputPath+"[0]",
		"-resize", "2550x3300>",
		outputPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to convert TIFF to PDF: %v, output: %s", err, string(output))
	}
	return outputPath, nil
}

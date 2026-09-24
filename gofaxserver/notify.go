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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/sirupsen/logrus"
)

type NotifyFaxResults struct {
	Results           map[string]*FaxJob `json:"results,omitempty"`
	FaxJob            *FaxJob            `json:"fax_job,omitempty"`
	AllAttemptsFailed bool               `json:"all_attempts_failed,omitempty"`
}

type NotifyDestination struct {
	Type        string `json:"type"`
	Destination string `json:"destination"`
}

// PortalStatusPayload is the compact status update POSTed to a `portal`
// notify destination (gofaxportal's /portal/api/notify/{svc_username}). Unlike
// the "webhook" notify type it carries no file data — just the final outcome,
// so the portal can flip a job to success/failed without waiting for its
// poller.
type PortalStatusPayload struct {
	UUID              string    `json:"uuid"`
	Success           bool      `json:"success"`
	AllAttemptsFailed bool      `json:"all_attempts_failed"`
	Attempts          int       `json:"attempts"`
	TransferredPages  uint      `json:"transferred_pages"`
	ResultText        string    `json:"result_text,omitempty"`
	HangupCause       string    `json:"hangup_cause,omitempty"`
	CallerIdNumber    string    `json:"caller_id_number,omitempty"`
	CalleeNumber      string    `json:"callee_number,omitempty"`
	StartTs           time.Time `json:"start_ts,omitempty"`
	EndTs             time.Time `json:"end_ts,omitempty"`
}

// buildPortalStatusPayload collapses all per-attempt results into one final
// outcome: the successful attempt wins if there is one, otherwise the latest
// attempt describes the failure. Pages are the max across attempts.
func (nfr *NotifyFaxResults) buildPortalStatusPayload() PortalStatusPayload {
	p := PortalStatusPayload{
		AllAttemptsFailed: nfr.AllAttemptsFailed,
		Attempts:          len(nfr.Results),
	}
	if nfr.FaxJob != nil {
		p.UUID = nfr.FaxJob.UUID.String()
		p.CallerIdNumber = nfr.FaxJob.CallerIdNumber
		p.CalleeNumber = nfr.FaxJob.CalleeNumber
	}

	var successJob, lastJob *FaxJob
	for _, job := range nfr.Results {
		if job == nil || job.Result == nil {
			continue
		}
		if job.Result.TransferredPages > p.TransferredPages {
			p.TransferredPages = job.Result.TransferredPages
		}
		if job.Result.Success && successJob == nil {
			successJob = job
		}
		if lastJob == nil || job.Result.EndTs.After(lastJob.Result.EndTs) {
			lastJob = job
		}
	}

	chosen := lastJob
	if successJob != nil {
		chosen = successJob
	}
	if chosen != nil {
		p.Success = chosen.Result.Success
		p.ResultText = chosen.Result.ResultText
		p.HangupCause = chosen.Result.HangupCause
		p.StartTs = chosen.Result.StartTs
		p.EndTs = chosen.Result.EndTs
	}
	return p
}

// emailSubjectBody builds a human-readable subject and plain-text body for
// fax notification emails from the collapsed job outcome, so recipients can
// see the result (direction, parties, pages, status, cause) without opening
// the attached report. kind tailors the attachment note ("email_report" vs
// the "email_full*" types which also attach the original fax).
func (nfr *NotifyFaxResults) emailSubjectBody(kind string) (subject, body string) {
	p := nfr.buildPortalStatusPayload()
	j := nfr.FaxJob

	// Direction and remote party. Reception/bridge results come from the
	// pre-queue call; outbound jobs are submitted via the API/portal.
	var phrase, direction string
	switch {
	case j.IsBridge:
		direction = "Bridged"
		phrase = fmt.Sprintf("Bridged fax %s → %s", j.CallerIdNumber, j.CalleeNumber)
	case j.SourceInfo.SourceType == "gateway":
		direction = "Received"
		phrase = fmt.Sprintf("Fax from %s", j.CallerIdNumber)
	default:
		direction = "Sent"
		phrase = fmt.Sprintf("Fax to %s", j.CalleeNumber)
	}

	pages := fmt.Sprintf("%d pages", p.TransferredPages)
	if p.TransferredPages == 1 {
		pages = "1 page"
	}

	if p.Success {
		subject = fmt.Sprintf("%s succeeded (%s)", phrase, pages)
	} else {
		cause := p.HangupCause
		if cause == "" {
			cause = p.ResultText
		}
		if cause == "" {
			cause = "unknown error"
		}
		subject = fmt.Sprintf("%s FAILED (%s)", phrase, cause)
	}

	status := "SUCCESS"
	if !p.Success {
		status = "FAILED"
	}

	from := j.CallerIdNumber
	if j.CallerIdName != "" {
		from = fmt.Sprintf("%s (%s)", j.CallerIdNumber, j.CallerIdName)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", subject)
	fmt.Fprintf(&b, "Status:    %s\n", status)
	fmt.Fprintf(&b, "Direction: %s\n", direction)
	fmt.Fprintf(&b, "From:      %s\n", from)
	fmt.Fprintf(&b, "To:        %s\n", j.CalleeNumber)
	fmt.Fprintf(&b, "Pages:     %d\n", p.TransferredPages)
	fmt.Fprintf(&b, "Attempts:  %d\n", p.Attempts)
	if p.ResultText != "" {
		fmt.Fprintf(&b, "Result:    %s\n", p.ResultText)
	}
	if p.HangupCause != "" {
		fmt.Fprintf(&b, "Cause:     %s\n", p.HangupCause)
	}
	if !p.StartTs.IsZero() {
		fmt.Fprintf(&b, "Started:   %s\n", p.StartTs.Format("2006-01-02 15:04:05 MST"))
	}
	if !p.EndTs.IsZero() {
		fmt.Fprintf(&b, "Completed: %s\n", p.EndTs.Format("2006-01-02 15:04:05 MST"))
	}
	fmt.Fprintf(&b, "Job UUID:  %s\n", p.UUID)
	if kind == "email_full" || kind == "email_full_failure" {
		b.WriteString("\nThe detailed fax report and the original fax are attached.\n")
	} else {
		b.WriteString("\nThe detailed fax report is attached.\n")
	}
	return subject, b.String()
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

		// Format timestamp; fall back to job status when no result exists.
		// (Notify-only jobs — failed receptions, bridged calls — always have
		// a result, but endpoint-less jobs must never panic here.)
		timestamp := ""
		resultText := faxJob.Status
		success := "failed"
		if faxJob.Result != nil {
			timestamp = faxJob.Result.EndTs.Format("2006-01-02 15:04:05")
			if faxJob.Result.ResultText != "" {
				resultText = faxJob.Result.ResultText
			} else if faxJob.Result.HangupCause != "" {
				// Failed receptions often carry only a hangup cause.
				resultText = faxJob.Result.HangupCause
			}
			if faxJob.Result.Success {
				success = "success"
			}
		}

		// Endpoint type is best-effort: notify-only jobs carry no endpoints.
		endpointType := "fax"
		if len(faxJob.Endpoints) > 0 && faxJob.Endpoints[0] != nil {
			endpointType = faxJob.Endpoints[0].EndpointType
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

// processNotifyDestinations resolves the notify destinations for a completed
// fax job. Number-level and tenant-level notify strings are MERGED (both
// fire) for the source side (sender receipts) and the destination side
// (recipient receipts), with duplicates removed by type+destination. The
// number lookup is attempted even when the tenant is unresolved, so a
// tenant-map miss (e.g. unresolvable tenant id) can never silently suppress
// a number's notify. Every decision is logged: a silently empty result here
// was a recurring production failure mode with zero log trail.
func (q *Queue) processNotifyDestinations(f *FaxJob) ([]NotifyDestination, error) {
	var notifyDestinations []NotifyDestination

	logf := func(level logrus.Level, msg string, fields map[string]interface{}) {
		if fields == nil {
			fields = map[string]interface{}{}
		}
		fields["uuid"] = f.UUID.String()
		q.server.LogManager.SendLog(q.server.LogManager.BuildLog("Notify.Resolve", msg, level, fields))
	}

	logf(logrus.InfoLevel, "resolving notify destinations", map[string]interface{}{
		"caller": f.CallerIdNumber, "callee": f.CalleeNumber,
		"src_tenant_id": f.SrcTenantID, "dst_tenant_id": f.DstTenantID,
	})

	// appendNotify parses a notify string and appends its destinations.
	// Malformed segments are skipped (and logged) rather than failing the
	// whole string.
	appendNotify := func(source, notifyStr string) {
		destinations, skipped := parseNotifyString(notifyStr)
		for _, seg := range skipped {
			logf(logrus.WarnLevel, "skipping malformed notify segment", map[string]interface{}{
				"source": source, "segment": seg, "notify": notifyStr,
			})
		}
		if len(destinations) > 0 {
			notifyDestinations = append(notifyDestinations, destinations...)
			logf(logrus.InfoLevel, "resolved notify destinations", map[string]interface{}{
				"source": source, "count": len(destinations), "types": destinationTypeCounts(destinations),
			})
		} else {
			logf(logrus.WarnLevel, "notify string produced no usable destinations", map[string]interface{}{
				"source": source, "notify": notifyStr,
			})
		}
	}

	// resolveSide resolves number-level and tenant-level notify for one side
	// of the job ("src" or "dst") and merges both.
	resolveSide := func(side string, tenantID uint, number string) {
		tenant := q.server.Tenants[tenantID]
		if tenant == nil && tenantID != 0 {
			logf(logrus.WarnLevel, "tenant not found in map; checking number directly", map[string]interface{}{
				"side": side, "tenant_id": tenantID,
			})
		}

		found := false
		if tn, err := q.server.getNumber(number); err != nil {
			logf(logrus.InfoLevel, "number not found; no number-level notify for this side", map[string]interface{}{
				"side": side, "number": number, "error": err.Error(),
			})
		} else if tn.Notify != "" {
			appendNotify(fmt.Sprintf("%s number %s", side, number), tn.Notify)
			found = true
		}

		if tenant != nil && tenant.Notify != "" {
			appendNotify(fmt.Sprintf("%s tenant %d (%s)", side, tenant.ID, tenant.Name), tenant.Notify)
			found = true
		}

		if !found {
			logf(logrus.InfoLevel, "no notify configured for side", map[string]interface{}{
				"side": side, "tenant_id": tenantID, "number": number,
			})
		}
	}

	// Source side: receipts for the sender (e.g. portal users assigned to the
	// calling number). Destination side: receipts for the recipients.
	resolveSide("src", f.SrcTenantID, f.CallerIdNumber)
	resolveSide("dst", f.DstTenantID, f.CalleeNumber)

	// Dedup by type+destination: on-net jobs resolve the same notify string
	// from both the src and dst sides.
	seen := make(map[string]struct{}, len(notifyDestinations))
	deduped := make([]NotifyDestination, 0, len(notifyDestinations))
	for _, d := range notifyDestinations {
		key := d.Type + "->" + d.Destination
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, d)
	}
	if len(deduped) != len(notifyDestinations) {
		logf(logrus.InfoLevel, "deduplicated notify destinations", map[string]interface{}{
			"before": len(notifyDestinations), "after": len(deduped),
		})
	}

	logf(logrus.InfoLevel, "notify destination resolution complete", map[string]interface{}{
		"total": len(deduped), "types": destinationTypeCounts(deduped),
	})
	return deduped, nil
}

// format of: email->shaun.agostinho@topsoffice.ca;shaun@dec0de.xyz,webhook->https://example.org/endpoint,portal->svc_username,gateway->TODO

// notifySegmentStart matches the beginning of a "type->" segment.
var notifySegmentStart = regexp.MustCompile(`^\s*[a-zA-Z_]+\s*->`)

// splitNotifySegments splits a notify string on commas — but only on commas
// that begin a new "type->" segment, so commas inside destinations (e.g.
// webhook URL query strings) don't corrupt the destination. (RE2 has no
// lookahead, hence the manual scan.)
func splitNotifySegments(notify string) []string {
	var segments []string
	start := 0
	for i := 0; i < len(notify); i++ {
		if notify[i] == ',' && notifySegmentStart.MatchString(notify[i+1:]) {
			segments = append(segments, notify[start:i])
			start = i + 1
		}
	}
	return append(segments, notify[start:])
}

// parseNotifyString parses a notify string into destinations. It is tolerant
// by design: malformed segments (no "->" separator, empty type/destination)
// are returned in skipped instead of failing the whole string — historically
// one bad segment silently discarded every destination, killing all
// notifications for the number/tenant with no log trail. The legacy "email"
// type (pre-email_report/email_full split) is normalized to "email_report".
func parseNotifyString(notify string) (destinations []NotifyDestination, skipped []string) {
	segments := splitNotifySegments(notify)
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		// Split each segment into type and destination using "->".
		parts := strings.SplitN(segment, "->", 2)
		if len(parts) != 2 {
			skipped = append(skipped, segment)
			continue
		}
		destType := strings.TrimSpace(parts[0])
		destValue := strings.TrimSpace(parts[1])
		if destType == "" || destValue == "" {
			skipped = append(skipped, segment)
			continue
		}
		// Legacy alias: "email->addr" predates the report/full split.
		if destType == "email" {
			destType = "email_report"
		}
		// Multiple destinations within one segment are separated by
		// semicolons; the full string is kept in Destination (dispatchers
		// split on ';'/',').
		destinations = append(destinations, NotifyDestination{
			Type:        destType,
			Destination: destValue,
		})
	}

	return destinations, skipped
}

// destinationTypeCounts summarizes destinations by type for compact logging.
func destinationTypeCounts(destinations []NotifyDestination) map[string]int {
	counts := make(map[string]int, len(destinations))
	for _, d := range destinations {
		counts[d.Type]++
	}
	return counts
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

	// Split the "to" field on semicolons or commas (legacy notify strings
	// sometimes comma-separate recipients) and trim spaces. Empty tokens are
	// dropped by FieldsFunc.
	recipients := strings.FieldsFunc(to, func(r rune) bool {
		return r == ';' || r == ','
	})
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
			map[string]interface{}{"uuid": nFR.FaxJob.UUID.String(), "pdf_path": faxReport, "error": err.Error()},
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
			case "email", "email_report":
				// "email" is the legacy alias for email_report (normally
				// normalized at parse time; kept here for robustness).
				subject, body := nFR.emailSubjectBody("email_report")
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
				if dest.Type == "email_full_failure" {
					if !nFR.AllAttemptsFailed {
						q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
							"Notify",
							"skipping email_full_failure: not all attempts failed",
							logrus.InfoLevel,
							map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
						))
						break
					}
				}

				subject, body := nFR.emailSubjectBody(dest.Type)

				attachments := []string{faxReport}
				pdf, err := tiffToPdf(nFR.FaxJob.FileName)
				if err != nil {
					// Report-only fallback: a conversion failure (e.g. a
					// partial TIFF from a failed reception, or a missing
					// file for a bridged call) must not kill the
					// notification entirely.
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("failed to convert tiff to pdf; sending report-only email: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String(), "file": nFR.FaxJob.FileName},
					))
				} else {
					attachments = append(attachments, pdf)
					defer func(name string) {
						if err := os.Remove(name); err != nil {
							q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
								"Notify",
								fmt.Sprintf("failed to remove full fax file: %s", name),
								logrus.ErrorLevel,
								map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
							))
						}
					}(pdf)
				}

				if err := SendEmailWithAttachment(subject, dest.Destination, body, attachments); err != nil {
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
			case "portal":
				// Push the final outcome to gofaxportal so it can update the
				// job immediately instead of waiting for its next poll. The
				// destination is the org's svc_username; auth mirrors inbound
				// delivery (svc username in the path + optional X-API-Key).
				portalBase := strings.TrimRight(gofaxlib.Config.Portal.URL, "/")
				if portalBase == "" {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						"portal notify destination configured but portal.url is empty, skipping",
						logrus.WarnLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
					break
				}

				payload, err := json.Marshal(nFR.buildPortalStatusPayload())
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("failed to marshal portal notify payload: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
					break
				}

				notifyURL := portalBase + "/portal/api/notify/" + dest.Destination
				req, err := http.NewRequest("POST", notifyURL, bytes.NewReader(payload))
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("error creating portal notify request: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
					break
				}
				req.Header.Set("Content-Type", "application/json")
				if gofaxlib.Config.Portal.APIKey != "" {
					req.Header.Set("X-API-Key", gofaxlib.Config.Portal.APIKey)
				}

				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Do(req)
				if err != nil {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("error sending portal notify request: %v", err),
						logrus.ErrorLevel,
						map[string]interface{}{"uuid": nFR.FaxJob.UUID.String()},
					))
					break
				}
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						"portal status notification sent successfully",
						logrus.InfoLevel,
						map[string]interface{}{
							"uuid":        nFR.FaxJob.UUID.String(),
							"destination": dest.Destination,
							"status_code": resp.StatusCode,
							"type":        "portal",
						},
					))
				} else {
					q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
						"Notify",
						fmt.Sprintf("portal notify endpoint responded with status %d", resp.StatusCode),
						logrus.ErrorLevel,
						map[string]interface{}{
							"uuid":        nFR.FaxJob.UUID.String(),
							"destination": dest.Destination,
						},
					))
				}
			case "gateway":
				// Gateway notifications are not implemented; log loudly
				// instead of silently "processing" nothing.
				q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
					"Notify",
					"gateway notify destination type is not implemented, skipping",
					logrus.WarnLevel,
					map[string]interface{}{
						"uuid":        nFR.FaxJob.UUID.String(),
						"destination": dest.Destination,
					},
				))
			default:
				q.server.LogManager.SendLog(q.server.LogManager.BuildLog(
					"Notify",
					"unknown notify destination type, skipping",
					logrus.WarnLevel,
					map[string]interface{}{
						"uuid":        nFR.FaxJob.UUID.String(),
						"type":        dest.Type,
						"destination": dest.Destination,
					},
				))
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
	// NB: 'magick' (IM7) not 'convert' (IM6) — source-built IM7 only ships 'magick'.
	// IM7 requires image OPERATORS (-alpha, -resize) to come AFTER the input;
	// only SETTINGS (-density, -background) may precede it. Putting '-alpha
	// remove' before the input fails with "no images found for operation".
	cmd := exec.Command("magick",
		"-density", "300",
		"-background", "white",
		inputPath+"[0]",
		"-alpha", "remove",
		"-resize", "2550x3300>",
		"-compress", "lzw",
		"-quality", "100",
		outputPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to convert TIFF to PDF: %v, output: %s", err, string(output))
	}
	return outputPath, nil
}

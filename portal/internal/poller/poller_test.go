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

package poller

import (
	"testing"
	"time"

	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"
)

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func row(success bool, attempt, pages int, resultText, cause string) fsclient.FaxStatusRow {
	return fsclient.FaxStatusRow{
		JobUUID: "j-1", ResultType: "transmission", AttemptNumber: attempt,
		StartTs: base.Add(time.Duration(attempt) * time.Minute),
		EndTs:   base.Add(time.Duration(attempt)*time.Minute + 30*time.Second),
		Success: success, TransferredPages: pages, ResultText: resultText, HangupCause: cause,
	}
}

func TestDecideSuccess(t *testing.T) {
	d := decide([]fsclient.FaxStatusRow{row(false, 1, 1, "NO ANSWER", ""), row(true, 2, 4, "OK", "")},
		false, true, true, base.Add(-time.Minute), base.Add(10*time.Minute))
	if !d.terminal || d.status != models.JobSuccess {
		t.Fatalf("want terminal success, got %+v", d)
	}
	if d.pages != 4 || d.attempts != 2 || d.resultText != "OK" {
		t.Fatalf("aggregation wrong: %+v", d)
	}
	if d.completed == nil {
		t.Fatal("completed timestamp required on success")
	}
}

func TestDecideFailedAfterSeenActiveAndGone(t *testing.T) {
	rows := []fsclient.FaxStatusRow{row(false, 1, 0, "", "NORMAL_CLEARING")}
	d := decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobFailed {
		t.Fatalf("want failed, got %+v", d)
	}
	if d.lastErr != "NORMAL_CLEARING" && d.lastErr == "" {
		t.Fatalf("last error should fall back to hangup cause: %+v", d)
	}
}

func TestDecideFailedWhenSnapshotUnavailable(t *testing.T) {
	rows := []fsclient.FaxStatusRow{row(false, 1, 0, "", "USER_BUSY")}
	d := decide(rows, false, false /* snapshot fetch failed */, false, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobFailed {
		t.Fatalf("snapshot-unavailable + rows should fail after grace, got %+v", d)
	}
}

func TestDecideStillQueuedBeforeGraceOrSighting(t *testing.T) {
	rows := []fsclient.FaxStatusRow{row(false, 1, 0, "", "NORMAL_CLEARING")}
	// Not yet past failure grace.
	d := decide(rows, false, true, true, base.Add(-10*time.Second), base)
	if d.terminal || d.status != models.JobSending {
		t.Fatalf("within grace should remain sending, got %+v", d)
	}
	// Never seen active and tracker reachable → could still start; keep queued.
	d = decide(rows, false, true, false, base.Add(-5*time.Minute), base)
	if d.terminal || d.status != models.JobQueued {
		t.Fatalf("unseen job with reachable tracker stays queued, got %+v", d)
	}
	// No rows at all, nothing seen.
	d = decide(nil, false, true, false, base.Add(-time.Hour), base)
	if d.terminal || d.status != models.JobQueued || d.attempts != 0 {
		t.Fatalf("silent job stays queued, got %+v", d)
	}
}

func TestDecideInFlightSending(t *testing.T) {
	d := decide(nil, true /* in active set */, true, false, base.Add(-time.Minute), base)
	if d.terminal || d.status != models.JobSending || !d.sawActive {
		t.Fatalf("in-flight job should be sending, got %+v", d)
	}
}

// placeholderRow mimics the synthetic result gofaxserver attaches at enqueue
// time (hangup cause "WEBHOOK"). It is not a real attempt.
func placeholderRow() fsclient.FaxStatusRow {
	return fsclient.FaxStatusRow{
		JobUUID: "j-1", ResultType: "reception",
		StartTs: base, EndTs: base.Add(2 * time.Second),
		Success: true, ResultText: "OK", HangupCause: placeholderHangupCause,
	}
}

func TestDecideIgnoresPlaceholderRows(t *testing.T) {
	// Regression: a placeholder success row followed by genuinely failed
	// attempts must resolve to failed, never success.
	rows := []fsclient.FaxStatusRow{
		placeholderRow(),
		row(false, 1, 0, "", "NORMAL_UNSPECIFIED"),
		row(false, 2, 0, "", "NORMAL_UNSPECIFIED"),
	}
	d := decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobFailed {
		t.Fatalf("placeholder + failed attempts should be failed, got %+v", d)
	}
	if d.attempts != 2 {
		t.Fatalf("placeholder row must not count as an attempt, got %+v", d)
	}
}

func TestDecidePlaceholderOnlyStaysQueued(t *testing.T) {
	// A placeholder row alone must not finalize the job.
	d := decide([]fsclient.FaxStatusRow{placeholderRow()},
		false, true, false, base.Add(-5*time.Minute), base)
	if d.terminal || d.status != models.JobQueued || d.attempts != 0 {
		t.Fatalf("placeholder-only job stays queued, got %+v", d)
	}
}

// submissionRow mimics the persisted intake leg (result_type "submission")
// now stored for portal/API-originated jobs. It keeps the placeholder hangup
// cause so terminal-state decisions keep ignoring it.
func submissionRow() fsclient.FaxStatusRow {
	return fsclient.FaxStatusRow{
		JobUUID: "j-1", ResultType: "submission", AttemptNumber: 0,
		StartTs: base, EndTs: base,
		Success: false, ResultText: "queued", HangupCause: placeholderHangupCause,
	}
}

func TestDecideIgnoresPersistedSubmissionRow(t *testing.T) {
	// A persisted submission leg followed by a successful transmission must
	// resolve to success with the transmission as the only attempt.
	rows := []fsclient.FaxStatusRow{
		submissionRow(),
		row(true, 1, 3, "OK", "NORMAL_CLEARING"),
	}
	d := decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobSuccess {
		t.Fatalf("submission + successful transmission should be success, got %+v", d)
	}
	if d.attempts != 1 {
		t.Fatalf("submission row must not count as an attempt, got %+v", d)
	}

	// Submission + failed transmission must resolve to failed (the
	// submission row alone never finalizes the job).
	rows = []fsclient.FaxStatusRow{
		submissionRow(),
		row(false, 1, 0, "", "NORMAL_UNSPECIFIED"),
	}
	d = decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobFailed {
		t.Fatalf("submission + failed transmission should be failed, got %+v", d)
	}
}

// processedSubmissionRow mimics the intake leg after the queue worker picked
// the job up (success=true, result_text "processed"). The WEBHOOK hangup
// cause is preserved, so terminal-state decisions must still ignore it.
func processedSubmissionRow() fsclient.FaxStatusRow {
	r := submissionRow()
	r.Success = true
	r.ResultText = "processed"
	return r
}

func TestDecideIgnoresProcessedSubmissionRow(t *testing.T) {
	// A processed (success=true) submission row must never make the job
	// succeed on its own...
	d := decide([]fsclient.FaxStatusRow{processedSubmissionRow()},
		false, true, false, base.Add(-5*time.Minute), base)
	if d.terminal || d.status != models.JobQueued || d.attempts != 0 {
		t.Fatalf("processed-submission-only job stays queued, got %+v", d)
	}

	// ...and must not mask a genuinely failed transmission.
	rows := []fsclient.FaxStatusRow{
		processedSubmissionRow(),
		row(false, 1, 0, "", "NORMAL_UNSPECIFIED"),
	}
	d = decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobFailed {
		t.Fatalf("processed submission + failed transmission should be failed, got %+v", d)
	}
	if d.attempts != 1 {
		t.Fatalf("processed submission row must not count as an attempt, got %+v", d)
	}
}

// notifyRow mimics a persisted notify-delivery leg (result_type "notify"):
// gofaxserver records each emailed report / webhook / portal push alongside
// the job's call legs. These are receipts, not fax attempts.
func notifyRow(success bool) fsclient.FaxStatusRow {
	return fsclient.FaxStatusRow{
		JobUUID: "j-1", ResultType: "notify", AttemptNumber: 0,
		StartTs: base.Add(3 * time.Minute), EndTs: base.Add(3 * time.Minute),
		Success: success, ResultText: "email_report -> ops@example.com: sent",
	}
}

func TestDecideIgnoresNotifyRows(t *testing.T) {
	// Regression guard: a successful email delivery about a FAILED fax must
	// never flip the job to success (and must not count as an attempt).
	rows := []fsclient.FaxStatusRow{
		row(false, 1, 0, "", "NORMAL_UNSPECIFIED"),
		notifyRow(true),
	}
	d := decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobFailed {
		t.Fatalf("failed fax + delivered receipt should be failed, got %+v", d)
	}
	if d.attempts != 1 {
		t.Fatalf("notify row must not count as an attempt, got %+v", d)
	}

	// And a failed webhook about a SUCCESSFUL fax must not flip it to failed.
	rows = []fsclient.FaxStatusRow{
		row(true, 1, 5, "OK", "NORMAL_CLEARING"),
		notifyRow(false),
	}
	d = decide(rows, false, true, true, base.Add(-5*time.Minute), base)
	if !d.terminal || d.status != models.JobSuccess {
		t.Fatalf("successful fax + failed receipt should be success, got %+v", d)
	}

	// A notify-only job (nothing else heard) stays queued.
	d = decide([]fsclient.FaxStatusRow{notifyRow(true)},
		false, true, false, base.Add(-5*time.Minute), base)
	if d.terminal || d.status != models.JobQueued || d.attempts != 0 {
		t.Fatalf("notify-only job stays queued, got %+v", d)
	}
}

func TestDecideTotalPages(t *testing.T) {
	// Total pages come from any non-notify row, including the submission
	// leg's intake TIFF count, so in-flight jobs already show the total.
	sub := submissionRow()
	sub.TotalPages = 15
	r := row(true, 1, 15, "OK", "NORMAL_CLEARING")
	r.TotalPages = 15
	d := decide([]fsclient.FaxStatusRow{sub, r, notifyRow(true)},
		false, true, true, base.Add(-5*time.Minute), base)
	if d.totalPages != 15 {
		t.Fatalf("total pages should be picked up across rows, got %+v", d)
	}

	// Intake count alone (no attempt rows yet) still surfaces.
	d = decide([]fsclient.FaxStatusRow{sub},
		false, true, false, base.Add(-time.Minute), base)
	if d.totalPages != 15 || d.terminal {
		t.Fatalf("in-flight job should carry the intake total, got %+v", d)
	}
}

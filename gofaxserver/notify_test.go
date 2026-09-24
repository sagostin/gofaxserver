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
	"testing"
	"time"

	"gofaxserver/gofaxlib"

	"github.com/google/uuid"
)

func attemptJob(success bool, pages uint, resultText, hangupCause string, end time.Time) *FaxJob {
	return &FaxJob{
		CallUUID: uuid.New(),
		Result: &gofaxlib.FaxResult{
			Success:          success,
			TransferredPages: pages,
			ResultText:       resultText,
			HangupCause:      hangupCause,
			StartTs:          end.Add(-time.Minute),
			EndTs:            end,
		},
	}
}

// TestBuildPortalStatusPayloadSuccessWins: a successful attempt decides the
// outcome even when a later attempt failed; pages are the max across attempts.
func TestBuildPortalStatusPayloadSuccessWins(t *testing.T) {
	jobUUID := uuid.New()
	now := time.Now()
	nfr := NotifyFaxResults{
		FaxJob: &FaxJob{UUID: jobUUID, CallerIdNumber: "+17785559876", CalleeNumber: "+16045551212"},
		Results: map[string]*FaxJob{
			"a": attemptJob(true, 4, "OK", "NORMAL_CLEARING", now.Add(-time.Minute)),
			"b": attemptJob(false, 2, "NO ANSWER", "NO_ANSWER", now),
		},
	}
	p := nfr.buildPortalStatusPayload()
	if p.UUID != jobUUID.String() {
		t.Errorf("uuid mismatch: %q", p.UUID)
	}
	if !p.Success {
		t.Error("expected success when any attempt succeeded")
	}
	if p.ResultText != "OK" || p.HangupCause != "NORMAL_CLEARING" {
		t.Errorf("expected fields from the successful attempt, got %+v", p)
	}
	if p.TransferredPages != 4 {
		t.Errorf("pages should be the max across attempts, got %d", p.TransferredPages)
	}
	if p.Attempts != 2 {
		t.Errorf("attempts mismatch: %d", p.Attempts)
	}
	if p.CallerIdNumber != "+17785559876" || p.CalleeNumber != "+16045551212" {
		t.Errorf("numbers mismatch: %+v", p)
	}
}

// TestBuildPortalStatusPayloadAllFailed: with no success, the latest attempt
// describes the failure.
func TestBuildPortalStatusPayloadAllFailed(t *testing.T) {
	now := time.Now()
	nfr := NotifyFaxResults{
		FaxJob:            &FaxJob{UUID: uuid.New()},
		AllAttemptsFailed: true,
		Results: map[string]*FaxJob{
			"a": attemptJob(false, 0, "BUSY", "USER_BUSY", now.Add(-time.Minute)),
			"b": attemptJob(false, 1, "NO ANSWER", "NO_ANSWER", now),
		},
	}
	p := nfr.buildPortalStatusPayload()
	if p.Success {
		t.Error("expected failure")
	}
	if !p.AllAttemptsFailed {
		t.Error("all_attempts_failed must propagate")
	}
	if p.ResultText != "NO ANSWER" || p.HangupCause != "NO_ANSWER" {
		t.Errorf("expected fields from the latest attempt, got %+v", p)
	}
	if !p.EndTs.Equal(now) {
		t.Errorf("end_ts should come from the latest attempt: %v", p.EndTs)
	}
}

// TestBuildPortalStatusPayloadNoResults: attempts without results (or none at
// all) must not panic and yield a zero-value outcome.
func TestBuildPortalStatusPayloadNoResults(t *testing.T) {
	nfr := NotifyFaxResults{
		FaxJob:  &FaxJob{UUID: uuid.New()},
		Results: map[string]*FaxJob{"a": {CallUUID: uuid.New()}},
	}
	p := nfr.buildPortalStatusPayload()
	if p.Success || p.ResultText != "" || p.TransferredPages != 0 {
		t.Errorf("expected zero-value outcome, got %+v", p)
	}
	if p.Attempts != 1 {
		t.Errorf("attempts mismatch: %d", p.Attempts)
	}
}

// ---------------------------------------------------------------------------
// parseNotifyString
// ---------------------------------------------------------------------------

func destTypes(dests []NotifyDestination) []string {
	out := make([]string, len(dests))
	for i, d := range dests {
		out[i] = d.Type
	}
	return out
}

func findDest(dests []NotifyDestination, typ string) *NotifyDestination {
	for i := range dests {
		if dests[i].Type == typ {
			return &dests[i]
		}
	}
	return nil
}

func TestParseNotifyStringAllTypes(t *testing.T) {
	dests, skipped := parseNotifyString("email_report->a@b.com;c@d.com,webhook->https://example.org/hook,portal->svc_acme")
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped segments: %v", skipped)
	}
	if len(dests) != 3 {
		t.Fatalf("expected 3 destinations, got %d (%v)", len(dests), destTypes(dests))
	}
	if d := findDest(dests, "email_report"); d == nil || d.Destination != "a@b.com;c@d.com" {
		t.Errorf("email_report destination wrong: %+v", d)
	}
	if d := findDest(dests, "portal"); d == nil || d.Destination != "svc_acme" {
		t.Errorf("portal destination wrong: %+v", d)
	}
}

// TestParseNotifyStringCommaInURL: a comma inside a webhook URL must not
// split the destination (only commas starting a new type-> segment split).
func TestParseNotifyStringCommaInURL(t *testing.T) {
	dests, skipped := parseNotifyString("webhook->https://example.org/hook?a=1,b=2,portal->svc_acme")
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped segments: %v", skipped)
	}
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations, got %d (%v)", len(dests), destTypes(dests))
	}
	if d := findDest(dests, "webhook"); d == nil || d.Destination != "https://example.org/hook?a=1,b=2" {
		t.Errorf("webhook URL was corrupted: %+v", d)
	}
}

// TestParseNotifyStringLegacyEmailAlias: the old "email" type is normalized
// to email_report so pre-split notify strings keep working.
func TestParseNotifyStringLegacyEmailAlias(t *testing.T) {
	dests, skipped := parseNotifyString("email->a@b.com;c@d.com")
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped segments: %v", skipped)
	}
	if len(dests) != 1 || dests[0].Type != "email_report" {
		t.Fatalf("expected legacy email to normalize to email_report, got %v", destTypes(dests))
	}
}

// TestParseNotifyStringSkipsMalformed: one bad segment must not kill the
// rest of the string (the old behavior discarded everything, silently).
func TestParseNotifyStringSkipsMalformed(t *testing.T) {
	dests, skipped := parseNotifyString("email_report->,portal->svc_acme")
	if len(dests) != 1 || dests[0].Type != "portal" {
		t.Errorf("expected only the portal destination, got %v", destTypes(dests))
	}
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped segment, got %v", skipped)
	}
}

func TestParseNotifyStringGarbage(t *testing.T) {
	dests, skipped := parseNotifyString("not-a-notify-string")
	if len(dests) != 0 {
		t.Errorf("expected no destinations, got %v", destTypes(dests))
	}
	if len(skipped) != 1 {
		t.Errorf("expected the garbage segment to be skipped, got %v", skipped)
	}
}

func TestParseNotifyStringEmpty(t *testing.T) {
	dests, skipped := parseNotifyString("")
	if len(dests) != 0 || len(skipped) != 0 {
		t.Errorf("expected nothing from empty string, got %v / %v", dests, skipped)
	}
}

// ---------------------------------------------------------------------------
// processNotifyDestinations
// ---------------------------------------------------------------------------

// newNotifyTestQueue builds a Queue whose server has the given tenants and
// numbers in memory, with a working (Loki-disabled) LogManager.
func newNotifyTestQueue(tenants map[uint]*Tenant, numbers map[string]*TenantNumber) *Queue {
	lm := gofaxlib.NewLogManager(gofaxlib.NewLokiClient())
	s := &Server{
		LogManager:    lm,
		Tenants:       tenants,
		TenantNumbers: numbers,
	}
	return &Queue{server: s}
}

func notifyJob(srcTenant, dstTenant uint, caller, callee string) *FaxJob {
	return &FaxJob{
		UUID:           uuid.New(),
		CallUUID:       uuid.New(),
		SrcTenantID:    srcTenant,
		DstTenantID:    dstTenant,
		CallerIdNumber: caller,
		CalleeNumber:   callee,
	}
}

// Number-level notify resolves for the source side (sender receipts).
func TestProcessNotifyDestinationsNumberNotify(t *testing.T) {
	q := newNotifyTestQueue(
		map[uint]*Tenant{1: {ID: 1, Name: "acme"}},
		map[string]*TenantNumber{
			"5551234567": {ID: 9, TenantID: 1, Number: "5551234567", Notify: "email_report->user@acme.com,portal->svc_acme"},
		},
	)
	dests, err := q.processNotifyDestinations(notifyJob(1, 0, "5551234567", "8005559999"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations, got %v", destTypes(dests))
	}
	if findDest(dests, "email_report") == nil || findDest(dests, "portal") == nil {
		t.Errorf("missing expected destinations: %+v", dests)
	}
}

// A number with no notify string falls back to the tenant-level notify.
func TestProcessNotifyDestinationsTenantFallback(t *testing.T) {
	q := newNotifyTestQueue(
		map[uint]*Tenant{1: {ID: 1, Name: "acme", Notify: "email_full_failure->ops@acme.com"}},
		map[string]*TenantNumber{
			"5551234567": {ID: 9, TenantID: 1, Number: "5551234567"},
		},
	)
	dests, err := q.processNotifyDestinations(notifyJob(0, 1, "8005559999", "5551234567"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 1 || dests[0].Type != "email_full_failure" || dests[0].Destination != "ops@acme.com" {
		t.Fatalf("expected tenant fallback destination, got %+v", dests)
	}
}

// Number AND tenant notify strings merge (both fire) instead of the number
// replacing the tenant — this is what keeps tenant-wide email_full_failure
// working for portal-managed numbers.
func TestProcessNotifyDestinationsMergesNumberAndTenant(t *testing.T) {
	q := newNotifyTestQueue(
		map[uint]*Tenant{1: {ID: 1, Name: "acme", Notify: "email_full_failure->ops@acme.com"}},
		map[string]*TenantNumber{
			"5551234567": {ID: 9, TenantID: 1, Number: "5551234567", Notify: "email_report->user@acme.com,portal->svc_acme"},
		},
	)
	dests, err := q.processNotifyDestinations(notifyJob(1, 0, "5551234567", "8005559999"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 3 {
		t.Fatalf("expected merged destinations (email_report, portal, email_full_failure), got %v", destTypes(dests))
	}
	if findDest(dests, "email_full_failure") == nil {
		t.Errorf("tenant-level email_full_failure was not merged: %+v", dests)
	}
}

// Identical type+destination pairs (e.g. the same notify string on number
// and tenant, or on-net jobs resolving both sides) are deduplicated.
func TestProcessNotifyDestinationsDedup(t *testing.T) {
	notify := "email_report->user@acme.com,portal->svc_acme"
	q := newNotifyTestQueue(
		map[uint]*Tenant{1: {ID: 1, Name: "acme", Notify: notify}},
		map[string]*TenantNumber{
			"5551234567": {ID: 9, TenantID: 1, Number: "5551234567", Notify: notify},
		},
	)
	// On-net job: src and dst resolve the same tenant/number.
	dests, err := q.processNotifyDestinations(notifyJob(1, 1, "5551234567", "5551234567"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 2 {
		t.Fatalf("expected 2 deduplicated destinations, got %d: %+v", len(dests), dests)
	}
}

// A tenant-map miss (unresolved tenant id) must not suppress a number's
// notify — the number is looked up independently.
func TestProcessNotifyDestinationsTenantMissingNumberStillResolves(t *testing.T) {
	q := newNotifyTestQueue(
		map[uint]*Tenant{},
		map[string]*TenantNumber{
			"5551234567": {ID: 9, TenantID: 1, Number: "5551234567", Notify: "email_report->user@acme.com"},
		},
	)
	dests, err := q.processNotifyDestinations(notifyJob(0, 0, "5551234567", "8005559999"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 1 || dests[0].Type != "email_report" {
		t.Fatalf("expected the number-level destination despite missing tenant, got %+v", dests)
	}
}

// Legacy email-> strings stored out-of-band keep working via the alias.
func TestProcessNotifyDestinationsLegacyAlias(t *testing.T) {
	q := newNotifyTestQueue(
		map[uint]*Tenant{},
		map[string]*TenantNumber{
			"5551234567": {ID: 9, TenantID: 1, Number: "5551234567", Notify: "email->legacy@acme.com"},
		},
	)
	dests, err := q.processNotifyDestinations(notifyJob(0, 1, "8005559999", "5551234567"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 1 || dests[0].Type != "email_report" || dests[0].Destination != "legacy@acme.com" {
		t.Fatalf("expected legacy alias destination, got %+v", dests)
	}
}

// No tenant, no number, no notify: empty result, no error, no panic.
func TestProcessNotifyDestinationsNone(t *testing.T) {
	q := newNotifyTestQueue(map[uint]*Tenant{}, map[string]*TenantNumber{})
	dests, err := q.processNotifyDestinations(notifyJob(0, 0, "8005551111", "8005559999"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dests) != 0 {
		t.Fatalf("expected no destinations, got %+v", dests)
	}
}

// GenerateFaxResultsPDF must not panic for endpoint-less jobs (notify-only
// failed receptions / bridged calls) or attempts without results.
func TestGenerateFaxResultsPDFEndpointless(t *testing.T) {
	dir := t.TempDir()
	old := gofaxlib.Config.Faxing.TempDir
	gofaxlib.Config.Faxing.TempDir = dir
	t.Cleanup(func() { gofaxlib.Config.Faxing.TempDir = old })

	job := &FaxJob{
		UUID:           uuid.New(),
		CallUUID:       uuid.New(),
		CallerIdNumber: "8005551111",
		CalleeNumber:   "5551234567",
		Status:         "FAILED",
		Result: &gofaxlib.FaxResult{
			Success:     false,
			ResultText:  "NO ANSWER",
			HangupCause: "NO_ANSWER",
			EndTs:       time.Now(),
		},
		SourceInfo: FaxSourceInfo{Timestamp: time.Now()},
	}
	nfr := NotifyFaxResults{
		FaxJob:            job,
		AllAttemptsFailed: true,
		Results:           map[string]*FaxJob{job.CallUUID.String(): job},
	}
	path, err := nfr.GenerateFaxResultsPDF()
	if err != nil {
		t.Fatalf("GenerateFaxResultsPDF failed for endpoint-less job: %v", err)
	}
	if path == "" {
		t.Fatal("expected a report path")
	}
}

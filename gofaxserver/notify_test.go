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

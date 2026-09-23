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

package api

import (
	"encoding/json"
	"testing"

	"gofaxportal/internal/config"

	"github.com/kataras/iris/v12/httptest"
)

// TestStatusNotifyPayloadDecodesUpstreamShape guards the JSON contract with
// gofaxserver's PortalStatusPayload (see gofaxserver/notify.go). If
// gofaxserver's field tags change, this test should be updated in step.
func TestStatusNotifyPayloadDecodesUpstreamShape(t *testing.T) {
	raw := `{
		"uuid": "3f7c1f2e-0000-4a1b-9c2d-0123456789ab",
		"success": true,
		"all_attempts_failed": false,
		"attempts": 2,
		"transferred_pages": 4,
		"result_text": "OK",
		"hangup_cause": "NORMAL_CLEARING",
		"caller_id_number": "+17785559876",
		"callee_number": "+16045551212",
		"start_ts": "2026-09-23T12:34:00Z",
		"end_ts": "2026-09-23T12:36:30Z"
	}`
	var p statusNotifyPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.UUID != "3f7c1f2e-0000-4a1b-9c2d-0123456789ab" {
		t.Errorf("uuid mismatch: %q", p.UUID)
	}
	if !p.Success || p.AllAttemptsFailed {
		t.Errorf("success flags mismatch: %+v", p)
	}
	if p.Attempts != 2 {
		t.Errorf("attempts mismatch: %d", p.Attempts)
	}
	if p.TransferredPages != 4 {
		t.Errorf("transferred_pages mismatch: %d", p.TransferredPages)
	}
	if p.ResultText != "OK" || p.HangupCause != "NORMAL_CLEARING" {
		t.Errorf("result fields mismatch: %+v", p)
	}
	if p.StartTs.IsZero() || p.EndTs.IsZero() {
		t.Error("timestamps must parse")
	}
}

// TestStatusNotifyRejectsBadAPIKey exercises the pre-shared-key gate; it must
// fire before any database access, so it runs against a DB-less server.
func TestStatusNotifyRejectsBadAPIKey(t *testing.T) {
	cfg := &config.Config{InboundAPIKey: "secret-key"}
	s := &Server{Cfg: cfg}
	e := httptest.New(t, s.BuildApp())

	e.POST("/portal/api/notify/svc_acme_x").WithJSON(map[string]string{"uuid": "x"}).
		Expect().Status(401)
	e.POST("/portal/api/notify/svc_acme_x").WithHeader("X-API-Key", "wrong").
		WithJSON(map[string]string{"uuid": "x"}).Expect().Status(401)
}

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

// TestInboundPayloadDecodesUpstreamShape guards the JSON contract with
// gofaxserver's FaxJobWithFile (see gofaxserver/queue.go): FaxJob fields are
// flattened and file_data carries the base64 PDF. If gofaxserver's field
// tags change, this test should be updated in step.
func TestInboundPayloadDecodesUpstreamShape(t *testing.T) {
	raw := `{
		"uuid": "3f7c1f2e-0000-4a1b-9c2d-0123456789ab",
		"call_uuid": "aaaa1f2e-0000-4a1b-9c2d-0123456789ab",
		"number": "+16045551212",
		"cidnum": "+17785559876",
		"cidname": "Some Sender",
		"filename": "/var/lib/gofaxserver/tmp/fax_x.tiff",
		"npages": 3,
		"status": "Completed",
		"ts": "2026-09-23T12:34:56Z",
		"file_data": "JVBERi0xLjQK"
	}`
	var p inboundPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.UUID != "3f7c1f2e-0000-4a1b-9c2d-0123456789ab" {
		t.Errorf("uuid mismatch: %q", p.UUID)
	}
	if p.CalleeNumber != "+16045551212" {
		t.Errorf("callee (number) mismatch: %q", p.CalleeNumber)
	}
	if p.CallerNumber != "+17785559876" {
		t.Errorf("caller (cidnum) mismatch: %q", p.CallerNumber)
	}
	if p.CallerName != "Some Sender" {
		t.Errorf("caller name (cidname) mismatch: %q", p.CallerName)
	}
	if p.Pages != 3 {
		t.Errorf("pages (npages) mismatch: %d", p.Pages)
	}
	if p.Ts.IsZero() {
		t.Error("ts must parse")
	}
	if p.FileData != "JVBERi0xLjQK" {
		t.Errorf("file_data mismatch: %q", p.FileData)
	}
}

// TestInboundRejectsBadAPIKey exercises the pre-shared-key gate; it must
// fire before any database access, so it runs against a DB-less server.
func TestInboundRejectsBadAPIKey(t *testing.T) {
	cfg := &config.Config{InboundAPIKey: "secret-key"}
	s := &Server{Cfg: cfg}
	e := httptest.New(t, s.BuildApp())

	e.POST("/portal/api/inbound/svc_acme_x").WithJSON(map[string]string{"uuid": "x"}).
		Expect().Status(401)
	e.POST("/portal/api/inbound/svc_acme_x").WithHeader("X-API-Key", "wrong").
		WithJSON(map[string]string{"uuid": "x"}).Expect().Status(401)
}

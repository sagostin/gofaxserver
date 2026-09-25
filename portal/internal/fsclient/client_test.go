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

package fsclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendFaxParsesJobUUID(t *testing.T) {
	var gotCaller, gotCallee, gotAuth, gotSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fax/send" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		gotCaller = r.FormValue("caller_number")
		gotCallee = r.FormValue("callee_number")
		gotSource = r.FormValue("source")
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("missing file: %v", err)
			return
		}
		file.Close()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "fax enqueued", "job_uuid": "abc-123"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	uuid, err := c.SendFax("svc_user", "svc_pass", "doc.pdf", []byte("%PDF-fake"), "5551234567", "5559876543")
	if err != nil {
		t.Fatalf("SendFax: %v", err)
	}
	if uuid != "abc-123" {
		t.Fatalf("uuid = %q", uuid)
	}
	if gotCaller != "5551234567" || gotCallee != "5559876543" {
		t.Fatalf("form values wrong: caller=%q callee=%q", gotCaller, gotCallee)
	}
	if gotSource != "portal" {
		t.Fatalf("source marker wrong: got %q, want %q", gotSource, "portal")
	}
	if gotAuth == "" {
		t.Fatal("basic auth header missing")
	}
}

func TestAPIErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported file type"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.SendFax("u", "p", "x.txt", []byte("data"), "5551234567", "5559876543")
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if ae.Status != http.StatusBadRequest || ae.Message != "unsupported file type" {
		t.Fatalf("bad APIError: %+v", ae)
	}
}

func TestListTenantsAndUsers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/tenants":
			_, _ = w.Write([]byte(`[{"id":7,"name":"Acme","notify":"","numbers":[{"id":9,"tenant_id":7,"number":"5551234567"}]}]`))
		case "/admin/users":
			if r.URL.Query().Get("tenant_id") != "7" {
				t.Errorf("tenant_id filter not forwarded: %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"id":3,"tenant_id":7,"username":"svc_acme_ab12","api_key":"k"}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "testkey")
	tenants, err := c.ListTenants()
	if err != nil || len(tenants) != 1 || tenants[0].ID != 7 || len(tenants[0].Numbers) != 1 {
		t.Fatalf("tenants = %+v err %v", tenants, err)
	}
	users, err := c.ListUsers(7)
	if err != nil || len(users) != 1 || users[0].Username != "svc_acme_ab12" {
		t.Fatalf("users = %+v err %v", users, err)
	}
}

func TestGetFaxStatusRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("uuid") != "j-1" {
			t.Errorf("uuid not forwarded")
		}
		_, _ = w.Write([]byte(`[
			{"job_uuid":"j-1","result_type":"transmission","attempt_number":1,"success":false,"hangup_cause":"NO_ANSWER"},
			{"job_uuid":"j-1","result_type":"transmission","attempt_number":2,"success":true,"transferred_pages":3,"result_text":"OK"}
		]`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL, "").GetFaxStatus("u", "p", "j-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[1].Success || rows[1].TransferredPages != 3 || rows[0].HangupCause != "NO_ANSWER" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCreateTenantUserSendsPayload(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/user" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"id":11,"tenant_id":2,"username":"svc_x","api_key":"ak"}`))
	}))
	defer srv.Close()

	u, err := New(srv.URL, "").CreateTenantUser(2, "svc_x", "pw", "ak")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 11 {
		t.Fatalf("user id = %d", u.ID)
	}
	if body["tenant_id"].(float64) != 2 || body["password"] != "pw" || body["api_key"] != "ak" {
		t.Fatalf("payload = %+v", body)
	}
}

func TestListFaxResultsForwardsQueryAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/fax-results" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("tenant_id") != "7" || q.Get("result_type") != "bridge" || q.Get("success") != "true" {
			t.Errorf("query not forwarded: %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"total":1,"items":[{"job_uuid":"j-9","src_tenant_id":7,"dst_tenant_id":2,"attempts":2,"leg_types":["bridge"],"success":true,"portal":true,"transferred_pages":2,"legs":[{"id":5,"job_uuid":"j-9","result_type":"bridge","success":true,"is_bridge":true,"bridge_direction":"pbx_to_upstream","gateway":"carrier1","transferred_pages":2}]}]}`))
	}))
	defer srv.Close()

	out, err := New(srv.URL, "k").ListFaxResults(map[string][]string{
		"tenant_id": {"7"}, "result_type": {"bridge"}, "success": {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("out = %+v", out)
	}
	g := out.Items[0]
	if g.JobUUID != "j-9" || g.SrcTenantID != 7 || !g.Success || g.Attempts != 2 || len(g.LegTypes) != 1 {
		t.Fatalf("group = %+v", g)
	}
	if !g.Portal {
		t.Fatalf("portal flag not decoded: %+v", g)
	}
	if len(g.Legs) != 1 {
		t.Fatalf("legs = %+v", g.Legs)
	}
	r := g.Legs[0]
	if !r.IsBridge || r.BridgeDirection != "pbx_to_upstream" || r.TransferredPages != 2 {
		t.Fatalf("leg = %+v", r)
	}
	if r.Gateway != "carrier1" {
		t.Fatalf("gateway not decoded: %+v", r)
	}
}

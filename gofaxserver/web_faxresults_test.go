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
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseFaxResultQueryDefaults(t *testing.T) {
	q, err := parseFaxResultQuery(url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if q.Limit != 50 || q.Offset != 0 {
		t.Errorf("defaults: got limit=%d offset=%d", q.Limit, q.Offset)
	}
	if q.Success != nil || q.JobUUID != nil || q.From != nil || q.To != nil {
		t.Errorf("unexpected non-nil filters: %+v", q)
	}
}

func TestParseFaxResultQueryFilters(t *testing.T) {
	id := uuid.New()
	q, err := parseFaxResultQuery(url.Values{
		"tenant_id":   {"7"},
		"result_type": {"bridge"},
		"success":     {"true"},
		"number":      {"2507"},
		"job_uuid":    {id.String()},
		"from":        {"2026-09-01"},
		"to":          {"2026-09-25"},
		"limit":       {"200"},
		"offset":      {"40"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.TenantID != 7 || q.ResultType != "bridge" || q.Number != "2507" {
		t.Errorf("scalar filters wrong: %+v", q)
	}
	if q.Success == nil || !*q.Success {
		t.Errorf("success filter wrong: %+v", q.Success)
	}
	if q.JobUUID == nil || *q.JobUUID != id {
		t.Errorf("job_uuid filter wrong: %+v", q.JobUUID)
	}
	if q.From == nil || q.To == nil {
		t.Fatal("time bounds not parsed")
	}
	if q.Limit != 200 || q.Offset != 40 {
		t.Errorf("pagination wrong: limit=%d offset=%d", q.Limit, q.Offset)
	}
}

func TestParseFaxResultQueryLimitClamped(t *testing.T) {
	q, err := parseFaxResultQuery(url.Values{"limit": {"99999"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.Limit != 500 {
		t.Errorf("limit should clamp to 500, got %d", q.Limit)
	}
}

func TestParseFaxResultQueryRejectsBadInput(t *testing.T) {
	for name, v := range map[string]url.Values{
		"bad result_type": {"result_type": {"bogus"}},
		"bad success":     {"success": {"maybe"}},
		"bad job_uuid":    {"job_uuid": {"not-a-uuid"}},
		"bad from":        {"from": {"yesterday"}},
		"bad to":          {"to": {"2026-13-99"}},
	} {
		if _, err := parseFaxResultQuery(v); err == nil {
			t.Errorf("%s: expected error, got none", name)
		}
	}
}

func TestParseTimeBoundDateOnly(t *testing.T) {
	start, err := parseTimeBound("2026-09-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if start.Hour() != 0 {
		t.Errorf("start bound should be midnight, got %v", start)
	}
	end, err := parseTimeBound("2026-09-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if end.Sub(start) != 24*time.Hour-time.Nanosecond {
		t.Errorf("end bound should be end of day, got %v", end)
	}
	if _, err := parseTimeBound("2026-09-01T10:00:00Z", false); err != nil {
		t.Errorf("RFC3339 should parse: %v", err)
	}
}

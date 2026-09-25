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

func leg(resultType string, attempt int, success bool, at time.Time) FaxJobResult {
	return FaxJobResult{
		ResultType:    resultType,
		AttemptNumber: attempt,
		Success:       success,
		Status:        map[bool]string{true: "OK", false: "FAILED"}[success],
		CreatedAt:     at,
	}
}

func TestDeriveGroupEmpty(t *testing.T) {
	id := uuid.New()
	g := deriveGroup(id, nil)
	if g.JobUUID != id || g.Attempts != 0 || len(g.Legs) != 0 || g.Success {
		t.Errorf("empty group wrong: %+v", g)
	}
}

func TestDeriveGroupRetryThenSuccess(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	legs := []FaxJobResult{
		leg("transmission", 1, false, base),
		leg("transmission", 2, false, base.Add(time.Minute)),
		leg("transmission", 3, true, base.Add(2*time.Minute)),
	}
	legs[2].TransferredPages, legs[2].TotalPages = 5, 5
	g := deriveGroup(id, legs)
	if !g.Success {
		t.Error("final attempt succeeded: job should be success")
	}
	if g.Attempts != 3 || len(g.Legs) != 3 {
		t.Errorf("attempts wrong: %+v", g)
	}
	if g.TransferredPages != 5 || g.TotalPages != 5 {
		t.Errorf("pages should come from final leg: %+v", g)
	}
	if !g.FirstTs.Equal(base) || !g.LastTs.Equal(base.Add(2*time.Minute)) {
		t.Errorf("time bounds wrong: %v → %v", g.FirstTs, g.LastTs)
	}
	if len(g.LegTypes) != 1 || g.LegTypes[0] != "transmission" {
		t.Errorf("leg types wrong: %v", g.LegTypes)
	}
}

func TestDeriveGroupAllFailed(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	g := deriveGroup(uuid.New(), []FaxJobResult{
		leg("transmission", 1, false, base),
		leg("transmission", 2, false, base.Add(time.Minute)),
	})
	if g.Success {
		t.Error("all attempts failed: job should be failed")
	}
}

func TestDeriveGroupDeliveryDoesNotDecide(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	g := deriveGroup(uuid.New(), []FaxJobResult{
		leg("reception", 1, true, base),
		leg("delivery", 1, false, base.Add(time.Minute)),
	})
	if !g.Success {
		t.Error("later failed delivery leg must not override successful reception")
	}
	if len(g.LegTypes) != 2 {
		t.Errorf("leg types wrong: %v", g.LegTypes)
	}
}

func TestDeriveGroupDeliveryOnlyFallback(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	g := deriveGroup(uuid.New(), []FaxJobResult{
		leg("delivery", 1, true, base),
		leg("delivery", 2, true, base.Add(time.Minute)),
	})
	if !g.Success {
		t.Error("delivery-only job should fall back to latest leg")
	}
}

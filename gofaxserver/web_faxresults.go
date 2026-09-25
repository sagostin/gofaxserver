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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"gorm.io/gorm"
)

// faxResultQuery holds the validated filters for listing FaxJobResult rows.
type faxResultQuery struct {
	TenantID   uint       // matches src_tenant_id OR dst_tenant_id
	ResultType string     // reception | bridge | transmission | delivery
	Success    *bool      // nil = no filter
	Number     string     // substring match on caller or callee number
	JobUUID    *uuid.UUID // exact job correlation
	From       *time.Time // created_at >= from
	To         *time.Time // created_at <= to
	Limit      int
	Offset     int
}

var faxResultTypes = map[string]bool{
	"reception":    true,
	"bridge":       true,
	"transmission": true,
	"delivery":     true,
}

// parseTimeBound accepts RFC3339 or a bare YYYY-MM-DD date.
func parseTimeBound(v string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q (want RFC3339 or YYYY-MM-DD)", v)
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t, nil
}

// parseFaxResultQuery validates raw query params into a faxResultQuery.
func parseFaxResultQuery(v url.Values) (faxResultQuery, error) {
	q := faxResultQuery{
		Limit:  intDefault(v.Get("limit"), 50),
		Offset: intDefault(v.Get("offset"), 0),
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	if tid := intDefault(v.Get("tenant_id"), 0); tid > 0 {
		q.TenantID = uint(tid)
	}

	if rt := v.Get("result_type"); rt != "" {
		if !faxResultTypes[rt] {
			return q, fmt.Errorf("invalid result_type %q", rt)
		}
		q.ResultType = rt
	}

	if sv := v.Get("success"); sv != "" {
		switch sv {
		case "true":
			b := true
			q.Success = &b
		case "false":
			b := false
			q.Success = &b
		default:
			return q, errors.New("success must be true or false")
		}
	}

	q.Number = v.Get("number")

	if ju := v.Get("job_uuid"); ju != "" {
		id, err := uuid.Parse(ju)
		if err != nil {
			return q, fmt.Errorf("invalid job_uuid: %v", err)
		}
		q.JobUUID = &id
	}

	if f := v.Get("from"); f != "" {
		t, err := parseTimeBound(f, false)
		if err != nil {
			return q, err
		}
		q.From = &t
	}
	if to := v.Get("to"); to != "" {
		t, err := parseTimeBound(to, true)
		if err != nil {
			return q, err
		}
		q.To = &t
	}
	return q, nil
}

func intDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// apply attaches the filters to a gorm query on the fax_job_results table.
func (q faxResultQuery) apply(db *gorm.DB) *gorm.DB {
	if q.TenantID > 0 {
		db = db.Where("src_tenant_id = ? OR dst_tenant_id = ?", q.TenantID, q.TenantID)
	}
	if q.ResultType != "" {
		db = db.Where("result_type = ?", q.ResultType)
	}
	if q.Success != nil {
		db = db.Where("success = ?", *q.Success)
	}
	if q.Number != "" {
		like := "%" + q.Number + "%"
		db = db.Where("caller_id_number LIKE ? OR callee_number LIKE ?", like, like)
	}
	if q.JobUUID != nil {
		db = db.Where("job_uuid = ?", *q.JobUUID)
	}
	if q.From != nil {
		db = db.Where("created_at >= ?", *q.From)
	}
	if q.To != nil {
		db = db.Where("created_at <= ?", *q.To)
	}
	return db
}

// handleListFaxResults lists persisted fax job results (receptions, bridged
// calls, transmissions, deliveries) across ALL tenants — not just jobs
// submitted through the portal. Used by the portal's admin "all jobs" view.
//
// Query params: tenant_id, result_type, success, number, job_uuid, from, to,
// limit, offset. Returns {total, items}.
func (s *Server) handleListFaxResults(ctx iris.Context) {
	q, err := parseFaxResultQuery(ctx.Request().URL.Query())
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}

	var total int64
	if err := q.apply(s.DB.Model(&FaxJobResult{})).Count(&total).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to count fax results: " + err.Error()})
		return
	}

	items := []FaxJobResult{}
	if err := q.apply(s.DB.Model(&FaxJobResult{})).
		Order("created_at DESC").Limit(q.Limit).Offset(q.Offset).
		Find(&items).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve fax results: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"total": total, "items": items})
}

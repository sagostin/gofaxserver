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
	"submission":   true,
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

// apply attaches the row-level identity/time filters (tenant, number, job
// UUID, time range) to a gorm query on the fax_job_results table. result_type
// and success are group-level filters and are applied by the handler itself.
func (q faxResultQuery) apply(db *gorm.DB) *gorm.DB {
	if q.TenantID > 0 {
		db = db.Where("src_tenant_id = ? OR dst_tenant_id = ?", q.TenantID, q.TenantID)
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

// FaxResultGroup is one fax job (correlated by job_uuid) with all of its
// call legs/attempts (each with its own call_uuid). The group-level outcome
// (success, status, pages) is derived from the final primary leg — the latest
// non-delivery, non-submission leg, falling back to the latest leg overall.
// Attempts counts real outbound attempts: the highest attempt_number seen on
// transmission/delivery legs (receptions, bridges and submissions are the
// job's source/intake, not attempts).
type FaxResultGroup struct {
	JobUUID          uuid.UUID      `json:"job_uuid"`
	CallerIDNumber   string         `json:"caller_id_number"`
	CallerIDName     string         `json:"caller_id_name"`
	CalleeNumber     string         `json:"callee_number"`
	SrcTenantID      uint           `json:"src_tenant_id"`
	DstTenantID      uint           `json:"dst_tenant_id"`
	Attempts         int            `json:"attempts"`
	LegTypes         []string       `json:"leg_types"`
	Success          bool           `json:"success"`
	Status           string         `json:"status"`
	TransferredPages uint           `json:"transferred_pages"`
	TotalPages       uint           `json:"total_pages"`
	FirstTs          time.Time      `json:"first_ts"`
	LastTs           time.Time      `json:"last_ts"`
	Legs             []FaxJobResult `json:"legs"`
}

// deriveGroup builds the group summary from a job's legs, which must be
// ordered by created_at ascending.
func deriveGroup(id uuid.UUID, legs []FaxJobResult) FaxResultGroup {
	g := FaxResultGroup{JobUUID: id, Legs: legs, LegTypes: []string{}}
	if len(legs) == 0 {
		return g
	}
	last := legs[len(legs)-1]
	g.CallerIDNumber = last.CallerIdNumber
	g.CallerIDName = last.CallerIdName
	g.CalleeNumber = last.CalleeNumber
	g.SrcTenantID = last.SrcTenantID
	g.DstTenantID = last.DstTenantID
	g.FirstTs = legs[0].CreatedAt
	g.LastTs = last.CreatedAt

	seen := map[string]bool{}
	for _, l := range legs {
		if !seen[l.ResultType] {
			seen[l.ResultType] = true
			g.LegTypes = append(g.LegTypes, l.ResultType)
		}
		// Attempts tracks real outbound attempts only: transmission and
		// delivery legs carry a per-retry attempt_number, while receptions,
		// bridges and submissions are the job's source/intake.
		if (l.ResultType == "transmission" || l.ResultType == "delivery") &&
			l.AttemptNumber > g.Attempts {
			g.Attempts = l.AttemptNumber
		}
	}

	// Final primary leg decides the outcome: latest leg that is not a
	// delivery or submission, falling back to the latest leg for
	// delivery/submission-only jobs.
	primary := last
	for i := len(legs) - 1; i >= 0; i-- {
		if legs[i].ResultType != "delivery" && legs[i].ResultType != "submission" {
			primary = legs[i]
			break
		}
	}
	g.Success = primary.Success
	g.Status = primary.Status
	g.TransferredPages = primary.TransferredPages
	g.TotalPages = primary.TotalPages
	if primary.ResultType == "submission" {
		// Intake-only job (queued/in-flight): the submission lifecycle state
		// is not a fax outcome. Present the intake state ("queued" /
		// "processed") and never report success before a real attempt ends.
		g.Success = false
		g.Status = primary.ResultText
		if g.Status == "" {
			g.Status = "queued"
		}
	}
	return g
}

// handleListFaxResults lists persisted fax job results (receptions, bridged
// calls, transmissions, deliveries) across ALL tenants — not just jobs
// submitted through the portal — grouped by job UUID. Each group carries all
// of the job's call legs (individual attempts, each with its own call UUID).
// Used by the portal's admin "all jobs" view.
//
// Query params: tenant_id, result_type (jobs containing such a leg), success
// (job-level outcome per the final primary leg), number, job_uuid, from, to,
// limit, offset. Returns {total: job count, items: []FaxResultGroup}.
func (s *Server) handleListFaxResults(ctx iris.Context) {
	q, err := parseFaxResultQuery(ctx.Request().URL.Query())
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}

	base := q.apply(s.DB.Model(&FaxJobResult{}))

	// result_type: keep jobs that have at least one leg of that type (all of
	// the job's legs are still returned in the group).
	if q.ResultType != "" {
		sub := s.DB.Model(&FaxJobResult{}).Select("job_uuid").Where("result_type = ?", q.ResultType)
		base = base.Where("job_uuid IN (?)", sub)
	}

	// success: job-level outcome — the success flag of the final primary leg
	// (latest leg, deliveries ranked below primary legs). Submission legs are
	// excluded entirely: they track intake lifecycle (queued/processed), not
	// outcomes, so intake-only jobs match neither success filter value.
	if q.Success != nil {
		latest := s.DB.Model(&FaxJobResult{}).
			Select("DISTINCT ON (job_uuid) job_uuid, success").
			Where("result_type <> ?", "submission").
			Order("job_uuid, CASE WHEN result_type = 'delivery' THEN 1 ELSE 0 END, created_at DESC")
		sub := s.DB.Table("(?) AS latest_legs", latest).
			Select("job_uuid").Where("success = ?", *q.Success)
		base = base.Where("job_uuid IN (?)", sub)
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Distinct("job_uuid").Count(&total).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to count fax jobs: " + err.Error()})
		return
	}

	// Page over jobs (not rows), most recently active first.
	var page []struct {
		JobUUID uuid.UUID
		LastAt  time.Time
	}
	if err := base.Session(&gorm.Session{}).
		Select("job_uuid, MAX(created_at) AS last_at").
		Group("job_uuid").Order("last_at DESC").
		Limit(q.Limit).Offset(q.Offset).
		Scan(&page).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve fax jobs: " + err.Error()})
		return
	}

	items := []FaxResultGroup{}
	if len(page) == 0 {
		ctx.JSON(iris.Map{"total": total, "items": items})
		return
	}

	uuids := make([]uuid.UUID, 0, len(page))
	for _, p := range page {
		uuids = append(uuids, p.JobUUID)
	}
	legs := []FaxJobResult{}
	if err := s.DB.Where("job_uuid IN ?", uuids).
		Order("created_at ASC").Find(&legs).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve fax job legs: " + err.Error()})
		return
	}

	byJob := map[uuid.UUID][]FaxJobResult{}
	for _, l := range legs {
		byJob[l.JobUUID] = append(byJob[l.JobUUID], l)
	}
	for _, p := range page {
		items = append(items, deriveGroup(p.JobUUID, byJob[p.JobUUID]))
	}
	ctx.JSON(iris.Map{"total": total, "items": items})
}

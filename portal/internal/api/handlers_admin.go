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
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gofaxportal/internal/auth"
	"gofaxportal/internal/crypto"
	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"

	"github.com/kataras/iris/v12"
)

// ---------- Orgs ----------

func (s *Server) adminListOrgs(ctx iris.Context) {
	orgs := []models.Org{}
	if err := s.DB.Order("name ASC").Find(&orgs).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to load orgs"})
		return
	}
	type orgRow struct {
		models.Org
		UserCount   int64 `json:"user_count"`
		NumberCount int64 `json:"number_count"`
	}
	out := make([]orgRow, 0, len(orgs))
	for _, o := range orgs {
		row := orgRow{Org: o}
		s.DB.Model(&models.PortalUser{}).Where("org_id = ?", o.ID).Count(&row.UserCount)
		s.DB.Model(&models.Number{}).Where("org_id = ?", o.ID).Count(&row.NumberCount)
		out = append(out, row)
	}
	ctx.JSON(out)
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := slugStrip.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 24 {
		s = s[:24]
	}
	if s == "" {
		s = "org"
	}
	return s
}

type createOrgReq struct {
	Name string `json:"name"`
}

func (s *Server) adminCreateOrg(ctx iris.Context) {
	var req createOrgReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "name required"})
		return
	}
	name := strings.TrimSpace(req.Name)

	tenant, err := s.FX.CreateTenant(name, "")
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to create upstream tenant: " + err.Error()})
		return
	}

	username := fmt.Sprintf("svc_%s_%s", slugify(name), crypto.RandomToken(2))
	password := crypto.RandomToken(16)
	apiKey := crypto.RandomToken(16)

	if _, uerr := s.FX.CreateTenantUser(tenant.ID, username, password, apiKey); uerr != nil {
		_ = s.FX.DeleteTenant(tenant.ID) // compensate
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to create upstream service account: " + uerr.Error()})
		return
	}
	if aerr := s.FX.AuthenticateSvcAccount(username, password); aerr != nil {
		_ = s.FX.DeleteTenant(tenant.ID) // compensate (cascades conceptually; user row orphan acceptable + documented)
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "service account verification failed: " + aerr.Error()})
		return
	}
	sealed, serr := crypto.SealString(s.Box, password)
	if serr != nil {
		_ = s.FX.DeleteTenant(tenant.ID)
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to seal credentials"})
		return
	}
	org := &models.Org{Name: name, GofaxTenantID: tenant.ID, SvcUsername: username, SvcPasswordEnc: sealed, Active: true}
	if derr := s.DB.Create(org).Error; derr != nil {
		_ = s.FX.DeleteTenant(tenant.ID)
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to save organization locally"})
		return
	}
	s.audit(ctx, "ORG_CREATE", fmt.Sprintf("org:%d", org.ID), map[string]any{
		"name": name, "gofax_tenant_id": tenant.ID, "svc_username": username,
	})
	ctx.StatusCode(201)
	ctx.JSON(org)
}

type updateOrgReq struct {
	Name   *string `json:"name"`
	Active *bool   `json:"active"`
}

func (s *Server) adminUpdateOrg(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var org models.Org
	if err := s.DB.First(&org, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "org not found"})
		return
	}
	var req updateOrgReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		newName := strings.TrimSpace(*req.Name)
		// Preserve the tenant-level notify: UpdateTenant rewrites the row, and
		// an empty string would silently clear any notify set out-of-band.
		liveTenants, lerr := s.FX.ListTenants()
		notify := ""
		if lerr == nil {
			for _, t := range liveTenants {
				if t.ID == org.GofaxTenantID {
					notify = t.Notify
				}
			}
		}
		if uerr := s.FX.UpdateTenant(org.GofaxTenantID, newName, notify); uerr != nil {
			ctx.StatusCode(502)
			ctx.JSON(map[string]string{"error": "upstream rename failed: " + uerr.Error()})
			return
		}
		org.Name = newName
	}
	if req.Active != nil {
		org.Active = *req.Active
	}
	if err := s.DB.Save(&org).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to update org"})
		return
	}
	s.audit(ctx, "ORG_UPDATE", fmt.Sprintf("org:%d", org.ID), req)
	ctx.JSON(org)
}

func (s *Server) adminDeleteOrg(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	purge := ctx.URLParam("purge") == "true"
	var org models.Org
	if err := s.DB.First(&org, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "org not found"})
		return
	}
	if purge {
		numbers := []models.Number{}
		s.DB.Where("org_id = ?", org.ID).Find(&numbers)
		for i := range numbers {
			s.deprovisionPortalEndpoint(&numbers[i])
			_ = s.FX.DeleteNumber(org.GofaxTenantID, numbers[i].Number)
		}
		derr := s.FX.DeleteTenant(org.GofaxTenantID)
		s.DB.Where("org_id = ?", org.ID).Delete(&models.Number{})
		s.DB.Where("org_id = ?", org.ID).Delete(&models.InboundFax{})
		s.DB.Exec("DELETE FROM user_numbers WHERE user_id IN (SELECT id FROM portal_users WHERE org_id = ?)", org.ID)
		users := []models.PortalUser{}
		s.DB.Where("org_id = ?", org.ID).Find(&users)
		for _, u := range users {
			_ = s.Auth.DestroyUserSessions(u.ID)
			s.DB.Model(&u).Update("active", false)
		}
		s.DB.Delete(&org)
		s.audit(ctx, "ORG_PURGE", fmt.Sprintf("org:%d", org.ID), map[string]any{"upstream_delete_error": fmt.Sprint(derr)})
		ctx.JSON(map[string]bool{"purged": true})
		return
	}
	org.Active = false
	if err := s.DB.Save(&org).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to deactivate org"})
		return
	}
	s.audit(ctx, "ORG_DEACTIVATE", fmt.Sprintf("org:%d", org.ID), nil)
	ctx.JSON(org)
}

// adminReconcileOrg compares the portal mirror against live gofaxserver state.
func (s *Server) adminReconcileOrg(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var org models.Org
	if err := s.DB.First(&org, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "org not found"})
		return
	}

	liveTenants, terr := s.FX.ListTenants()
	if terr != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream tenants: " + terr.Error()})
		return
	}
	liveNumbers, nerr := s.FX.ListNumbers(org.GofaxTenantID)
	if nerr != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream numbers: " + nerr.Error()})
		return
	}
	liveUsers, uerr := s.FX.ListUsers(org.GofaxTenantID)
	if uerr != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream users: " + uerr.Error()})
		return
	}

	report := map[string]any{
		"org_id":                   org.ID,
		"tenant_exists":            false,
		"svc_account_ok":           false,
		"missing_upstream_numbers": []string{}, // in portal DB, not on gofaxserver
		"missing_local_numbers":    []string{}, // on gofaxserver, not in portal DB
		"number_field_drift":       []map[string]string{},
	}

	for _, t := range liveTenants {
		if t.ID == org.GofaxTenantID {
			report["tenant_exists"] = true
		}
	}
	for _, u := range liveUsers {
		if u.Username == org.SvcUsername {
			report["svc_account_ok"] = true
		}
	}

	localNumbers := []models.Number{}
	s.DB.Where("org_id = ?", org.ID).Find(&localNumbers)
	liveByNumber := map[string]fsclient.TenantNumber{}
	for _, ln := range liveNumbers {
		liveByNumber[ln.Number] = ln
	}
	localByNumber := map[string]models.Number{}
	for _, ln := range localNumbers {
		localByNumber[ln.Number] = ln
		if _, ok := liveByNumber[ln.Number]; !ok {
			report["missing_upstream_numbers"] = append(report["missing_upstream_numbers"].([]string), ln.Number)
		}
	}
	for number, ln := range liveByNumber {
		lnLocal, exists := localByNumber[number]
		if !exists {
			report["missing_local_numbers"] = append(report["missing_local_numbers"].([]string), number)
			continue
		}
		if ln.ID != lnLocal.GofaxNumberID {
			report["number_field_drift"] = append(report["number_field_drift"].([]map[string]string),
				map[string]string{"number": number, "field": "gofax_number_id",
					"portal": fmt.Sprint(lnLocal.GofaxNumberID), "live": fmt.Sprint(ln.ID)})
		}
	}
	sort.Strings(report["missing_upstream_numbers"].([]string))
	sort.Strings(report["missing_local_numbers"].([]string))
	ctx.JSON(report)
}

// ---------- Numbers ----------

type numberResp struct {
	models.Number
	OrgName       string `json:"org_name"`
	AssignedCount int64  `json:"assigned_count"`
}

func (s *Server) adminListNumbers(ctx iris.Context) {
	rows := []struct {
		models.Number
		OrgName string
	}{}
	q := s.DB.Table("numbers").Select("numbers.*, orgs.name AS org_name").
		Joins("JOIN orgs ON orgs.id = numbers.org_id").Order("numbers.number ASC")
	if oid := ctx.URLParamIntDefault("org_id", 0); oid > 0 {
		q = q.Where("numbers.org_id = ?", oid)
	}
	if err := q.Scan(&rows).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to load numbers"})
		return
	}
	out := make([]numberResp, 0, len(rows))
	for _, r := range rows {
		row := numberResp{Number: r.Number, OrgName: r.OrgName}
		s.DB.Model(&models.UserNumber{}).Where("number_id = ?", r.ID).Count(&row.AssignedCount)
		out = append(out, row)
	}
	ctx.JSON(out)
}

type createNumberReq struct {
	OrgID          uint   `json:"org_id"`
	Number         string `json:"number"`
	Name           string `json:"name"`
	Header         string `json:"header"`
	InboundEnabled *bool  `json:"inbound_enabled"` // default true
}

// provisionPortalEndpoint creates the backend "portal" endpoint that
// delivers inbound faxes for num into the org's portal inbox, and records
// its upstream id on num (caller persists).
func (s *Server) provisionPortalEndpoint(num *models.Number, org *models.Org) error {
	ep, err := s.FX.AddEndpoint(fsclient.Endpoint{
		Type:         "number",
		TypeID:       num.GofaxNumberID,
		EndpointType: "portal",
		Endpoint:     org.SvcUsername,
		Priority:     0,
	})
	if err != nil {
		return err
	}
	num.PortalEndpointID = ep.ID
	return nil
}

// deprovisionPortalEndpoint removes the backend "portal" endpoint for num
// (best-effort, with a scope scan fallback if the recorded id drifted).
func (s *Server) deprovisionPortalEndpoint(num *models.Number) {
	if num.PortalEndpointID != 0 {
		if err := s.FX.DeleteEndpoint(num.PortalEndpointID); err == nil {
			num.PortalEndpointID = 0
			return
		}
	}
	if eps, err := s.FX.ListEndpoints("number", num.GofaxNumberID); err == nil {
		for _, ep := range eps {
			if ep.EndpointType == "portal" {
				_ = s.FX.DeleteEndpoint(ep.ID)
			}
		}
	}
	num.PortalEndpointID = 0
}

func (s *Server) adminCreateNumber(ctx iris.Context) {
	var req createNumberReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Number) == "" || req.OrgID == 0 {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "org_id and number required"})
		return
	}
	number := sanitizeNumber(req.Number)
	if len(number) < 7 {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "number must contain at least 7 digits"})
		return
	}
	var org models.Org
	if err := s.DB.First(&org, req.OrgID).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "org not found"})
		return
	}
	var count int64
	s.DB.Model(&models.Number{}).Where("number = ?", number).Count(&count)
	if count > 0 {
		ctx.StatusCode(409)
		ctx.JSON(map[string]string{"error": "number already registered in portal"})
		return
	}
	// New numbers have no user assignments yet, so the notify string is just
	// the portal status-push destination (email_report entries are added by
	// the assignment flow).
	created, err := s.FX.AddNumber(org.GofaxTenantID, number, req.Name, req.Header, s.computeNotify(0, org.SvcUsername))
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream add failed: " + err.Error()})
		return
	}
	inboundEnabled := req.InboundEnabled == nil || *req.InboundEnabled
	row := &models.Number{Number: number, Name: req.Name, Header: req.Header, GofaxNumberID: created.ID, OrgID: org.ID, Active: true, InboundEnabled: inboundEnabled}
	if err := s.DB.Create(row).Error; err != nil {
		_ = s.FX.DeleteNumber(org.GofaxTenantID, number)
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to save number mirror"})
		return
	}
	if row.InboundEnabled {
		if perr := s.provisionPortalEndpoint(row, &org); perr != nil {
			_ = s.FX.DeleteNumber(org.GofaxTenantID, number)
			s.DB.Delete(row)
			ctx.StatusCode(502)
			ctx.JSON(map[string]string{"error": "inbound endpoint provisioning failed: " + perr.Error()})
			return
		}
		if err := s.DB.Save(row).Error; err != nil {
			ctx.StatusCode(500)
			ctx.JSON(map[string]string{"error": "failed to record inbound endpoint"})
			return
		}
	}
	s.audit(ctx, "NUMBER_CREATE", number, map[string]any{"org_id": org.ID, "gofax_number_id": created.ID, "inbound_enabled": row.InboundEnabled})
	ctx.StatusCode(201)
	ctx.JSON(row)
}

func (s *Server) loadNumber(id uint) (*models.Number, *models.Org, error) {
	var num models.Number
	if err := s.DB.First(&num, id).Error; err != nil {
		return nil, nil, fmt.Errorf("number not found")
	}
	var org models.Org
	if err := s.DB.First(&org, num.OrgID).Error; err != nil {
		return nil, nil, fmt.Errorf("org missing for number")
	}
	return &num, &org, nil
}

type updateNumberReq struct {
	Name           *string `json:"name"`
	Header         *string `json:"header"`
	Active         *bool   `json:"active"`
	InboundEnabled *bool   `json:"inbound_enabled"`
}

func (s *Server) adminUpdateNumber(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	num, org, err := s.loadNumber(id)
	if err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": err.Error()})
		return
	}
	var req updateNumberReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	if req.Name != nil {
		num.Name = *req.Name
	}
	if req.Header != nil {
		num.Header = *req.Header
	}
	if req.Active != nil {
		num.Active = *req.Active
	}
	if req.InboundEnabled != nil && *req.InboundEnabled != num.InboundEnabled {
		if *req.InboundEnabled {
			if perr := s.provisionPortalEndpoint(num, org); perr != nil {
				ctx.StatusCode(502)
				ctx.JSON(map[string]string{"error": "inbound endpoint provisioning failed: " + perr.Error()})
				return
			}
		} else {
			s.deprovisionPortalEndpoint(num)
		}
		num.InboundEnabled = *req.InboundEnabled
	}
	notify := s.computeNotify(num.ID, org.SvcUsername)
	if uerr := s.FX.UpdateNumber(num.GofaxNumberID, org.GofaxTenantID, num.Number, num.Name, num.Header, notify); uerr != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + uerr.Error()})
		return
	}
	if err := s.DB.Save(num).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to update number"})
		return
	}
	s.audit(ctx, "NUMBER_UPDATE", num.Number, req)
	ctx.JSON(num)
}

func (s *Server) adminDeleteNumber(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	num, org, err := s.loadNumber(id)
	if err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": err.Error()})
		return
	}
	s.deprovisionPortalEndpoint(num)
	if derr := s.FX.DeleteNumber(org.GofaxTenantID, num.Number); derr != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + derr.Error()})
		return
	}
	s.DB.Where("number_id = ?", num.ID).Delete(&models.UserNumber{})
	s.DB.Delete(num)
	s.audit(ctx, "NUMBER_DELETE", num.Number, map[string]any{"org_id": org.ID})
	ctx.JSON(map[string]bool{"ok": true})
}

// computeNotify builds the derived notify string for a number: an
// email_report destination per assigned user's email, plus the portal
// status-push destination (portal->svc_username) so gofaxserver notifies the
// portal of final job outcomes immediately. The portal destination is always
// present for orgs with a service account, even with no assigned users.
func (s *Server) computeNotify(numberID uint, svcUsername string) string {
	dests := []string{}
	emails := []string{}
	s.DB.Table("portal_users").
		Joins("JOIN user_numbers ON user_numbers.user_id = portal_users.id").
		Where("user_numbers.number_id = ? AND portal_users.active = ? AND portal_users.email <> ''", numberID, true).
		Distinct().Order("portal_users.email ASC").Pluck("portal_users.email", &emails)
	if len(emails) > 0 {
		dests = append(dests, "email_report->"+strings.Join(emails, ";"))
	}
	if svcUsername != "" {
		dests = append(dests, "portal->"+svcUsername)
	}
	return strings.Join(dests, ",")
}

func (s *Server) adminGetAssignments(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	userIDs := []uint{}
	s.DB.Model(&models.UserNumber{}).Where("number_id = ?", id).Pluck("user_id", &userIDs)
	ctx.JSON(map[string][]uint{"user_ids": userIDs})
}

type setAssignmentsReq struct {
	UserIDs []uint `json:"user_ids"`
}

// adminSetAssignments replaces the assigned-user set for a number and pushes
// the merged email_report notify upstream BEFORE committing local rows.
func (s *Server) adminSetAssignments(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	num, org, err := s.loadNumber(id)
	if err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": err.Error()})
		return
	}
	var req setAssignmentsReq
	if err := ctx.ReadJSON(&req); err != nil || req.UserIDs == nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "user_ids array required"})
		return
	}
	// Validate all users belong to this org, are active fax users.
	valid := []uint{}
	for _, uid := range req.UserIDs {
		var u models.PortalUser
		if e := s.DB.Select("id", "role", "org_id", "active").First(&u, uid).Error; e == nil &&
			u.Active && u.Role == models.RoleUser && u.OrgID != nil && *u.OrgID == num.OrgID {
			valid = append(valid, uid)
		}
	}

	tx := s.DB.Begin()
	tx.Where("number_id = ?", num.ID).Delete(&models.UserNumber{})
	for _, uid := range valid {
		if e := tx.Create(&models.UserNumber{UserID: uid, NumberID: num.ID}).Error; e != nil {
			tx.Rollback()
			ctx.StatusCode(500)
			ctx.JSON(map[string]string{"error": "failed to store assignments"})
			return
		}
	}
	notify := s.computeNotify(num.ID, org.SvcUsername)
	if uerr := s.FX.UpdateNumber(num.GofaxNumberID, org.GofaxTenantID, num.Number, num.Name, num.Header, notify); uerr != nil {
		tx.Rollback()
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "assignments stored locally but upstream notify sync failed: " + uerr.Error()})
		return
	}
	if cerr := tx.Commit().Error; cerr != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to commit assignments"})
		return
	}
	s.audit(ctx, "NUMBER_ASSIGNMENTS", num.Number, map[string]any{"user_ids": valid, "notify": notify})
	ctx.JSON(map[string]any{"number_id": num.ID, "user_ids": valid, "notify": notify})
}

// ---------- Users ----------

type userResp struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	OrgID       *uint  `json:"org_id"`
	OrgName     string `json:"org_name,omitempty"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"-"`
	AssignedIDs []uint `json:"assigned_number_ids"`
}

func (s *Server) adminListUsers(ctx iris.Context) {
	users := []models.PortalUser{}
	q := s.DB.Order("username ASC")
	if oid := ctx.URLParamIntDefault("org_id", 0); oid > 0 {
		q = q.Where("org_id = ?", oid)
	}
	if err := q.Find(&users).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to load users"})
		return
	}
	orgNames := map[uint]string{}
	orgs := []models.Org{}
	s.DB.Find(&orgs)
	for _, o := range orgs {
		orgNames[o.ID] = o.Name
	}
	out := make([]userResp, 0, len(users))
	for _, u := range users {
		r := userResp{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role, OrgID: u.OrgID, Active: u.Active, AssignedIDs: []uint{}}
		if u.OrgID != nil {
			r.OrgName = orgNames[*u.OrgID]
		}
		s.DB.Model(&models.UserNumber{}).Where("user_id = ?", u.ID).Pluck("number_id", &r.AssignedIDs)
		out = append(out, r)
	}
	ctx.JSON(out)
}

type createUserReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
	OrgID    *uint  `json:"org_id"`
}

func (s *Server) adminCreateUser(ctx iris.Context) {
	var req createUserReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Username) == "" || req.Password == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "username and password required"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if req.Role == "" {
		req.Role = models.RoleUser
	}
	if req.Role != models.RoleAdmin && req.Role != models.RoleUser {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "role must be admin or user"})
		return
	}
	if req.Role == models.RoleUser && (req.OrgID == nil || *req.OrgID == 0) {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "org_id required for user accounts"})
		return
	}
	if req.OrgID != nil && *req.OrgID > 0 {
		var cnt int64
		s.DB.Model(&models.Org{}).Where("id = ?", *req.OrgID).Count(&cnt)
		if cnt == 0 {
			ctx.StatusCode(404)
			ctx.JSON(map[string]string{"error": "org not found"})
			return
		}
	}
	hash, herr := auth.HashPassword(req.Password)
	if herr != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to hash password"})
		return
	}
	u := &models.PortalUser{Username: req.Username, Email: strings.TrimSpace(req.Email), PasswordHash: hash, Role: req.Role, OrgID: req.OrgID, Active: true}
	if err := s.DB.Create(u).Error; err != nil {
		ctx.StatusCode(409)
		ctx.JSON(map[string]string{"error": "failed to create user (duplicate username?)"})
		return
	}
	s.audit(ctx, "USER_CREATE", u.Username, map[string]any{"role": u.Role, "org_id": u.OrgID})
	ctx.StatusCode(201)
	ctx.JSON(userResp{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role, OrgID: u.OrgID, Active: u.Active, AssignedIDs: []uint{}})
}

type updateUserReq struct {
	Email  *string `json:"email"`
	Active *bool   `json:"active"`
}

func (s *Server) adminUpdateUser(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var u models.PortalUser
	if err := s.DB.First(&u, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "user not found"})
		return
	}
	var req updateUserReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	if req.Email != nil {
		u.Email = strings.TrimSpace(*req.Email)
	}
	if req.Active != nil {
		u.Active = *req.Active
		if !*req.Active {
			_ = s.Auth.DestroyUserSessions(u.ID)
		}
	}
	if err := s.DB.Save(&u).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to update user"})
		return
	}
	s.audit(ctx, "USER_UPDATE", u.Username, req)
	ctx.JSON(userResp{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role, OrgID: u.OrgID, Active: u.Active})
}

type resetPasswordReq struct {
	Password string `json:"password"`
}

func (s *Server) adminResetPassword(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var u models.PortalUser
	if err := s.DB.First(&u, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "user not found"})
		return
	}
	var req resetPasswordReq
	if err := ctx.ReadJSON(&req); err != nil || len(req.Password) < 10 {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "password of at least 10 characters required"})
		return
	}
	hash, herr := auth.HashPassword(req.Password)
	if herr != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to hash password"})
		return
	}
	u.PasswordHash = hash
	if err := s.DB.Save(&u).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to set password"})
		return
	}
	_ = s.Auth.DestroyUserSessions(u.ID)
	s.audit(ctx, "USER_PASSWORD_RESET", u.Username, nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminDeleteUser(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	_, actor, _ := currentUser(ctx)
	if actor != nil && actor.ID == id {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "you cannot delete your own account"})
		return
	}
	var u models.PortalUser
	if err := s.DB.First(&u, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "user not found"})
		return
	}
	_ = s.Auth.DestroyUserSessions(u.ID)
	s.DB.Where("user_id = ?", u.ID).Delete(&models.UserNumber{})
	s.DB.Delete(&u)
	s.audit(ctx, "USER_DELETE", u.Username, nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Endpoints ----------

var validEndpointTypes = map[string]bool{"tenant": true, "number": true, "global": true}
var validEndpointKinds = map[string]bool{"gateway": true, "webhook": true, "email": true, "portal": true}

// endpointReq / gatewaySpecReq wrap the upstream payloads with the portal-only
// scope_source field, which selects the ID namespace for type_id:
//   - "portal": type_id is a portal org/number id → translated to the linked
//     gofaxserver tenant/number id before proxying.
//   - anything else ("direct" or empty): type_id is a raw gofaxserver
//     tenant/number id → validated live against the upstream.
type endpointReq struct {
	fsclient.Endpoint
	ScopeSource string `json:"scope_source"`
}

type gatewaySpecReq struct {
	fsclient.GatewayProvisionSpec
	ScopeSource string `json:"scope_source"`
}

// resolveScopeTypeID validates a scope reference and returns the
// gofaxserver-side type_id to use.
func (s *Server) resolveScopeTypeID(scopeType, scopeSource string, typeID uint) (uint, string) {
	if !validEndpointTypes[scopeType] {
		return 0, "type must be tenant, number or global"
	}
	if scopeType == "global" {
		return 0, ""
	}
	if scopeSource == "portal" {
		switch scopeType {
		case "tenant":
			var org models.Org
			if err := s.DB.First(&org, typeID).Error; err != nil {
				return 0, "unknown tenant scope: no such portal org"
			}
			return org.GofaxTenantID, ""
		case "number":
			var num models.Number
			if err := s.DB.First(&num, typeID).Error; err != nil {
				return 0, "unknown number scope: no such portal number"
			}
			return num.GofaxNumberID, ""
		}
	}
	// Direct scope: type_id is an upstream gofaxserver id — validate live so
	// typos don't create endpoints pointing at nothing.
	switch scopeType {
	case "tenant":
		tenants, err := s.FX.ListTenants()
		if err != nil {
			return 0, "failed to validate tenant upstream: " + err.Error()
		}
		for _, t := range tenants {
			if t.ID == typeID {
				return typeID, ""
			}
		}
		return 0, "unknown tenant scope: no such upstream tenant id"
	case "number":
		numbers, err := s.FX.ListNumbers(0)
		if err != nil {
			return 0, "failed to validate number upstream: " + err.Error()
		}
		for _, n := range numbers {
			if n.ID == typeID {
				return typeID, ""
			}
		}
		return 0, "unknown number scope: no such upstream number id"
	}
	return 0, ""
}

func (s *Server) validateEndpoint(ep *fsclient.Endpoint, scopeSource string) string {
	if !validEndpointKinds[ep.EndpointType] {
		return "endpoint_type must be gateway, webhook, email or portal"
	}
	if strings.TrimSpace(ep.Endpoint) == "" {
		return "endpoint value required"
	}
	tid, msg := s.resolveScopeTypeID(ep.Type, scopeSource, ep.TypeID)
	if msg != "" {
		return msg
	}
	ep.TypeID = tid
	return ""
}

func (s *Server) adminListEndpoints(ctx iris.Context) {
	t := ctx.URLParam("type")
	tid := uint(ctx.URLParamIntDefault("type_id", 0))
	eps, err := s.FX.ListEndpoints(t, tid)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream endpoints: " + err.Error()})
		return
	}
	ctx.JSON(eps)
}

func (s *Server) adminCreateEndpoint(ctx iris.Context) {
	var req endpointReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	ep := req.Endpoint
	if msg := s.validateEndpoint(&ep, req.ScopeSource); msg != "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": msg})
		return
	}
	created, err := s.FX.AddEndpoint(ep)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream create failed: " + err.Error()})
		return
	}
	s.audit(ctx, "ENDPOINT_CREATE", fmt.Sprintf("endpoint:%d", created.ID), ep)
	ctx.StatusCode(201)
	ctx.JSON(created)
}

func (s *Server) adminUpdateEndpoint(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var req endpointReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	ep := req.Endpoint
	ep.ID = id
	if msg := s.validateEndpoint(&ep, req.ScopeSource); msg != "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": msg})
		return
	}
	if err := s.FX.UpdateEndpoint(ep); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "ENDPOINT_UPDATE", fmt.Sprintf("endpoint:%d", id), ep)
	ctx.JSON(ep)
}

func (s *Server) adminDeleteEndpoint(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	existing, _ := s.FX.ListEndpoints("", 0)
	isGlobal := false
	for _, ep := range existing {
		if ep.ID == id {
			isGlobal = ep.Type == "global"
		}
	}
	if isGlobal && ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "refusing to delete global endpoint without confirm=true query param"})
		return
	}
	if err := s.FX.DeleteEndpoint(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "ENDPOINT_DELETE", fmt.Sprintf("endpoint:%d", id), map[string]bool{"global": isGlobal})
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Gateway templates ----------

func (s *Server) adminListGatewayTemplates(ctx iris.Context) {
	out, err := s.FX.ListGatewayTemplates()
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream gateway templates: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminCreateGatewayTemplate(ctx iris.Context) {
	var tpl fsclient.GatewayTemplate
	if err := ctx.ReadJSON(&tpl); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	created, err := s.FX.CreateGatewayTemplate(tpl)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream create failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_TEMPLATE_CREATE", fmt.Sprintf("template:%d", created.ID), map[string]string{"name": created.Name})
	ctx.StatusCode(201)
	ctx.JSON(created)
}

func (s *Server) adminUpdateGatewayTemplate(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var tpl fsclient.GatewayTemplate
	if err := ctx.ReadJSON(&tpl); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	tpl.ID = id
	if err := s.FX.UpdateGatewayTemplate(tpl); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_TEMPLATE_UPDATE", fmt.Sprintf("template:%d", id), map[string]string{"name": tpl.Name})
	ctx.JSON(tpl)
}

func (s *Server) adminDeleteGatewayTemplate(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if err := s.FX.DeleteGatewayTemplate(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_TEMPLATE_DELETE", fmt.Sprintf("template:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- FreeSWITCH gateway provisioning ----------

func (s *Server) adminListGateways(ctx iris.Context) {
	out, err := s.FX.ListGateways()
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream gateways: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminAdoptGateway(ctx iris.Context) {
	var req struct {
		File string `json:"file"`
	}
	if err := ctx.ReadJSON(&req); err != nil || req.File == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "file is required"})
		return
	}
	gs, err := s.FX.AdoptGateway(req.File)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream adopt failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_ADOPT", "gateway:"+gs.Gateway.Name, map[string]string{"file": req.File})
	ctx.StatusCode(201)
	ctx.JSON(gs)
}

func (s *Server) adminDeleteUnmanagedGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "refusing to delete unmanaged gateway without confirm=true query param"})
		return
	}
	if err := s.FX.DeleteUnmanagedGateway(name); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_DELETE_UNMANAGED", "gateway:"+name, nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminRepairGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	gs, err := s.FX.RepairGateway(name)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream repair failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_REPAIR", "gateway:"+name, nil)
	ctx.JSON(gs)
}

// validateGatewaySpec sanity-checks the scope fields before proxying; the
// upstream performs full validation (name, realm, template, XML). Scope
// fields are only validated when present — updates omit them and the upstream
// preserves the existing endpoint's scope. scope_source selects the type_id
// namespace (portal ids are translated, direct ids validated upstream).
func (s *Server) validateGatewaySpec(spec *fsclient.GatewayProvisionSpec, scopeSource string) string {
	if spec.Scope == "" {
		return ""
	}
	tid, msg := s.resolveScopeTypeID(spec.Scope, scopeSource, spec.TypeID)
	if msg != "" {
		return msg
	}
	spec.TypeID = tid
	return ""
}

func (s *Server) adminProvisionGateway(ctx iris.Context) {
	var req gatewaySpecReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	spec := req.GatewayProvisionSpec
	if msg := s.validateGatewaySpec(&spec, req.ScopeSource); msg != "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": msg})
		return
	}
	gs, err := s.FX.ProvisionGateway(spec)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream provision failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_PROVISION", "gateway:"+spec.Name, map[string]interface{}{"type": spec.Scope, "type_id": spec.TypeID, "bridge": spec.Bridge})
	ctx.StatusCode(201)
	ctx.JSON(gs)
}

func (s *Server) adminUpdateGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	var req gatewaySpecReq
	if err := ctx.ReadJSON(&req); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	spec := req.GatewayProvisionSpec
	if msg := s.validateGatewaySpec(&spec, req.ScopeSource); msg != "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": msg})
		return
	}
	gs, err := s.FX.UpdateGateway(name, spec)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_UPDATE", "gateway:"+name, nil)
	ctx.JSON(gs)
}

func (s *Server) adminDeprovisionGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "refusing to deprovision gateway without confirm=true query param"})
		return
	}
	if err := s.FX.DeprovisionGateway(name); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_DELETE", "gateway:"+name, nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Dialplan rules ----------

func (s *Server) adminGetDialplan(ctx iris.Context) {
	out, err := s.FX.GetDialplan()
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to read upstream dialplan: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminCreateDialplanRule(ctx iris.Context) {
	var rule fsclient.DialplanRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	created, err := s.FX.CreateDialplanRule(rule)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream create failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIALPLAN_RULE_CREATE", fmt.Sprintf("rule:%d", created.ID), map[string]string{"pattern": created.Pattern})
	ctx.StatusCode(201)
	ctx.JSON(created)
}

func (s *Server) adminUpdateDialplanRule(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var rule fsclient.DialplanRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	rule.ID = id
	if err := s.FX.UpdateDialplanRule(rule); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIALPLAN_RULE_UPDATE", fmt.Sprintf("rule:%d", id), map[string]string{"pattern": rule.Pattern})
	ctx.JSON(rule)
}

func (s *Server) adminDeleteDialplanRule(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if err := s.FX.DeleteDialplanRule(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIALPLAN_RULE_DELETE", fmt.Sprintf("rule:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminReorderDialplanRules(ctx iris.Context) {
	var order []map[string]uint
	if err := ctx.ReadJSON(&order); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	if err := s.FX.ReorderDialplanRules(order); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream reorder failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIALPLAN_RULES_REORDER", "", nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Fax policy rules (T.38 / ECM / V.17) ----------

func (s *Server) adminListFaxPolicies(ctx iris.Context) {
	out, err := s.FX.ListFaxPolicies(ctx.URLParam("number"), ctx.URLParam("origin"))
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to read upstream fax policies: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminCreateFaxPolicyRule(ctx iris.Context) {
	var rule fsclient.FaxPolicyRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	created, err := s.FX.CreateFaxPolicyRule(rule)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream create failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FAX_POLICY_CREATE", fmt.Sprintf("rule:%d", created.ID), map[string]string{
		"scope": created.Scope, "effect": created.Effect,
		"src": created.SrcNumber, "dst": created.DstNumber,
	})
	ctx.StatusCode(201)
	ctx.JSON(created)
}

func (s *Server) adminUpdateFaxPolicyRule(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var rule fsclient.FaxPolicyRule
	if err := ctx.ReadJSON(&rule); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	rule.ID = id
	if err := s.FX.UpdateFaxPolicyRule(rule); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FAX_POLICY_UPDATE", fmt.Sprintf("rule:%d", id), map[string]string{
		"scope": rule.Scope, "effect": rule.Effect,
	})
	ctx.JSON(rule)
}

func (s *Server) adminDeleteFaxPolicyRule(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if err := s.FX.DeleteFaxPolicyRule(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FAX_POLICY_DELETE", fmt.Sprintf("rule:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminExpireFaxPolicyRule(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if err := s.FX.ExpireFaxPolicyRule(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream expire failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FAX_POLICY_EXPIRE", fmt.Sprintf("rule:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminResolveFaxPolicy(ctx iris.Context) {
	out, err := s.FX.ResolveFaxPolicy(ctx.URLParam("src"), ctx.URLParam("dst"), ctx.URLParamDefault("type", "softmodem"))
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream resolve failed: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminListFaxPairStates(ctx iris.Context) {
	out, err := s.FX.ListFaxPairStates(ctx.URLParam("number"))
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to read upstream pair states: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminDeleteFaxPairState(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if err := s.FX.DeleteFaxPairState(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FAX_PAIR_STATE_DELETE", fmt.Sprintf("pair_state:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Faxes / jobs / audit ----------

func (s *Server) adminActiveFaxes(ctx iris.Context) {
	out, err := s.FX.ListActiveFaxes()
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to read active faxes: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

type adminJobRow struct {
	models.FaxJob
	Username string `json:"username"`
	OrgName  string `json:"org_name"`
}

func (s *Server) adminListAllJobs(ctx iris.Context) {
	jobs := []adminJobRow{}
	q := s.DB.Table("fax_jobs").
		Select("fax_jobs.*, portal_users.username AS username, orgs.name AS org_name").
		Joins("JOIN portal_users ON portal_users.id = fax_jobs.user_id").
		Joins("JOIN orgs ON orgs.id = fax_jobs.org_id").
		Order("fax_jobs.submitted_at DESC")
	if oid := ctx.URLParamIntDefault("org_id", 0); oid > 0 {
		q = q.Where("fax_jobs.org_id = ?", oid)
	}
	if st := ctx.URLParam("status"); st != "" {
		q = q.Where("fax_jobs.status = ?", st)
	}
	limit := clampLimit(ctx.URLParamIntDefault("limit", 100), 500)
	if err := q.Limit(limit).Offset(ctx.URLParamIntDefault("offset", 0)).Scan(&jobs).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to load jobs"})
		return
	}
	ctx.JSON(jobs)
}

func (s *Server) svcCredsForOrg(orgID uint) (username, pass string, err error) {
	var org models.Org
	if err = s.DB.First(&org, orgID).Error; err != nil {
		return "", "", err
	}
	pass, err = crypto.OpenString(s.Box, org.SvcPasswordEnc)
	return org.SvcUsername, pass, err
}

// adminJobLive returns the fresh upstream attempt rows for one job.
func (s *Server) adminJobLive(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	job := models.FaxJob{}
	if err := s.DB.First(&job, id).Error; err != nil {
		ctx.StatusCode(404)
		ctx.JSON(map[string]string{"error": "job not found"})
		return
	}
	user, pass, err := s.svcCredsForOrg(job.OrgID)
	if err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to unlock org credentials"})
		return
	}
	rows, ferr := s.FX.GetFaxStatus(user, pass, job.JobUUID)
	if ferr != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to fetch upstream status: " + ferr.Error()})
		return
	}
	ctx.JSON(rows)
}

func (s *Server) adminAuditLog(ctx iris.Context) {
	limit := clampLimit(ctx.URLParamIntDefault("limit", 100), 500)
	offset := ctx.URLParamIntDefault("offset", 0)
	logs := []models.AuditLog{}
	q := s.DB.Order("created_at DESC").Limit(limit).Offset(offset)
	if a := ctx.URLParam("action"); a != "" {
		q = q.Where("action = ?", a)
	}
	if err := q.Find(&logs).Error; err != nil {
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to load audit log"})
		return
	}
	ctx.JSON(logs)
}

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
		for _, n := range numbers {
			_ = s.FX.DeleteNumber(org.GofaxTenantID, n.Number)
		}
		derr := s.FX.DeleteTenant(org.GofaxTenantID)
		s.DB.Where("org_id = ?", org.ID).Delete(&models.Number{})
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
	OrgID  uint   `json:"org_id"`
	Number string `json:"number"`
	Name   string `json:"name"`
	Header string `json:"header"`
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
	created, err := s.FX.AddNumber(org.GofaxTenantID, number, req.Name, req.Header, "")
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream add failed: " + err.Error()})
		return
	}
	row := &models.Number{Number: number, Name: req.Name, Header: req.Header, GofaxNumberID: created.ID, OrgID: org.ID, Active: true}
	if err := s.DB.Create(row).Error; err != nil {
		_ = s.FX.DeleteNumber(org.GofaxTenantID, number)
		ctx.StatusCode(500)
		ctx.JSON(map[string]string{"error": "failed to save number mirror"})
		return
	}
	s.audit(ctx, "NUMBER_CREATE", number, map[string]any{"org_id": org.ID, "gofax_number_id": created.ID})
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
	Name   *string `json:"name"`
	Header *string `json:"header"`
	Active *bool   `json:"active"`
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
	notify := s.computeNotify(num.ID)
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

// computeNotify builds the derived notify string for a number from its
// assigned users' emails: email_report->a@x;b@y (empty when unassigned).
func (s *Server) computeNotify(numberID uint) string {
	emails := []string{}
	s.DB.Table("portal_users").
		Joins("JOIN user_numbers ON user_numbers.user_id = portal_users.id").
		Where("user_numbers.number_id = ? AND portal_users.active = ? AND portal_users.email <> ''", numberID, true).
		Distinct().Order("portal_users.email ASC").Pluck("portal_users.email", &emails)
	if len(emails) == 0 {
		return ""
	}
	return "email_report->" + strings.Join(emails, ";")
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
	notify := s.computeNotify(num.ID)
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
var validEndpointKinds = map[string]bool{"gateway": true, "webhook": true, "email": true}

func (s *Server) validateEndpoint(ep *fsclient.Endpoint) string {
	if !validEndpointTypes[ep.Type] {
		return "type must be tenant, number or global"
	}
	if !validEndpointKinds[ep.EndpointType] {
		return "endpoint_type must be gateway, webhook or email"
	}
	if strings.TrimSpace(ep.Endpoint) == "" {
		return "endpoint value required"
	}
	switch ep.Type {
	case "tenant":
		var cnt int64
		s.DB.Model(&models.Org{}).Where("id = ?", ep.TypeID).Count(&cnt)
		if cnt == 0 {
			return "unknown tenant scope: no such org"
		}
	case "number":
		var cnt int64
		s.DB.Model(&models.Number{}).Where("id = ?", ep.TypeID).Count(&cnt)
		if cnt == 0 {
			return "unknown number scope: no such number id"
		}
	case "global":
		ep.TypeID = 0
	}
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
	var ep fsclient.Endpoint
	if err := ctx.ReadJSON(&ep); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	if msg := s.validateEndpoint(&ep); msg != "" {
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
	var ep fsclient.Endpoint
	if err := ctx.ReadJSON(&ep); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	ep.ID = id
	if msg := s.validateEndpoint(&ep); msg != "" {
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

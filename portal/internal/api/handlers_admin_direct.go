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
	"strings"

	"gofaxportal/internal/crypto"
	"gofaxportal/internal/fsclient"

	"github.com/kataras/iris/v12"
)

// This file implements the admin surface for everything that lives ONLY on
// gofaxserver (no portal DB mirror): direct tenants, their auth users and
// numbers, FreeSWITCH profile control, and provisioning gateways from
// pre-existing endpoints. All mutations are proxied via the admin API and
// recorded in the portal audit log.

// ---------- FreeSWITCH profile control ----------

func (s *Server) adminRescanFSProfile(ctx iris.Context) {
	if err := s.FX.RescanFSProfile(); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream rescan failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FS_PROFILE_RESCAN", "freeswitch", nil)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminRestartFSProfile(ctx iris.Context) {
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "refusing to restart the sofia profile (drops active calls) without confirm=true query param"})
		return
	}
	if err := s.FX.RestartFSProfile(); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream restart failed: " + err.Error()})
		return
	}
	s.audit(ctx, "FS_PROFILE_RESTART", "freeswitch", nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Upstream pickers (read-only; also feed scope dropdowns) ----------

// adminListUpstreamTenants lists gofaxserver tenants, including those with no
// portal org — the target list for direct management and scope pickers.
func (s *Server) adminListUpstreamTenants(ctx iris.Context) {
	out, err := s.FX.ListTenants()
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream tenants: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

// adminListUpstreamNumbers lists gofaxserver tenant numbers, optionally
// filtered by tenant_id.
func (s *Server) adminListUpstreamNumbers(ctx iris.Context) {
	tid := uint(ctx.URLParamIntDefault("tenant_id", 0))
	out, err := s.FX.ListNumbers(tid)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream numbers: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

// ---------- Direct tenants ----------

type directTenantReq struct {
	Name   string `json:"name"`
	Notify string `json:"notify"`
}

func (s *Server) adminCreateDirectTenant(ctx iris.Context) {
	var req directTenantReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "name required"})
		return
	}
	tenant, err := s.FX.CreateTenant(strings.TrimSpace(req.Name), req.Notify)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream create failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_TENANT_CREATE", fmt.Sprintf("tenant:%d", tenant.ID), map[string]string{"name": tenant.Name})
	ctx.StatusCode(201)
	ctx.JSON(tenant)
}

func (s *Server) adminUpdateDirectTenant(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var req directTenantReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "name required"})
		return
	}
	if err := s.FX.UpdateTenant(id, strings.TrimSpace(req.Name), req.Notify); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_TENANT_UPDATE", fmt.Sprintf("tenant:%d", id), req)
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminDeleteDirectTenant(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "refusing to delete tenant without confirm=true query param"})
		return
	}
	if err := s.FX.DeleteTenant(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_TENANT_DELETE", fmt.Sprintf("tenant:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Direct tenant users (gofaxserver auth accounts for /fax/send) ----------

type directUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"` // empty on create = generate; empty on update = keep
	APIKey   string `json:"api_key"`  // empty on create = generate
}

func (s *Server) adminListDirectTenantUsers(ctx iris.Context) {
	tid := ctx.Params().GetUintDefault("id", 0)
	out, err := s.FX.ListUsers(tid)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list upstream users: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminCreateDirectTenantUser(ctx iris.Context) {
	tid := ctx.Params().GetUintDefault("id", 0)
	var req directUserReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Username) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "username required"})
		return
	}
	generated := false
	if req.Password == "" {
		req.Password = crypto.RandomToken(16)
		generated = true
	}
	if req.APIKey == "" {
		req.APIKey = crypto.RandomToken(16)
	}
	user, err := s.FX.CreateTenantUser(tid, strings.TrimSpace(req.Username), req.Password, req.APIKey)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream create failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_USER_CREATE", fmt.Sprintf("tenant:%d user:%s", tid, user.Username), nil)
	// The password is returned exactly once, on creation, so the admin can
	// hand it to the direct-integration user; it is never retrievable later.
	ctx.StatusCode(201)
	ctx.JSON(map[string]any{
		"user": user, "password": req.Password, "api_key": req.APIKey, "generated": generated,
	})
}

func (s *Server) adminUpdateDirectTenantUser(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var req struct {
		TenantID uint   `json:"tenant_id"`
		Username string `json:"username"`
		Password string `json:"password"` // empty = keep existing
		APIKey   string `json:"api_key"`
	}
	if err := ctx.ReadJSON(&req); err != nil || req.TenantID == 0 || strings.TrimSpace(req.Username) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "tenant_id and username required"})
		return
	}
	// Empty password = unchanged (upstream keeps the existing one). An empty
	// API key would be CLEARED upstream, so preserve the current value.
	if req.APIKey == "" {
		if users, lerr := s.FX.ListUsers(req.TenantID); lerr == nil {
			for _, u := range users {
				if u.ID == id {
					req.APIKey = u.APIKey
					break
				}
			}
		}
	}
	if err := s.FX.UpdateTenantUser(id, req.TenantID, strings.TrimSpace(req.Username), req.Password, req.APIKey); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_USER_UPDATE", fmt.Sprintf("user:%d", id), map[string]any{"tenant_id": req.TenantID, "username": req.Username, "password_changed": req.Password != ""})
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminDeleteDirectTenantUser(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	if err := s.FX.DeleteTenantUser(id); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_USER_DELETE", fmt.Sprintf("user:%d", id), nil)
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Direct tenant numbers ----------

type directNumberReq struct {
	Number string `json:"number"`
	Name   string `json:"name"`
	Header string `json:"header"`
	Notify string `json:"notify"`
}

func (s *Server) adminCreateDirectNumber(ctx iris.Context) {
	tid := ctx.Params().GetUintDefault("id", 0)
	var req directNumberReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.Number) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "number required"})
		return
	}
	number := sanitizeNumber(req.Number)
	if len(number) < 7 {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "number must contain at least 7 digits"})
		return
	}
	created, err := s.FX.AddNumber(tid, number, req.Name, req.Header, req.Notify)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream add failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_NUMBER_ASSIGN", number, map[string]any{"tenant_id": tid, "gofax_number_id": created.ID})
	ctx.StatusCode(201)
	ctx.JSON(created)
}

func (s *Server) adminUpdateDirectNumber(ctx iris.Context) {
	id := ctx.Params().GetUintDefault("id", 0)
	var req struct {
		TenantID uint   `json:"tenant_id"`
		Number   string `json:"number"`
		Name     string `json:"name"`
		Header   string `json:"header"`
		Notify   string `json:"notify"`
	}
	if err := ctx.ReadJSON(&req); err != nil || req.TenantID == 0 || strings.TrimSpace(req.Number) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "tenant_id and number required"})
		return
	}
	if err := s.FX.UpdateNumber(id, req.TenantID, req.Number, req.Name, req.Header, req.Notify); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream update failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_NUMBER_UPDATE", req.Number, map[string]any{"tenant_id": req.TenantID, "gofax_number_id": id})
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminDeleteDirectNumber(ctx iris.Context) {
	tid := uint(ctx.URLParamIntDefault("tenant_id", 0))
	number := ctx.URLParam("number")
	if tid == 0 || number == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "tenant_id and number query params required"})
		return
	}
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "refusing to delete number without confirm=true query param"})
		return
	}
	if err := s.FX.DeleteNumber(tid, number); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream delete failed: " + err.Error()})
		return
	}
	s.audit(ctx, "DIRECT_NUMBER_DELETE", number, map[string]any{"tenant_id": tid})
	ctx.JSON(map[string]bool{"ok": true})
}

// ---------- Provision gateway from existing endpoint ----------

func (s *Server) adminProvisionGatewayFromEndpoint(ctx iris.Context) {
	var spec fsclient.ProvisionGatewayFromEndpointSpec
	if err := ctx.ReadJSON(&spec); err != nil {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "invalid payload"})
		return
	}
	if spec.EndpointID == 0 || spec.TemplateID == 0 {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "endpoint_id and template_id required"})
		return
	}
	gs, err := s.FX.ProvisionGatewayFromEndpoint(spec)
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream provision failed: " + err.Error()})
		return
	}
	s.audit(ctx, "GATEWAY_PROVISION_FROM_ENDPOINT", "gateway:"+gs.Gateway.Name, map[string]any{"endpoint_id": spec.EndpointID, "template_id": spec.TemplateID})
	ctx.StatusCode(201)
	ctx.JSON(gs)
}

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
	"net/http"
	"strconv"

	"github.com/kataras/iris/v12"
)

// Admin handlers for FreeSWITCH gateway template management and gateway
// provisioning. See gateway_provision.go for the implementation.

// gatewayTemplateView is a template plus its extracted variable list, so API
// consumers can render a dynamic form.
type gatewayTemplateView struct {
	GatewayTemplate
	Variables []string `json:"variables"`
}

func templateViews(tpls []GatewayTemplate) ([]gatewayTemplateView, error) {
	out := make([]gatewayTemplateView, 0, len(tpls))
	for _, t := range tpls {
		vars, err := TemplateVariables(t.Body)
		if err != nil {
			return nil, err
		}
		out = append(out, gatewayTemplateView{GatewayTemplate: t, Variables: vars})
	}
	return out, nil
}

func (s *Server) handleListGatewayTemplates(ctx iris.Context) {
	var tpls []GatewayTemplate
	if err := s.DB.Find(&tpls).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to list templates: " + err.Error()})
		return
	}
	views, err := templateViews(tpls)
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(views)
}

func (s *Server) handleCreateGatewayTemplate(ctx iris.Context) {
	var tpl GatewayTemplate
	if err := ctx.ReadJSON(&tpl); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if tpl.Name == "" || tpl.Body == "" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "name and body are required"})
		return
	}
	if _, err := TemplateVariables(tpl.Body); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	if err := s.DB.Create(&tpl).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to create template: " + err.Error()})
		return
	}
	vars, _ := TemplateVariables(tpl.Body)
	ctx.StatusCode(http.StatusCreated)
	ctx.JSON(gatewayTemplateView{GatewayTemplate: tpl, Variables: vars})
}

func (s *Server) handleUpdateGatewayTemplate(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid template id"})
		return
	}
	var tpl GatewayTemplate
	if err := ctx.ReadJSON(&tpl); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if _, err := TemplateVariables(tpl.Body); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	tpl.ID = uint(id)
	if err := s.DB.Save(&tpl).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to update template: " + err.Error()})
		return
	}
	vars, _ := TemplateVariables(tpl.Body)
	ctx.JSON(gatewayTemplateView{GatewayTemplate: tpl, Variables: vars})
}

func (s *Server) handleDeleteGatewayTemplate(ctx iris.Context) {
	id, err := strconv.Atoi(ctx.Params().Get("id"))
	if err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid template id"})
		return
	}
	var inUse int64
	s.DB.Model(&GatewayConfig{}).Where("template_id = ?", id).Count(&inUse)
	if inUse > 0 {
		ctx.StatusCode(http.StatusConflict)
		ctx.JSON(iris.Map{"error": "template is used by provisioned gateways"})
		return
	}
	if err := s.DB.Delete(&GatewayTemplate{}, id).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to delete template: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}

func (s *Server) handleListGateways(ctx iris.Context) {
	overview, err := s.ListGateways()
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to list gateways: " + err.Error()})
		return
	}
	ctx.JSON(overview)
}

func (s *Server) handleAdoptGateway(ctx iris.Context) {
	var req struct {
		File string `json:"file"`
	}
	if err := ctx.ReadJSON(&req); err != nil || req.File == "" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "file is required"})
		return
	}
	gs, err := s.AdoptGateway(req.File)
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(http.StatusCreated)
	ctx.JSON(gs)
}

func (s *Server) handleDeleteUnmanagedGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "refusing to delete unmanaged gateway without confirm=true query param"})
		return
	}
	if err := s.DeleteUnmanagedGateway(name); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}

func (s *Server) handleRepairGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	gs, err := s.RepairGateway(name)
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(gs)
}

func (s *Server) handleProvisionGateway(ctx iris.Context) {
	var spec GatewayProvisionSpec
	if err := ctx.ReadJSON(&spec); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	gs, err := s.ProvisionGateway(spec)
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(http.StatusCreated)
	ctx.JSON(gs)
}

func (s *Server) handleUpdateGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	var spec GatewayProvisionSpec
	if err := ctx.ReadJSON(&spec); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	gs, err := s.UpdateGateway(name, spec)
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(gs)
}

func (s *Server) handleProvisionGatewayFromEndpoint(ctx iris.Context) {
	var spec ProvisionGatewayFromEndpointSpec
	if err := ctx.ReadJSON(&spec); err != nil {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "invalid payload: " + err.Error()})
		return
	}
	if spec.EndpointID == 0 || spec.TemplateID == 0 {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "endpoint_id and template_id are required"})
		return
	}
	gs, err := s.ProvisionGatewayFromEndpoint(spec)
	if err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(http.StatusCreated)
	ctx.JSON(gs)
}

func (s *Server) handleDeprovisionGateway(ctx iris.Context) {
	name := ctx.Params().Get("name")
	if err := s.DeprovisionGateway(name); err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true})
}

// handleRescanFSProfile reloads the XML config and rescans the gateway
// profile — safe, no impact on active calls.
func (s *Server) handleRescanFSProfile(ctx iris.Context) {
	if err := fsReloadGateways(); err != nil {
		ctx.StatusCode(http.StatusBadGateway)
		ctx.JSON(iris.Map{"error": "freeswitch rescan failed: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true, "profile": gatewayProfile()})
}

// handleRestartFSProfile restarts the sofia profile, dropping active calls
// on it — requires confirm=true.
func (s *Server) handleRestartFSProfile(ctx iris.Context) {
	if ctx.URLParam("confirm") != "true" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "refusing to restart the sofia profile (drops active calls) without confirm=true query param"})
		return
	}
	if err := fsRestartProfile(); err != nil {
		ctx.StatusCode(http.StatusBadGateway)
		ctx.JSON(iris.Map{"error": "freeswitch profile restart failed: " + err.Error()})
		return
	}
	ctx.JSON(iris.Map{"ok": true, "profile": gatewayProfile()})
}

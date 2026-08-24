package gofaxserver

import (
	"net/http"
	"strconv"

	"github.com/kataras/iris/v12"
)

// TenantUserPublic is the redacted view of a tenant user returned by listing
// endpoints. The encrypted password material is never exposed.
type TenantUserPublic struct {
	ID       uint   `json:"id"`
	TenantID uint   `json:"tenant_id"`
	Username string `json:"username"`
	APIKey   string `json:"api_key"`
}

// handleListTenants returns all tenants with their numbers preloaded.
// Optional query parameter: none.
func (s *Server) handleListTenants(ctx iris.Context) {
	var tenants []Tenant
	if err := s.DB.Preload("Numbers").Find(&tenants).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve tenants: " + err.Error()})
		return
	}
	ctx.JSON(tenants)
}

// handleListTenantNumbers returns all tenant numbers, optionally filtered by
// tenant_id query parameter.
func (s *Server) handleListTenantNumbers(ctx iris.Context) {
	query := s.DB.Model(&TenantNumber{})
	if v := ctx.URLParam("tenant_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			ctx.StatusCode(http.StatusBadRequest)
			ctx.JSON(iris.Map{"error": "invalid tenant_id"})
			return
		}
		query = query.Where("tenant_id = ?", id)
	}
	var numbers []TenantNumber
	if err := query.Find(&numbers).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve numbers: " + err.Error()})
		return
	}
	ctx.JSON(numbers)
}

// handleListTenantUsers returns all tenant users (passwords redacted),
// optionally filtered by tenant_id query parameter.
func (s *Server) handleListTenantUsers(ctx iris.Context) {
	query := s.DB.Model(&TenantUser{})
	if v := ctx.URLParam("tenant_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			ctx.StatusCode(http.StatusBadRequest)
			ctx.JSON(iris.Map{"error": "invalid tenant_id"})
			return
		}
		query = query.Where("tenant_id = ?", id)
	}
	var users []TenantUserPublic
	if err := query.Select("id", "tenant_id", "username", "api_key").Scan(&users).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve users: " + err.Error()})
		return
	}
	ctx.JSON(users)
}

// handleListEndpoints returns all endpoints, optionally filtered by type
// ("tenant", "number", or "global") and/or type_id query parameters.
func (s *Server) handleListEndpoints(ctx iris.Context) {
	query := s.DB.Model(&Endpoint{})
	if t := ctx.URLParam("type"); t != "" {
		query = query.Where("type = ?", t)
	}
	if tid := ctx.URLParam("type_id"); tid != "" {
		id, err := strconv.ParseUint(tid, 10, 64)
		if err != nil {
			ctx.StatusCode(http.StatusBadRequest)
			ctx.JSON(iris.Map{"error": "invalid type_id"})
			return
		}
		query = query.Where("type_id = ?", id)
	}
	var endpoints []Endpoint
	if err := query.Find(&endpoints).Error; err != nil {
		ctx.StatusCode(http.StatusInternalServerError)
		ctx.JSON(iris.Map{"error": "failed to retrieve endpoints: " + err.Error()})
		return
	}
	ctx.JSON(endpoints)
}

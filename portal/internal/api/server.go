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
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"gofaxportal/internal/auth"
	"gofaxportal/internal/config"
	"gofaxportal/internal/crypto"
	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"
	"gofaxportal/internal/web"

	"github.com/kataras/iris/v12"
	"gorm.io/gorm"
)

type Server struct {
	Cfg        *config.Config
	DB         *gorm.DB
	Auth       *auth.Service
	Box        *crypto.Box
	FaxBox     *crypto.Box // domain-separated box sealing received fax PDFs at rest
	FX         *fsclient.Client
	LoginLimit *auth.RateLimiter
	SendLimit  *auth.RateLimiter
}

func New(cfg *config.Config, db *gorm.DB, authSvc *auth.Service, box, faxBox *crypto.Box, fx *fsclient.Client) *Server {
	return &Server{
		Cfg:        cfg,
		DB:         db,
		Auth:       authSvc,
		Box:        box,
		FaxBox:     faxBox,
		FX:         fx,
		LoginLimit: auth.NewRateLimiter(time.Minute),
		SendLimit:  auth.NewRateLimiter(time.Hour),
	}
}

func (s *Server) BuildApp() *iris.Application {
	app := iris.New()
	app.OnErrorCode(iris.StatusInternalServerError, func(ctx iris.Context) {
		ctx.JSON(map[string]string{"error": "internal server error"})
	})

	// The portal API and SPA live under /portal so a single Caddy host can
	// split traffic: /portal/* → portal, everything else → gofaxserver.
	// Existing gofaxserver API clients are unaffected either way.
	apiParty := app.Party("/portal/api")
	apiParty.Post("/auth/login", s.handleLogin)
	apiParty.Get("/health", func(ctx iris.Context) {
		ctx.JSON(map[string]string{"status": "ok"})
	})

	// Inbound fax delivery from gofaxserver. Deliberately outside the
	// session/CSRF middleware: the caller is gofaxserver itself, authorized
	// by the per-org service-account path + optional pre-shared X-API-Key.
	apiParty.Post("/inbound/{svc_username}", s.handleInboundFax)
	// Outbound job status push from gofaxserver's notify system (notify
	// destination type "portal"). Same auth model as inbound delivery.
	apiParty.Post("/notify/{svc_username}", s.handleStatusNotify)

	// --- authenticated ---
	authed := apiParty.Party("", s.authenticate, s.requireAuth)
	authed.Post("/auth/logout", s.handleLogout)
	authed.Get("/auth/me", s.handleMe)

	// --- user realm (role=user, scoped to own org) ---
	userParty := apiParty.Party("", s.authenticate, s.requireAuth, s.requireUserRealm)
	userParty.Get("/me/numbers", s.handleMyNumbers)
	userParty.Get("/faxes", s.handleListJobs)
	userParty.Get("/faxes/{id:uint}", s.handleGetJob)
	userParty.Post("/faxes", s.handleSendFax)
	userParty.Get("/inbox", s.handleListInbox)
	userParty.Get("/inbox/{id:uint}", s.handleGetInbound)
	userParty.Get("/inbox/{id:uint}/file", s.handleGetInboundFile)

	// --- admin realm ---
	admin := apiParty.Party("/admin", s.authenticate, s.requireAuth, s.requireAdminRealm)
	admin.Get("/users", s.adminListUsers)
	admin.Post("/users", s.adminCreateUser)
	admin.Put("/users/{id:uint}", s.adminUpdateUser)
	admin.Post("/users/{id:uint}/password", s.adminResetPassword)
	admin.Delete("/users/{id:uint}", s.adminDeleteUser)

	orgs := admin.Party("/orgs")
	orgs.Get("/", s.adminListOrgs)
	orgs.Post("/", s.adminCreateOrg)
	orgs.Put("/{id:uint}", s.adminUpdateOrg)
	orgs.Delete("/{id:uint}", s.adminDeleteOrg)
	orgs.Get("/{id:uint}/reconcile", s.adminReconcileOrg)
	orgs.Post("/{id:uint}/credentials/rotate", s.adminRotateSvcCredentials)
	orgs.Post("/{id:uint}/notify/resync", s.adminResyncOrgNotify)

	numbers := admin.Party("/numbers")
	numbers.Get("/", s.adminListNumbers)
	numbers.Post("/", s.adminCreateNumber)
	numbers.Put("/{id:uint}", s.adminUpdateNumber)
	numbers.Delete("/{id:uint}", s.adminDeleteNumber)
	numbers.Get("/{id:uint}/assignments", s.adminGetAssignments)
	numbers.Put("/{id:uint}/assignments", s.adminSetAssignments)

	endpoints := admin.Party("/endpoints")
	endpoints.Get("/", s.adminListEndpoints)
	endpoints.Post("/", s.adminCreateEndpoint)
	endpoints.Put("/{id:uint}", s.adminUpdateEndpoint)
	endpoints.Delete("/{id:uint}", s.adminDeleteEndpoint)

	gwTemplates := admin.Party("/gateway-templates")
	gwTemplates.Get("/", s.adminListGatewayTemplates)
	gwTemplates.Post("/", s.adminCreateGatewayTemplate)
	gwTemplates.Put("/{id:uint}", s.adminUpdateGatewayTemplate)
	gwTemplates.Delete("/{id:uint}", s.adminDeleteGatewayTemplate)

	gateways := admin.Party("/gateways")
	gateways.Get("/", s.adminListGateways)
	gateways.Post("/", s.adminProvisionGateway)
	gateways.Post("/adopt", s.adminAdoptGateway)
	gateways.Post("/from-endpoint", s.adminProvisionGatewayFromEndpoint)
	gateways.Delete("/unmanaged/{name}", s.adminDeleteUnmanagedGateway)
	gateways.Post("/{name}/repair", s.adminRepairGateway)
	gateways.Put("/{name}", s.adminUpdateGateway)
	gateways.Delete("/{name}", s.adminDeprovisionGateway)

	// FreeSWITCH profile control.
	admin.Post("/freeswitch/rescan", s.adminRescanFSProfile)
	admin.Post("/freeswitch/profile/restart", s.adminRestartFSProfile)

	// Upstream pickers (feed scope dropdowns; direct tenants included).
	admin.Get("/upstream/tenants", s.adminListUpstreamTenants)
	admin.Get("/upstream/numbers", s.adminListUpstreamNumbers)

	// Direct (non-portal) gofaxserver tenants: pure proxy, nothing mirrored.
	direct := admin.Party("/tenants")
	direct.Get("/", s.adminListUpstreamTenants)
	direct.Post("/", s.adminCreateDirectTenant)
	direct.Put("/{id:uint}", s.adminUpdateDirectTenant)
	direct.Delete("/{id:uint}", s.adminDeleteDirectTenant)
	direct.Get("/{id:uint}/users", s.adminListDirectTenantUsers)
	direct.Post("/{id:uint}/users", s.adminCreateDirectTenantUser)
	direct.Post("/{id:uint}/numbers", s.adminCreateDirectNumber)

	admin.Put("/tenant-users/{id:uint}", s.adminUpdateDirectTenantUser)
	admin.Delete("/tenant-users/{id:uint}", s.adminDeleteDirectTenantUser)
	admin.Put("/tenant-numbers/{id:uint}", s.adminUpdateDirectNumber)
	admin.Delete("/tenant-numbers/{id:uint}", s.adminDeleteDirectNumber)

	dialplan := admin.Party("/dialplan")
	dialplan.Get("/", s.adminGetDialplan)
	dialplan.Post("/rules", s.adminCreateDialplanRule)
	dialplan.Put("/rules/{id:uint}", s.adminUpdateDialplanRule)
	dialplan.Delete("/rules/{id:uint}", s.adminDeleteDialplanRule)
	dialplan.Post("/rules/reorder", s.adminReorderDialplanRules)

	faxpolicies := admin.Party("/fax-policies")
	faxpolicies.Get("/", s.adminListFaxPolicies)
	faxpolicies.Post("/", s.adminCreateFaxPolicyRule)
	faxpolicies.Put("/{id:uint}", s.adminUpdateFaxPolicyRule)
	faxpolicies.Delete("/{id:uint}", s.adminDeleteFaxPolicyRule)
	faxpolicies.Post("/{id:uint}/expire", s.adminExpireFaxPolicyRule)
	faxpolicies.Get("/resolve", s.adminResolveFaxPolicy)
	faxpolicies.Get("/pair-states", s.adminListFaxPairStates)
	faxpolicies.Delete("/pair-states/{id:uint}", s.adminDeleteFaxPairState)

	admin.Get("/faxes/active", s.adminActiveFaxes)
	admin.Get("/jobs", s.adminListAllJobs)
	admin.Get("/jobs/{id:uint}/live", s.adminJobLive)
	admin.Get("/inbox", s.adminListInbound)
	admin.Get("/inbox/{id:uint}/file", s.adminGetInboundFile)
	admin.Delete("/inbox/{id:uint}", s.adminDeleteInbound)
	admin.Get("/audit", s.adminAuditLog)

	// Unknown /portal/api paths must return JSON 404s, never the SPA shell.
	apiParty.HandleMany("GET POST PUT PATCH DELETE", "/{p:path}", func(ctx iris.Context) {
		ctx.StatusCode(iris.StatusNotFound)
		ctx.JSON(map[string]string{"error": "not found"})
	})

	// Convenience redirect: / lands on the SPA (HandleDir canonicalizes
	// /portal/ -> /portal itself).
	app.Get("/", func(ctx iris.Context) { ctx.Redirect("/portal") })

	// --- embedded SPA under /portal (registered last; explicit routes win) ---
	distFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic("embedded dist missing: " + err.Error())
	}
	app.HandleDir("/portal", http.FS(distFS), iris.DirOptions{
		IndexName: "/index.html",
		SPA:       true,
	})

	return app
}

// --- context helpers ---

func currentUser(ctx iris.Context) (*models.Session, *models.PortalUser, bool) {
	sess, _ := ctx.Values().Get("session").(*models.Session)
	user, _ := ctx.Values().Get("user").(*models.PortalUser)
	if sess == nil || user == nil {
		return nil, nil, false
	}
	return sess, user, true
}

func currentOrgID(ctx iris.Context) (uint, bool) {
	v := ctx.Values().Get("orgID")
	id, ok := v.(uint)
	return id, ok
}

// authenticate loads the session (if any) into context. Anonymous requests
// pass through and are rejected later by requireAuth where needed.
func (s *Server) authenticate(ctx iris.Context) {
	c, err := ctx.Request().Cookie(auth.SessionCookie)
	if err == nil && c.Value != "" {
		if sess, user, lerr := s.Auth.Lookup(c.Value); lerr == nil {
			ctx.Values().Set("session", sess)
			ctx.Values().Set("user", user)
			if user.OrgID != nil {
				ctx.Values().Set("orgID", *user.OrgID)
			}
		}
	}
	ctx.Next()
}

var mutatingMethods = map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// requireAuth rejects anonymous requests and enforces CSRF on mutations.
func (s *Server) requireAuth(ctx iris.Context) {
	sess, user, ok := currentUser(ctx)
	if !ok || !user.Active {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "authentication required"})
		return
	}
	if mutatingMethods[ctx.Method()] && !auth.SecureEquals(ctx.GetHeader("X-CSRF-Token"), sess.CSRFToken) {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "csrf validation failed"})
		return
	}
	ctx.Next()
}

// requireAdminRealm gates the admin surface.
func (s *Server) requireAdminRealm(ctx iris.Context) {
	_, user, ok := currentUser(ctx)
	if !ok || user.Role != models.RoleAdmin {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "admin access required"})
		return
	}
	ctx.Next()
}

// requireUserRealm gates the sending surface (admins manage; users send).
func (s *Server) requireUserRealm(ctx iris.Context) {
	_, user, ok := currentUser(ctx)
	if !ok {
		ctx.StatusCode(iris.StatusUnauthorized)
		ctx.JSON(map[string]string{"error": "authentication required"})
		return
	}
	if user.Role != models.RoleUser {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "this area is for fax user accounts"})
		return
	}
	if _, has := currentOrgID(ctx); !has {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "account is not linked to an organization"})
		return
	}
	var org models.Org
	if err := s.DB.First(&org, user.OrgID).Error; err != nil || !org.Active {
		ctx.StatusCode(iris.StatusForbidden)
		ctx.JSON(map[string]string{"error": "organization is inactive"})
		return
	}
	ctx.Next()
}

// audit records an auditable event; never fails the request.
func (s *Server) audit(ctx iris.Context, action, target string, detail any) {
	entry := models.AuditLog{Action: action, Target: target, IP: ctx.RemoteAddr()}
	if _, user, ok := currentUser(ctx); ok {
		uid := user.ID
		entry.ActorID = &uid
		entry.ActorUsername = user.Username
	}
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			entry.Detail = string(b)
		}
	}
	_ = s.DB.Create(&entry).Error
}

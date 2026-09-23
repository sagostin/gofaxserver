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
	"testing"

	"gofaxportal/internal/config"

	"github.com/kataras/iris/v12/httptest"
)

// testServer builds the full route tree without a database; anonymous-path
// and middleware behavior must not touch the DB when no session is present.
func testServer(t *testing.T) *httptest.Expect {
	t.Helper()
	cfg := &config.Config{}
	s := &Server{Cfg: cfg}
	app := s.BuildApp()
	return httptest.New(t, app)
}

func TestHealthIsPublic(t *testing.T) {
	testServer(t).GET("/portal/api/health").Expect().Status(200).
		JSON().Object().ValueEqual("status", "ok")
}

func TestUnknownApiPathReturnsJSON404NotSPA(t *testing.T) {
	testServer(t).GET("/portal/api/definitely-not-a-route").Expect().
		Status(404).JSON().Object().Keys().Contains("error")
}

func TestProtectedRoutesRejectAnonymous(t *testing.T) {
	e := testServer(t)
	e.GET("/portal/api/auth/me").Expect().Status(401)
	e.GET("/portal/api/me/numbers").Expect().Status(401)
	e.GET("/portal/api/faxes").Expect().Status(401)
	e.GET("/portal/api/admin/orgs").Expect().Status(401)
	e.GET("/portal/api/admin/jobs").Expect().Status(401)
	e.GET("/portal/api/admin/faxes/active").Expect().Status(401)
	// Gateway provisioning + dialplan routes
	e.GET("/portal/api/admin/gateways").Expect().Status(401)
	e.POST("/portal/api/admin/gateways").Expect().Status(401)
	e.POST("/portal/api/admin/gateways/adopt").Expect().Status(401)
	e.GET("/portal/api/admin/gateway-templates").Expect().Status(401)
	e.GET("/portal/api/admin/dialplan").Expect().Status(401)
	e.POST("/portal/api/admin/dialplan/rules").Expect().Status(401)
}

func TestSPAServesUnderPortalPrefix(t *testing.T) {
	// HandleDir canonicalizes the SPA root at /portal (and redirects /portal/ here).
	testServer(t).GET("/portal").Expect().Status(200).ContentType("text/html")
}

func TestRootRedirectsToPortal(t *testing.T) {
	// httpexpect follows redirects by default; assert the chain ends at the SPA.
	testServer(t).GET("/").Expect().Status(200).ContentType("text/html")
}

func TestSPAFallbackServesIndexForAppRoutes(t *testing.T) {
	// Client-side routes must resolve to the SPA shell (history mode).
	testServer(t).GET("/portal/admin/users").Expect().Status(200).ContentType("text/html")
	testServer(t).GET("/portal/app/faxes").Expect().Status(200).ContentType("text/html")
}

func TestLoginRequiresFields(t *testing.T) {
	testServer(t).POST("/portal/api/auth/login").WithJSON(map[string]string{}).
		Expect().Status(400)
}

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

func TestBrandingIsPublic(t *testing.T) {
	// No DB in the test server: branding falls back to built-in defaults.
	e := testServer(t)
	e.GET("/portal/api/branding").Expect().Status(200).
		JSON().Object().ValueEqual("name", "Fax Portal").
		ValueEqual("has_logo", false).ValueEqual("has_favicon", false)
	e.GET("/portal/api/branding/logo").Expect().Status(404)
	e.GET("/portal/api/branding/favicon").Expect().Status(404)
}

func TestBrandingAdminRoutesRejectAnonymous(t *testing.T) {
	e := testServer(t)
	e.PUT("/portal/api/admin/branding").WithJSON(map[string]string{"name": "X"}).Expect().Status(401)
	e.POST("/portal/api/admin/branding/logo").Expect().Status(401)
	e.DELETE("/portal/api/admin/branding/logo").Expect().Status(401)
	e.POST("/portal/api/admin/branding/favicon").Expect().Status(401)
	e.DELETE("/portal/api/admin/branding/favicon").Expect().Status(401)
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
	// Org maintenance routes
	e.POST("/portal/api/admin/orgs/1/credentials/rotate").Expect().Status(401)
	e.POST("/portal/api/admin/orgs/1/notify/resync").Expect().Status(401)
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

func TestTOTPResetIsAdminOnly(t *testing.T) {
	testServer(t).POST("/portal/api/admin/users/1/totp/reset").Expect().Status(401)
}

func TestTOTPSetupRequiresToken(t *testing.T) {
	// Missing token is rejected before any DB access.
	testServer(t).POST("/portal/api/auth/totp/setup").WithJSON(map[string]string{}).
		Expect().Status(400)
}

func TestTOTPVerifyRequiresFields(t *testing.T) {
	e := testServer(t)
	e.POST("/portal/api/auth/totp/verify").WithJSON(map[string]string{}).
		Expect().Status(400)
	e.POST("/portal/api/auth/totp/verify").WithJSON(map[string]string{"mfa_token": "x"}).
		Expect().Status(400)
}

// iris httptest uses an in-memory listener with an empty RemoteAddr, so the
// client-IP resolution logic is tested at the pure-function level.

func TestResolveClientIPHonorsXFFFromTrustedProxy(t *testing.T) {
	// Last XFF entry wins (the one appended by the trusted proxy); earlier
	// entries may be client-supplied spoofs.
	got := resolveClientIP("127.0.0.1:8081", "", "9.9.9.9, 203.0.113.7", []string{"127.0.0.1", "::1"})
	if got != "203.0.113.7" {
		t.Errorf("got %q", got)
	}
}

func TestResolveClientIPPrefersXRealIP(t *testing.T) {
	got := resolveClientIP("127.0.0.1:8081", "198.51.100.23", "9.9.9.9", []string{"127.0.0.1"})
	if got != "198.51.100.23" {
		t.Errorf("got %q", got)
	}
}

func TestResolveClientIPIgnoresHeadersFromUntrustedPeer(t *testing.T) {
	// Direct peer is NOT in the trusted list: forwarded headers must be
	// ignored and the direct peer reported instead.
	got := resolveClientIP("192.0.2.55:12345", "", "9.9.9.9", []string{"127.0.0.1"})
	if got != "192.0.2.55" {
		t.Errorf("got %q", got)
	}
}

func TestResolveClientIPTrustedCIDR(t *testing.T) {
	got := resolveClientIP("10.1.2.3:9000", "", "203.0.113.9", []string{"10.0.0.0/8"})
	if got != "203.0.113.9" {
		t.Errorf("got %q", got)
	}
}

func TestResolveClientIPFallsBackWithoutHeaders(t *testing.T) {
	got := resolveClientIP("203.0.113.10:4567", "", "", []string{"127.0.0.1"})
	if got != "203.0.113.10" {
		t.Errorf("got %q", got)
	}
}

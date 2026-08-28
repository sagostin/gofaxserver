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
	testServer(t).GET("/api/health").Expect().Status(200).
		JSON().Object().ValueEqual("status", "ok")
}

func TestUnknownApiPathReturnsJSON404NotSPA(t *testing.T) {
	testServer(t).GET("/api/definitely-not-a-route").Expect().
		Status(404).JSON().Object().Keys().Contains("error")
}

func TestProtectedRoutesRejectAnonymous(t *testing.T) {
	e := testServer(t)
	e.GET("/api/auth/me").Expect().Status(401)
	e.GET("/api/me/numbers").Expect().Status(401)
	e.GET("/api/faxes").Expect().Status(401)
	e.GET("/api/admin/orgs").Expect().Status(401)
	e.GET("/api/admin/jobs").Expect().Status(401)
	e.GET("/api/admin/faxes/active").Expect().Status(401)
}

func TestSPAServesIndexAtRoot(t *testing.T) {
	res := testServer(t).GET("/").Expect().Status(200)
	res.ContentType("text/html")
}

func TestSPAFallbackServesIndexForAppRoutes(t *testing.T) {
	// Client-side routes must resolve to the SPA shell (history mode).
	testServer(t).GET("/admin/users").Expect().Status(200).ContentType("text/html")
	testServer(t).GET("/app/faxes").Expect().Status(200).ContentType("text/html")
}

func TestLoginRequiresFields(t *testing.T) {
	testServer(t).POST("/api/auth/login").WithJSON(map[string]string{}).
		Expect().Status(400)
}

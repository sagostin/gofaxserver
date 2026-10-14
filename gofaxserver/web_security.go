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

// web_security.go — admin API for the SIP brute-force sentry: ban listing,
// manual ban/unban, and metrics.

package gofaxserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"gofaxserver/gofaxlib"
)

// sentryOr404 returns the sentry or writes a 404 when the feature is off.
func (s *Server) sentryOr404(ctx iris.Context) *IPSentry {
	if s.IPSentry == nil {
		ctx.StatusCode(http.StatusNotFound)
		ctx.JSON(iris.Map{"error": "security sentry is disabled (security.enabled=false)"})
		return nil
	}
	return s.IPSentry
}

// handleSecurityStatus reports sentry config + metrics.
func (s *Server) handleSecurityStatus(ctx iris.Context) {
	if s.IPSentry == nil {
		ctx.JSON(SentryMetrics{Enabled: false, Backend: gofaxlib.Config.Security.BackendName()})
		return
	}
	ctx.JSON(s.IPSentry.Metrics())
}

// handleListBlockedIPs lists active bans (?all=true includes history).
func (s *Server) handleListBlockedIPs(ctx iris.Context) {
	sentry := s.sentryOr404(ctx)
	if sentry == nil {
		return
	}
	ctx.JSON(sentry.ListBans(ctx.URLParamExists("all")))
}

type banRequest struct {
	IP       string `json:"ip"`
	Reason   string `json:"reason"`
	Duration string `json:"duration"` // Go duration; empty = permanent
}

// handleBanIP manually bans an IP.
func (s *Server) handleBanIP(ctx iris.Context) {
	sentry := s.sentryOr404(ctx)
	if sentry == nil {
		return
	}
	var req banRequest
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.IP) == "" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "ip is required"})
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if req.Reason == "" {
		req.Reason = "manual ban via admin API"
	}
	var dur *time.Duration
	if req.Duration != "" {
		d, err := time.ParseDuration(req.Duration)
		if err != nil || d <= 0 {
			ctx.StatusCode(http.StatusBadRequest)
			ctx.JSON(iris.Map{"error": "invalid duration (Go duration string, e.g. \"24h\")"})
			return
		}
		dur = &d
	}
	if err := sentry.BanManual(req.IP, req.Reason, dur); err != nil {
		ctx.StatusCode(http.StatusConflict)
		ctx.JSON(iris.Map{"error": err.Error()})
		return
	}
	ctx.JSON(iris.Map{"message": "banned " + req.IP})
}

// handleUnbanIP revokes a ban.
func (s *Server) handleUnbanIP(ctx iris.Context) {
	sentry := s.sentryOr404(ctx)
	if sentry == nil {
		return
	}
	ipStr := strings.TrimSpace(ctx.Params().Get("ip"))
	if ipStr == "" {
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(iris.Map{"error": "ip parameter required"})
		return
	}
	if !sentry.UnbanIP(ipStr) {
		ctx.StatusCode(http.StatusNotFound)
		ctx.JSON(iris.Map{"error": ipStr + " is not banned"})
		return
	}
	ctx.JSON(iris.Map{"message": "unbanned " + ipStr})
}

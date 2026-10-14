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

// handlers_admin_security.go — admin surface for gofaxserver's SIP
// brute-force sentry: status/metrics, ban listing, manual ban/unban.
// Everything lives upstream (no portal DB mirror); mutations are audited.

package api

import (
	"strings"

	"github.com/kataras/iris/v12"
)

func (s *Server) adminSecurityStatus(ctx iris.Context) {
	out, err := s.FX.SecurityStatus()
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "upstream security status failed: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

func (s *Server) adminListBlockedIPs(ctx iris.Context) {
	out, err := s.FX.ListBlockedIPs(ctx.URLParamExists("all"))
	if err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "failed to list blocked IPs: " + err.Error()})
		return
	}
	ctx.JSON(out)
}

type adminBanReq struct {
	IP       string `json:"ip"`
	Reason   string `json:"reason"`
	Duration string `json:"duration"`
}

func (s *Server) adminBanIP(ctx iris.Context) {
	var req adminBanReq
	if err := ctx.ReadJSON(&req); err != nil || strings.TrimSpace(req.IP) == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "ip is required"})
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if err := s.FX.BanIP(req.IP, req.Reason, req.Duration); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "ban failed: " + err.Error()})
		return
	}
	s.audit(ctx, "SECURITY_IP_BAN", req.IP, map[string]string{"reason": req.Reason, "duration": req.Duration})
	ctx.JSON(map[string]bool{"ok": true})
}

func (s *Server) adminUnbanIP(ctx iris.Context) {
	ip := strings.TrimSpace(ctx.Params().Get("ip"))
	if ip == "" {
		ctx.StatusCode(400)
		ctx.JSON(map[string]string{"error": "ip parameter required"})
		return
	}
	if err := s.FX.UnbanIP(ip); err != nil {
		ctx.StatusCode(502)
		ctx.JSON(map[string]string{"error": "unban failed: " + err.Error()})
		return
	}
	s.audit(ctx, "SECURITY_IP_UNBAN", ip, nil)
	ctx.JSON(map[string]bool{"ok": true})
}

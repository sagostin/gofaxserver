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
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
)

// Registration monitor: periodically polls `sofia status gateway` for
// provisioned gateways that use SIP registration (register=true) and records
// state transitions on the gateway_configs row. Log + dashboard visibility
// only — no alerting.

// monitorInterval returns the configured poll interval (default 60s; a
// negative value disables the monitor).
func monitorInterval() time.Duration {
	sec := gofaxlib.Config.FreeSwitch.GatewayMonitorSeconds
	switch {
	case sec < 0:
		return 0
	case sec == 0:
		return 60 * time.Second
	default:
		return time.Duration(sec) * time.Second
	}
}

// monitorTransition decides whether a state change is noteworthy and at what
// log level. Pure function for testability.
func monitorTransition(old, new string) (changed bool, level logrus.Level) {
	if old == new {
		return false, logrus.InfoLevel
	}
	// Registration up is informational; anything else (drop, failure,
	// unknown) is a warning.
	if new == "REGED" {
		return true, logrus.InfoLevel
	}
	return true, logrus.WarnLevel
}

// startGatewayMonitor runs the registration polling loop. Intended to be
// called as a goroutine from Server.Start after DB init.
func (s *Server) startGatewayMonitor() {
	interval := monitorInterval()
	if interval == 0 {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Gateway.Monitor", "registration monitor disabled (gateway_monitor_seconds < 0)",
			logrus.InfoLevel, nil,
		))
		return
	}
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"Gateway.Monitor", fmt.Sprintf("registration monitor running every %s", interval),
		logrus.InfoLevel, nil,
	))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.monitorTick()
	}
}

func (s *Server) monitorTick() {
	// DNS refresh runs first and independently of the GatewayConfig rows:
	// hostname ACL entries also come from manually managed endpoints.
	s.refreshGatewayACLResolution()

	var gws []GatewayConfig
	if err := s.DB.Find(&gws).Error; err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Gateway.Monitor", fmt.Sprintf("failed to list gateways: %v", err),
			logrus.ErrorLevel, nil,
		))
		return
	}
	for _, gw := range gws {
		params, err := decodeParams(gw.Params)
		if err != nil {
			continue
		}
		if reg, _ := params["register"].(bool); !reg {
			continue // IP-auth gateways have no registration to track
		}
		state := fsGatewayState(gw.Name)
		changed, level := monitorTransition(gw.LastState, state)
		if !changed {
			continue
		}
		now := time.Now().UTC()
		s.DB.Model(&gw).Updates(map[string]interface{}{
			"last_state":            state,
			"last_state_checked_at": now,
		})
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Gateway.Monitor",
			fmt.Sprintf("gateway %s registration state %s -> %s", gw.Name, gw.LastState, state),
			level,
			map[string]interface{}{"gateway": gw.Name, "old_state": gw.LastState, "new_state": state},
		))
	}
}

// equalIPs compares two IP lists order-insensitively (DNS resolvers may
// reorder A/AAAA records between lookups).
func equalIPs(a, b []string) bool {
	as := slices.Clone(a)
	bs := slices.Clone(b)
	sort.Strings(as)
	sort.Strings(bs)
	return slices.Equal(as, bs)
}

// refreshGatewayACLResolution re-resolves hostname-valued gateway ACL entries
// so inbound ACL matching tracks far-end IP changes without manual
// intervention. Last-good IPs are kept for entries whose lookup fails, so a
// transient DNS error never drops a gateway from the ACL.
func (s *Server) refreshGatewayACLResolution() {
	s.mu.RLock()
	entries := slices.Clone(s.GatewayEndpointsACL)
	previous := make(map[string][]string, len(s.GatewayACLResolved))
	for k, v := range s.GatewayACLResolved {
		previous[k] = slices.Clone(v)
	}
	s.mu.RUnlock()

	if len(entries) == 0 {
		return
	}

	resolved, failures := resolveGatewayACLs(entries)

	for entry, err := range failures {
		if old, ok := previous[entry]; ok {
			resolved[entry] = old // keep last-good IPs
		}
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"Gateway.Monitor",
			fmt.Sprintf("gateway ACL entry %q DNS resolution failed: %v (keeping previous IPs %v)", entry, err, previous[entry]),
			logrus.WarnLevel,
			map[string]interface{}{"endpoint": entry},
		))
	}

	// A changed IP set means the far end moved — worth surfacing.
	for entry, ips := range resolved {
		if old, ok := previous[entry]; !ok || !equalIPs(old, ips) {
			s.LogManager.SendLog(s.LogManager.BuildLog(
				"Gateway.Monitor",
				fmt.Sprintf("gateway ACL entry %q resolved IPs changed: %v -> %v", entry, previous[entry], ips),
				logrus.InfoLevel,
				map[string]interface{}{"endpoint": entry, "old_ips": previous[entry], "new_ips": ips},
			))
		}
	}

	s.mu.Lock()
	s.GatewayACLResolved = resolved
	s.mu.Unlock()
}

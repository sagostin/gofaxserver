package gofaxserver

import (
	"fmt"
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

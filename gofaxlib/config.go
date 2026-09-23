// This file is part of gofaxserver - https://github.com/sagostin/gofaxserver
// Originally part of the GOfax.IP project - https://github.com/gonicus/gofaxip
// Copyright (C) 2014 GONICUS GmbH, Germany - http://www.gonicus.de
// Modifications Copyright (C) 2025-2026 Shaun Agostinho
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

package gofaxlib

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
)

var (
	// Config is the global configuration struct
	Config config
)

type config struct {
	FreeSwitch struct {
		EventClientSocket         string `json:"event_client_socket"`          // used for sending commands to freeswitch
		EventClientSocketPassword string `json:"event_client_socket_password"` // password for the event client socket
		EventServerSocket         string `json:"event_server_socket"`          // used for receiving events from freeswitch
		/*Gateway                   []string `json:"gateway"`                      // default gateways for sending faxes / upstream trunk*/
		Ident   string `json:"ident"`
		Header  string `json:"header"`
		Verbose bool   `json:"verbose"`
		// SoftmodemFallback is DEPRECATED and no longer used: softmodem
		// fallback / T.38 policy is managed in Postgres via fax policy rules
		// (see Faxing.Policy). The field remains so old config files parse.
		SoftmodemFallback bool `json:"softmodem_fallback"`
		// GatewayConfigDir is the directory on a shared filesystem where
		// FreeSWITCH gateway XML files are written (e.g. /etc/freeswitch/gateways).
		// Empty disables API-driven gateway provisioning.
		GatewayConfigDir string `json:"gateway_config_dir"`
		// GatewayConfigChown optionally sets owner:group (names or uid:gid) on
		// rendered gateway XML files, e.g. "freeswitch:freeswitch". Empty keeps
		// the writer's ownership.
		GatewayConfigChown string `json:"gateway_config_chown"`
		// GatewayProfile is the sofia profile that hosts the gateways.
		GatewayProfile string `json:"gateway_profile"`
		// GatewayMonitorSeconds is the poll interval for sofia registration
		// state tracking of provisioned gateways and for re-resolving
		// hostname-valued gateway ACL entries (0 = default 60s, negative disables).
		GatewayMonitorSeconds int `json:"gateway_monitor_seconds"`
	} `json:"freeswitch"`
	Faxing struct {
		TempDir                      string          `json:"temp_dir"`     // shared with FreeSWITCH (store-and-forward files)
		TempMaxAge                   string          `json:"temp_max_age"` // janitor: delete orphaned temp files older than this (s/m/h/d; empty = 24h; "0s" = disabled)
		EnableT38                    bool            `json:"enable_t38"`
		RequestT38                   bool            `json:"request_t38"`
		RecipientFromDiversionHeader bool            `json:"recipient_from_diversion_header"`
		AnswerAfter                  uint64          `json:"answer_after"`
		WaitTime                     uint64          `json:"wait_time"`
		DisableV17AfterRetry         string          `json:"disable_v17_after_retry"`
		DisableECMAfterRetry         string          `json:"disable_ecm_after_retry"`
		FailedResponse               []string        `json:"failed_response"`
		FailedResponseMap            map[string]bool `json:"failed_response_map"`
		RetryDelay                   string          `json:"retry_delay"`
		RetryAttempts                string          `json:"retry_attempts"`
		// Policy configures the Postgres-backed fax policy engine (T.38 /
		// ECM / V.17 rules and adaptive learning). See FaxPolicyConfig.
		Policy FaxPolicyConfig `json:"policy"`
	} `json:"faxing"`
	Database struct { // this is a postgresql database
		Host     string `json:"host"`
		Port     string `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Database string `json:"database"`
	} `json:"database"`
	Loki struct {
		PushURL  string `json:"push_url"`
		Username string `json:"username"`
		Password string `json:"password"`
		Job      string `json:"job"`
		Enabled  bool   `json:"enabled"`
	} `json:"loki"`
	Web struct {
		Listen string `json:"listen"`
		APIKey string `json:"api_key"`
	} `json:"web"`
	// Portal configures inbound fax delivery to a gofaxportal instance.
	// Numbers with an endpoint of type "portal" have their received faxes
	// POSTed (same payload as webhook delivery) to
	// "<url>/portal/api/inbound/<endpoint>" where <endpoint> is the portal
	// organization's service-account username. Empty URL disables portal
	// delivery. APIKey is an optional pre-shared key sent as X-API-Key; it
	// must match the portal's inbound_api_key when set.
	Portal struct {
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	} `json:"portal"`
	SMTP struct {
		Host        string `json:"host,omitempty"`
		Port        int    `json:"port,omitempty"`
		Username    string `json:"username,omitempty"`
		Password    string `json:"password,omitempty"`
		Encryption  string `json:"encryption,omitempty"`
		FromAddress string `json:"from_address,omitempty"`
		FromName    string `json:"from_name,omitempty"`
	} `json:"smtp"`
	PSK string `json:"psk"`
	// Dialplan is optional. When the section is absent, built-in default
	// transformation rules are used. When present (even with an empty rules
	// list), the configured rules fully replace the defaults.
	Dialplan *DialplanConfig `json:"dialplan,omitempty"`
}

// FaxPolicyConfig tunes the Postgres-backed fax policy engine which replaces
// the old FreeSWITCH mod_db softmodem fallback. Pointer booleans default to
// true when absent so the engine is on unless explicitly disabled.
type FaxPolicyConfig struct {
	// Enabled controls rule evaluation at call time (manual + auto rules).
	Enabled *bool `json:"enabled"`
	// LearnEnabled controls automatic rule creation from call outcomes.
	LearnEnabled *bool `json:"learn_enabled"`
	// T38FailureThreshold is the number of qualifying failures before an
	// auto t38_off rule is created for the remote number (default 1).
	T38FailureThreshold int `json:"t38_failure_threshold"`
	// ECMFailureThreshold is the number of qualifying failures before an
	// auto ecm_off rule is added (default 2).
	ECMFailureThreshold int `json:"ecm_failure_threshold"`
	// V17FailureThreshold is the number of qualifying failures before an
	// auto v17_off rule is added (default 3).
	V17FailureThreshold int `json:"v17_failure_threshold"`
	// RecoverySuccesses is the number of consecutive successes after which
	// auto-learned rules for a number are cleared (re-probing; default 3).
	RecoverySuccesses int `json:"recovery_successes"`
	// AutoRuleTTL is how long an auto-learned rule lives before expiring
	// (Go duration string, default "720h" = 30 days).
	AutoRuleTTL string `json:"auto_rule_ttl"`
	// PairStateTTL is how long the per-pair flip-flop probing state is
	// remembered (Go duration string, default "15m").
	PairStateTTL string `json:"pair_state_ttl"`
	// RetryChainEscalation disables T.38 for subsequent attempts of the same
	// job when the previous attempt showed T.38 negotiation trouble.
	RetryChainEscalation *bool `json:"retry_chain_escalation"`
	// BridgeLearn enables creating bridge-scoped auto t38_off rules when a
	// transcoded (bridged) call fails with a SIP negotiation error while
	// T.38 was enabled.
	BridgeLearn *bool `json:"bridge_learn"`
}

func boolOrDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// PolicyEnabled reports whether rule evaluation is active (default true).
func (c FaxPolicyConfig) PolicyEnabled() bool { return boolOrDefault(c.Enabled, true) }

// LearningEnabled reports whether auto-learning is active (default true).
func (c FaxPolicyConfig) LearningEnabled() bool { return boolOrDefault(c.LearnEnabled, true) }

// RetryChainEnabled reports whether within-job retry escalation is active (default true).
func (c FaxPolicyConfig) RetryChainEnabled() bool { return boolOrDefault(c.RetryChainEscalation, true) }

// BridgeLearnEnabled reports whether bridge auto-learning is active (default true).
func (c FaxPolicyConfig) BridgeLearnEnabled() bool { return boolOrDefault(c.BridgeLearn, true) }

// T38Threshold returns the configured t38 failure threshold (default 1).
func (c FaxPolicyConfig) T38Threshold() int {
	if c.T38FailureThreshold > 0 {
		return c.T38FailureThreshold
	}
	return 1
}

// ECMThreshold returns the configured ecm failure threshold (default 2).
func (c FaxPolicyConfig) ECMThreshold() int {
	if c.ECMFailureThreshold > 0 {
		return c.ECMFailureThreshold
	}
	return 2
}

// V17Threshold returns the configured v17 failure threshold (default 3).
func (c FaxPolicyConfig) V17Threshold() int {
	if c.V17FailureThreshold > 0 {
		return c.V17FailureThreshold
	}
	return 3
}

// RecoveryThreshold returns the successes needed to clear auto rules (default 3).
func (c FaxPolicyConfig) RecoveryThreshold() int {
	if c.RecoverySuccesses > 0 {
		return c.RecoverySuccesses
	}
	return 3
}

// DialplanRule is a single regex-based number transformation.
type DialplanRule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// DialplanConfig holds the ordered transformation rules applied to
// caller/callee numbers before tenant lookup and routing.
type DialplanConfig struct {
	// Source selects where rules come from: "config" (default — this
	// section's rules, or built-in defaults when the section is absent) or
	// "db" (the dialplan_rules table, hot-reloadable via /admin/reload).
	Source string         `json:"source"`
	Rules  []DialplanRule `json:"rules"`
}

// LoadConfig loads the configuration from a JSON file.
func LoadConfig(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Config: unable to open file: %v", err)
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("Config: unable to read file: %v", err)
	}

	err = json.Unmarshal(data, &Config)
	if err != nil {
		log.Fatalf("Config: unable to parse JSON: %v", err)
	}

	// Rebuild FailedResponseMap from FailedResponse list
	Config.Faxing.FailedResponseMap = make(map[string]bool)
	for _, val := range Config.Faxing.FailedResponse {
		Config.Faxing.FailedResponseMap[val] = true
	}
}

func FailedHangUpCause(hangUpCause string) bool {
	if Config.Faxing.FailedResponseMap[hangUpCause] {
		return true
	} else {
		return false
	}
}

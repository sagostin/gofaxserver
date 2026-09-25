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

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the portal configuration. It is independent from the gofaxserver
// config; the only coupling is the gofaxserver base URL + admin API key.
type Config struct {
	Listen       string   `json:"listen"`
	CookieSecure bool     `json:"cookie_secure"` // set true when served over HTTPS
	Database     Database `json:"database"`

	// SessionSecret is used to derive HMACs (future TOTP etc.). Treat as a secret.
	SessionSecret string `json:"session_secret"`
	// EncryptionKey encrypts per-org gofaxserver service-account passwords at
	// rest (AES-256-GCM, key derived via SHA-256). Treat as a secret.
	EncryptionKey string `json:"encryption_key"`

	GofaxServer GofaxServer `json:"gofaxserver"`

	// InboundAPIKey is the optional pre-shared key gofaxserver must present
	// (as X-API-Key) when delivering received faxes to
	// POST /portal/api/inbound/{svc_username}. It must match gofaxserver's
	// portal.api_key. Empty disables the check (loopback-only deployments).
	InboundAPIKey string `json:"inbound_api_key"`

	// TrustedProxies are the direct-peer IPs/CIDRs whose X-Forwarded-For /
	// X-Real-IP headers are honored when resolving the client IP (audit log,
	// rate limiters). Defaults to loopback — the Caddy reverse proxy on the
	// same host. Requests from any other peer have those headers ignored,
	// so they cannot spoof their IP.
	TrustedProxies []string `json:"trusted_proxies"`

	PollIntervalSeconds int            `json:"poll_interval_seconds"`
	UploadMaxMB         int64          `json:"upload_max_mb"`
	LoginRatePerMinute  int            `json:"login_rate_per_minute"`
	SendRatePerHour     int            `json:"send_rate_per_hour"`
	BootstrapAdmin      BootstrapAdmin `json:"bootstrap_admin"`
}

type Database struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	SSLMode  string `json:"sslmode"`
	TimeZone string `json:"timezone"`
}

type GofaxServer struct {
	BaseURL     string `json:"base_url"`
	AdminAPIKey string `json:"admin_api_key"`
}

type BootstrapAdmin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

func defaults() *Config {
	return &Config{
		Listen:              ":8081",
		Database:            Database{Host: "localhost", Port: "5432", User: "gofaxportal", Database: "gofaxportal", SSLMode: "disable", TimeZone: "America/Vancouver"},
		PollIntervalSeconds: 5,
		UploadMaxMB:         20,
		LoginRatePerMinute:  5,
		SendRatePerHour:     120,
		TrustedProxies:      []string{"127.0.0.1", "::1"},
		BootstrapAdmin:      BootstrapAdmin{Username: "admin"},
	}
}

// Load reads the JSON config at path and applies environment overrides.
func Load(path string) (*Config, error) {
	cfg := defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config: %w", err)
			}
			// Missing file is allowed; env vars may fully configure the portal.
		} else if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	env := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	if v := env("PORTAL_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := env("PORTAL_COOKIE_SECURE"); v == "true" || v == "1" {
		cfg.CookieSecure = true
	}
	db := &cfg.Database
	if v := env("PORTAL_DB_HOST"); v != "" {
		db.Host = v
	}
	if v := env("PORTAL_DB_PORT"); v != "" {
		db.Port = v
	}
	if v := env("PORTAL_DB_USER"); v != "" {
		db.User = v
	}
	if v := env("PORTAL_DB_PASSWORD"); v != "" {
		db.Password = v
	}
	if v := env("PORTAL_DB_NAME"); v != "" {
		db.Database = v
	}
	if v := os.Getenv("PORTAL_DB_SSLMODE"); v != "" {
		db.SSLMode = v
	}
	if v := env("PORTAL_SESSION_SECRET"); v != "" {
		cfg.SessionSecret = v
	}
	if v := env("PORTAL_ENCRYPTION_KEY"); v != "" {
		cfg.EncryptionKey = v
	}
	if v := env("PORTAL_GOFAX_BASE_URL"); v != "" {
		cfg.GofaxServer.BaseURL = v
	}
	if v := env("PORTAL_ADMIN_API_KEY"); v != "" {
		cfg.GofaxServer.AdminAPIKey = v
	}
	if v := env("PORTAL_INBOUND_API_KEY"); v != "" {
		cfg.InboundAPIKey = v
	}
	if v := env("PORTAL_TRUSTED_PROXIES"); v != "" {
		cfg.TrustedProxies = strings.Split(v, ",")
		for i := range cfg.TrustedProxies {
			cfg.TrustedProxies[i] = strings.TrimSpace(cfg.TrustedProxies[i])
		}
	}
	if v := env("PORTAL_BOOTSTRAP_USERNAME"); v != "" {
		cfg.BootstrapAdmin.Username = v
	}
	if v := env("PORTAL_BOOTSTRAP_PASSWORD"); v != "" {
		cfg.BootstrapAdmin.Password = v
	}
	if v := env("PORTAL_BOOTSTRAP_EMAIL"); v != "" {
		cfg.BootstrapAdmin.Email = v
	}
	if cfg.GofaxServer.BaseURL == "" {
		cfg.GofaxServer.BaseURL = "http://127.0.0.1:8080"
	}
	if cfg.UploadMaxMB <= 0 {
		cfg.UploadMaxMB = 20
	}
	if cfg.PollIntervalSeconds <= 0 {
		cfg.PollIntervalSeconds = 5
	}
	if cfg.LoginRatePerMinute <= 0 {
		cfg.LoginRatePerMinute = 5
	}
	if cfg.SendRatePerHour <= 0 {
		cfg.SendRatePerHour = 120
	}
	if db.Port == "" {
		db.Port = "5432"
	}
	return cfg, nil
}

// Validate ensures required secrets are present.
func (c *Config) Validate() error {
	var missing []string
	if c.SessionSecret == "" {
		missing = append(missing, "session_secret")
	}
	if c.EncryptionKey == "" {
		missing = append(missing, "encryption_key")
	}
	if c.GofaxServer.AdminAPIKey == "" {
		missing = append(missing, "gofaxserver.admin_api_key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config values: %s", strings.Join(missing, ", "))
	}
	return nil
}

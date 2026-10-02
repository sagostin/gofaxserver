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
	"path/filepath"
	"strconv"
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
	PrepareRatePerHour  int            `json:"prepare_rate_per_hour"`
	BootstrapAdmin      BootstrapAdmin `json:"bootstrap_admin"`

	// Converter drives the portal-side document conversion pipeline
	// (docx/doc/png/jpeg → PDF, cover pages, fax previews). gofaxserver is
	// untouched — it still receives a plain PDF.
	Converter Converter `json:"converter"`
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

// Converter is the portal-side conversion pipeline config.
type Converter struct {
	Enabled        bool   `json:"enabled"`
	GotenbergURL   string `json:"gotenberg_url"`   // docx/doc → PDF (LibreOffice sidecar)
	GhostscriptBin string `json:"ghostscript_bin"` // fax-accurate B&W previews
	ImageMagickBin string `json:"imagemagick_bin"` // tiff → PDF
	TempDir        string `json:"temp_dir"`        // prepared docs live here until TTL
	PrepareTTLMin  int    `json:"prepare_ttl_minutes"`
	MaxPages       int    `json:"max_pages"`
	DefaultFitMode string `json:"default_fit_mode"` // constrain | fill | stretch
}

func defaults() *Config {
	return &Config{
		Listen:              ":8081",
		Database:            Database{Host: "localhost", Port: "5432", User: "gofaxportal", Database: "gofaxportal", SSLMode: "disable", TimeZone: "America/Vancouver"},
		PollIntervalSeconds: 5,
		UploadMaxMB:         20,
		LoginRatePerMinute:  5,
		SendRatePerHour:     120,
		PrepareRatePerHour:  240,
		TrustedProxies:      []string{"127.0.0.1", "::1"},
		BootstrapAdmin:      BootstrapAdmin{Username: "admin"},
		Converter: Converter{
			Enabled:        true,
			GotenbergURL:   "http://127.0.0.1:3200",
			GhostscriptBin: "gs",
			ImageMagickBin: "magick",
			PrepareTTLMin:  60,
			MaxPages:       50,
			DefaultFitMode: "constrain",
		},
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
	cv := &cfg.Converter
	if v := env("PORTAL_CONVERTER_ENABLED"); v != "" {
		cv.Enabled = v == "true" || v == "1"
	}
	if v := env("PORTAL_GOTENBERG_URL"); v != "" {
		cv.GotenbergURL = v
	}
	if v := env("PORTAL_GS_BIN"); v != "" {
		cv.GhostscriptBin = v
	}
	if v := env("PORTAL_MAGICK_BIN"); v != "" {
		cv.ImageMagickBin = v
	}
	if v := env("PORTAL_CONVERTER_TEMP_DIR"); v != "" {
		cv.TempDir = v
	}
	if v := env("PORTAL_CONVERTER_TTL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cv.PrepareTTLMin = n
		}
	}
	if v := env("PORTAL_CONVERTER_MAX_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cv.MaxPages = n
		}
	}
	if v := env("PORTAL_CONVERTER_DEFAULT_FIT"); v != "" {
		cv.DefaultFitMode = strings.ToLower(v)
	}
	if v := env("PORTAL_PREPARE_RATE_PER_HOUR"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PrepareRatePerHour = n
		}
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
	if cfg.PrepareRatePerHour <= 0 {
		cfg.PrepareRatePerHour = 240
	}
	cv = &cfg.Converter
	if cv.GotenbergURL == "" {
		cv.GotenbergURL = "http://127.0.0.1:3200"
	}
	if cv.GhostscriptBin == "" {
		cv.GhostscriptBin = "gs"
	}
	if cv.ImageMagickBin == "" {
		cv.ImageMagickBin = "magick"
	}
	if cv.TempDir == "" {
		cv.TempDir = filepath.Join(os.TempDir(), "gofaxportal-convert")
	}
	if cv.PrepareTTLMin <= 0 {
		cv.PrepareTTLMin = 60
	}
	if cv.MaxPages <= 0 {
		cv.MaxPages = 50
	}
	switch cv.DefaultFitMode {
	case "constrain", "fill", "stretch":
	default:
		cv.DefaultFitMode = "constrain"
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

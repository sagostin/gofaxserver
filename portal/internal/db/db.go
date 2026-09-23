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

package db

import (
	"fmt"
	"log"
	"os"

	"gofaxportal/internal/config"
	"gofaxportal/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Connect opens the portal database and migrates the portal schema. The
// portal database is separate from the gofaxserver database by design.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	sslMode := cfg.Database.SSLMode
	if v := os.Getenv("PORTAL_DB_SSLMODE"); v != "" {
		sslMode = v
	}
	timeZone := cfg.Database.TimeZone
	if timeZone == "" {
		timeZone = "America/Vancouver"
	}
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		cfg.Database.Host, cfg.Database.Port, cfg.Database.User,
		cfg.Database.Password, cfg.Database.Database, sslMode, timeZone,
	)
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("connect portal db: %w", err)
	}
	if err := gdb.AutoMigrate(models.AllModels()...); err != nil {
		return nil, fmt.Errorf("migrate portal schema: %w", err)
	}
	log.Printf("[portal] database connected and schema migrated (%s/%s)", cfg.Database.Host, cfg.Database.Database)
	return gdb, nil
}

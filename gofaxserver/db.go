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
	"gofaxserver/gofaxlib"
	"os"
)

func (s *Server) createIndexes() error {
	// Create index on expires_at column
	/*err := s.dB.Migrator().CreateIndex(&MediaFile{}, "ExpiresAt")
	if err != nil {
		return fmt.Errorf("failed to create index on expires_at: %v", err)
	}*/
	return nil
}

func (s *Server) migrateSchema() error {
	if err := s.DB.AutoMigrate(&Tenant{}, &TenantNumber{}, &Endpoint{}, &FaxJobResult{}, &TenantUser{}, &GatewayTemplate{}, &GatewayConfig{}, &DialplanRule{}, &FaxPolicyRule{}, &FaxPairState{}); err != nil {
		return err
	}
	err := s.createIndexes()
	if err != nil {
		return err
	}
	return nil
}

func getPostgresDSN() string {
	host := gofaxlib.Config.Database.Host
	if host == "" {
		host = "localhost"
	}

	port := gofaxlib.Config.Database.Port
	if port == "" {
		port = "5432"
	}

	user := gofaxlib.Config.Database.User
	password := gofaxlib.Config.Database.Password
	dbName := gofaxlib.Config.Database.Database
	sslMode := os.Getenv("POSTGRES_SSLMODE") // todo
	if sslMode == "" {
		sslMode = "disable"
	}

	timeZone := os.Getenv("POSTGRES_TIMEZONE") // todo
	if timeZone == "" {
		timeZone = "America/Vancouver"
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		host, port, user, password, dbName, sslMode, timeZone,
	)

	return dsn
}

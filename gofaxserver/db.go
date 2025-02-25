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
	if err := s.DB.AutoMigrate(&Tenant{}, &TenantNumber{}, &Endpoint{}, &FaxJobResultRecord{}); err != nil {
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

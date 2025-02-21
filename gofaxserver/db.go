package gofaxserver

func (s *Server) createIndexes() error {
	// Create index on expires_at column
	/*err := s.dB.Migrator().CreateIndex(&MediaFile{}, "ExpiresAt")
	if err != nil {
		return fmt.Errorf("failed to create index on expires_at: %v", err)
	}*/
	return nil
}

func (s *Server) migrateSchema() error {
	if err := s.dB.AutoMigrate(&Tenant{}, &TenantNumber{}); err != nil {
		return err
	}
	err := s.createIndexes()
	if err != nil {
		return err
	}
	return nil
}

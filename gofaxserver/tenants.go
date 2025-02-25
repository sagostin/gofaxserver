package gofaxserver

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
)

type Tenant struct {
	ID      uint           `gorm:"primaryKey" json:"id"`
	Name    string         `json:"name"`
	Email   string         `json:"email"` // main contact email?
	Numbers []TenantNumber `gorm:"foreignKey:TenantID" json:"numbers"`
}

type TenantNumber struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	TenantID     uint   `gorm:"index;not null" json:"tenant_id"`
	Number       string `gorm:"unique;not null" json:"number"` // 10 digit or what ever format matches the transformation rules
	NotifyEmails string `json:"notify_emails"`                 // email addresses to notify when a fax is received / failed, etc, if all other endpoint deliveries fail
	CID          string `json:"cid"`                           // caller id that is displayed on the fax? eg. +1 555-555-5555
	Header       string `json:"webhook"`                       // this is the name displayed at the top of the fax eg. "Company Faxing Relay"
}

// loadTenants loads Tenants (with their associated numbers) from the database.
func (s *Server) loadTenants() error {
	var tenants []Tenant
	// Preload Numbers for each tenant.
	if err := s.DB.Preload("Numbers").Find(&tenants).Error; err != nil {
		return err
	}

	tenantMap := make(map[uint]*Tenant)
	for _, tenant := range tenants {
		t := tenant // create a copy to avoid referencing the loop variable
		tenantMap[t.ID] = &t
	}

	s.mu.Lock()
	s.Tenants = tenantMap
	s.mu.Unlock()
	return nil
}

// loadTenantNumbers loads tenant numbers from the database.
func (s *Server) loadTenantNumbers() error {
	var numbers []TenantNumber
	if err := s.DB.Find(&numbers).Error; err != nil {
		return err
	}

	numberMap := make(map[string]*TenantNumber)
	for _, number := range numbers {
		n := number // create a copy
		numberMap[n.Number] = &n
	}

	s.mu.Lock()
	s.TenantNumbers = numberMap
	s.mu.Unlock()
	return nil
}

// reloadTenantsAndNumbers reloads Tenants and tenant numbers.
func (s *Server) reloadTenantsAndNumbers() error {
	if err := s.loadTenants(); err != nil {
		return err
	}
	if err := s.loadTenantNumbers(); err != nil {
		return err
	}
	return nil
}

// addTenantNumber adds a new number for a tenant.
func (s *Server) addTenantNumber(tenantID uint, number *TenantNumber) error {
	s.mu.RLock()
	_, tenantExists := s.Tenants[tenantID]
	s.mu.RUnlock()
	if !tenantExists {
		return fmt.Errorf("tenant with id %d does not exist", tenantID)
	}

	s.mu.RLock()
	_, exists := s.TenantNumbers[number.Number]
	s.mu.RUnlock()
	if exists {
		return fmt.Errorf("number %s already exists", number.Number)
	}

	number.TenantID = tenantID
	if err := s.DB.Create(number).Error; err != nil {
		return fmt.Errorf("failed to add number to database: %w", err)
	}

	s.mu.Lock()
	s.TenantNumbers[number.Number] = number
	s.mu.Unlock()

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"TenantNumber.Add",
		fmt.Sprintf("Added number %s to tenant %d", number.Number, tenantID),
		logrus.InfoLevel,
		map[string]interface{}{
			"tenant_id": tenantID,
			"number":    number.Number,
		},
	))
	return nil
}

// removeTenantNumber removes a tenant number based on tenant id and number string.
func (s *Server) removeTenantNumber(tenantID uint, numberStr string) error {
	s.mu.RLock()
	number, exists := s.TenantNumbers[numberStr]
	s.mu.RUnlock()
	if !exists || number.TenantID != tenantID {
		return fmt.Errorf("number %s not found for tenant id %d", numberStr, tenantID)
	}

	if err := s.DB.Delete(&TenantNumber{}, number.ID).Error; err != nil {
		return fmt.Errorf("failed to remove number from database: %w", err)
	}

	s.mu.Lock()
	delete(s.TenantNumbers, numberStr)
	s.mu.Unlock()

	s.LogManager.SendLog(s.LogManager.BuildLog(
		"TenantNumber.Remove",
		fmt.Sprintf("Removed number %s from tenant %d", numberStr, tenantID),
		logrus.InfoLevel,
		map[string]interface{}{
			"tenant_id": tenantID,
			"number":    numberStr,
		},
	))
	return nil
}

// getTenantByNumber returns the Tenant associated with a given phone number.
func (s *Server) getTenantByNumber(number string) (*Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tn, exists := s.TenantNumbers[number]
	if !exists {
		return nil, fmt.Errorf("number %s not found", number)
	}

	tenant, exists := s.Tenants[tn.TenantID]
	if !exists {
		return nil, fmt.Errorf("tenant with id %d not found", tn.TenantID)
	}
	return tenant, nil
}

// getEndpointsForNumber returns endpoints for a given phone number. If number-specific endpoints exist, they take priority.
func (s *Server) getEndpointsForNumber(number string) ([]*Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// First check for endpoints tied to the specific number.
	if eps, exists := s.NumberEndpoints[number]; exists && len(eps) > 0 {
		return eps, nil
	}

	// DEBUG
	marshal, _ := json.Marshal(s.TenantNumbers)
	fmt.Printf("DEBUG 333: %s", marshal)

	// If no number-specific endpoints, get the tenant endpoints.

	tn, exists := s.TenantNumbers[number]
	if !exists {
		return nil, fmt.Errorf("number %s not found", number)
	}
	if eps, exists := s.TenantEndpoints[tn.TenantID]; exists {
		return eps, nil
	}
	return nil, fmt.Errorf("no endpoints found for number %s", number)
}

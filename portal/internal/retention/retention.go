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

// Package retention periodically deletes inbound faxes (including their
// sealed PDFs) that are older than each organization's configured retention
// period. Orgs with RetentionDays == 0 keep their faxes indefinitely.
// Outbound fax_jobs metadata and upstream fax_job_results are never touched.
package retention

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gofaxportal/internal/models"

	"gorm.io/gorm"
)

const (
	// SweepInterval is how often the sweeper runs. Hourly is plenty: the
	// retention granularity is days.
	SweepInterval = time.Hour
	// batchSize caps rows deleted per statement so a large backlog doesn't
	// hold a long table lock.
	batchSize = 500
)

type Sweeper struct {
	DB *gorm.DB
	// now is injectable for tests.
	now func() time.Time
}

func New(db *gorm.DB) *Sweeper {
	return &Sweeper{DB: db, now: time.Now}
}

func (s *Sweeper) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	log.Printf("[retention] sweeping every %s", SweepInterval)
	s.Sweep() // immediate pass at startup
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.Sweep()
		}
	}
}

// Sweep deletes expired inbound faxes for every org with retention enabled.
func (s *Sweeper) Sweep() {
	orgs := []models.Org{}
	if err := s.DB.Where("retention_days > 0").Find(&orgs).Error; err != nil {
		log.Printf("[retention] list orgs: %v", err)
		return
	}
	for i := range orgs {
		s.sweepOrg(&orgs[i])
	}
}

func (s *Sweeper) sweepOrg(org *models.Org) {
	cutoff := retentionCutoff(org.RetentionDays, s.now())
	deleted := int64(0)
	for {
		res := s.DB.Where(
			"id IN (SELECT id FROM inbound_faxes WHERE org_id = ? AND received_at < ? LIMIT ?)",
			org.ID, cutoff, batchSize,
		).Delete(&models.InboundFax{})
		if res.Error != nil {
			log.Printf("[retention] org %d: delete batch: %v", org.ID, res.Error)
			return
		}
		deleted += res.RowsAffected
		if res.RowsAffected < batchSize {
			break
		}
	}
	if deleted == 0 {
		return
	}
	log.Printf("[retention] org %d (%s): deleted %d inbound fax(es) older than %dd", org.ID, org.Name, deleted, org.RetentionDays)
	s.audit(org, deleted, cutoff)
}

// retentionCutoff returns the received_at threshold: faxes received before
// it are expired. Pure function so the boundary is unit-testable.
func retentionCutoff(days int, now time.Time) time.Time {
	return now.Add(-time.Duration(days) * 24 * time.Hour)
}

// audit writes an actor-less audit row for an automated sweep. The API
// server's audit helper needs a request context, so the sweeper inserts
// directly with a synthetic actor name.
func (s *Sweeper) audit(org *models.Org, deleted int64, cutoff time.Time) {
	detail, err := json.Marshal(map[string]any{
		"deleted_inbound": deleted,
		"retention_days":  org.RetentionDays,
		"cutoff":          cutoff.UTC(),
	})
	if err != nil {
		return
	}
	entry := models.AuditLog{
		ActorID:       nil,
		ActorUsername: "retention",
		Action:        "RETENTION_SWEEP",
		Target:        fmt.Sprintf("org:%d", org.ID),
		Detail:        string(detail),
		CreatedAt:     s.now().UTC(),
	}
	if err := s.DB.Create(&entry).Error; err != nil {
		log.Printf("[retention] audit org %d: %v", org.ID, err)
	}
}

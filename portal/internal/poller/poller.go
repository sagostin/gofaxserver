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

package poller

import (
	"fmt"
	"log"
	"time"

	"gofaxportal/internal/config"
	"gofaxportal/internal/crypto"
	"gofaxportal/internal/fsclient"
	"gofaxportal/internal/models"

	"gorm.io/gorm"
)

const (
	failureGrace = 30 * time.Second // must be this old before we may declare failure
	batchSize    = 200
	// Jobs non-terminal after this long are declared failed: gofaxserver may
	// have restarted before writing any attempt rows, orphaning the job.
	staleJobTimeout = 24 * time.Hour
)

type Poller struct {
	Cfg *config.Config
	DB  *gorm.DB
	FX  *fsclient.Client
	Box *crypto.Box
}

func New(cfg *config.Config, db *gorm.DB, fx *fsclient.Client, box *crypto.Box) *Poller {
	return &Poller{Cfg: cfg, DB: db, FX: fx, Box: box}
}

func (p *Poller) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Duration(p.Cfg.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	log.Printf("[poller] running every %ds", p.Cfg.PollIntervalSeconds)
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.tick()
		}
	}
}

func (p *Poller) tick() {
	jobs := []models.FaxJob{}
	if err := p.DB.
		Where("status IN ? AND updated_at < ?", []string{models.JobQueued, models.JobSending}, time.Now().Add(-3*time.Second)).
		Order("submitted_at ASC").Limit(batchSize).Find(&jobs).Error; err != nil {
		log.Printf("[poller] query jobs: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	// One snapshot per tick of in-flight jobs from gofaxserver's tracker.
	activeSet := map[string]bool{}
	activeOK := false
	if snap, err := p.FX.ListActiveFaxes(); err != nil {
		log.Printf("[poller] list active faxes: %v", err)
	} else {
		activeOK = true
		for _, item := range snap.Items {
			activeSet[item.JobUUID] = true
		}
	}

	for i := range jobs {
		job := &jobs[i]
		if time.Since(job.SubmittedAt) > staleJobTimeout {
			now := time.Now().UTC()
			p.update(job, models.JobFailed, job.Attempts, job.Pages, "", "timed out without an upstream result", false, &now)
			continue
		}
		p.reconcile(job, activeSet, activeOK)
	}
}

func (p *Poller) reconcile(job *models.FaxJob, activeSet map[string]bool, activeOK bool) {
	username, pass, err := p.svcCreds(job.OrgID)
	if err != nil {
		log.Printf("[poller] svc creds for org %d: %v", job.OrgID, err)
		return
	}
	rows, err := p.FX.GetFaxStatus(username, pass, job.JobUUID)
	if err != nil {
		log.Printf("[poller] status %s: %v", job.JobUUID, err)
		return
	}

	inActive := activeSet[job.JobUUID]
	d := decide(rows, inActive, activeOK, job.SeenActive, job.SubmittedAt, time.Now())

	updates := map[string]any{
		"attempts": d.attempts,
	}
	if d.terminal {
		updates["status"] = d.status
		updates["pages"] = d.pages
		updates["result_text"] = d.resultText
		updates["last_error"] = d.lastErr
		updates["seen_active"] = true
		if d.completed != nil {
			updates["completed_at"] = d.completed.UTC()
		}
	} else {
		updates["status"] = d.status
		if d.sawActive {
			updates["seen_active"] = true
		}
	}
	if err := p.DB.Model(job).Updates(updates).Error; err != nil {
		log.Printf("[poller] update %s: %v", job.JobUUID, err)
		return
	}
	if d.terminal {
		log.Printf("[poller] job %s -> %s (%d pages, %d attempts)", job.JobUUID, d.status, d.pages, d.attempts)
	}
}

// decision is the outcome of the terminal-state heuristic.
type decision struct {
	status     string // target status (may equal current)
	attempts   int
	pages      int
	resultText string
	lastErr    string
	completed  *time.Time
	terminal   bool // job reached success/failed
	sawActive  bool // tracker observed the job this pass
}

// placeholderHangupCause marks the synthetic result gofaxserver attaches to a
// job at enqueue time. Rows carrying it are not real attempts and must be
// ignored when deciding the terminal state.
const placeholderHangupCause = "WEBHOOK"

// decide is a pure function so the heuristic is unit-testable.
//
// Rules:
//  1. Any successful attempt row ⇒ success (pages = max across attempts).
//  2. No success yet, attempt rows exist, and the job is no longer in the
//     active-faxes snapshot (or the snapshot was unavailable) after the
//     failure grace period ⇒ failed.
//  3. Otherwise still in flight: sending if seen in the tracker or previously
//     seen there, queued if we've heard nothing yet.
func decide(rows []fsclient.FaxStatusRow, inActive, activeOK, seenActive bool, submitted, now time.Time) decision {
	// Drop placeholder rows (enqueue markers), keeping only real attempt rows.
	real := make([]fsclient.FaxStatusRow, 0, len(rows))
	for _, r := range rows {
		if r.HangupCause == placeholderHangupCause {
			continue
		}
		real = append(real, r)
	}
	rows = real

	d := decision{status: models.JobQueued, attempts: len(rows)}
	bestPages := 0
	var successRow, lastRow *fsclient.FaxStatusRow
	for i := range rows {
		r := &rows[i]
		if r.TransferredPages > bestPages {
			bestPages = r.TransferredPages
		}
		if r.Success && successRow == nil {
			successRow = r
		}
		if lastRow == nil || !r.StartTs.Before(lastRow.StartTs) {
			lastRow = r
		}
	}
	d.pages = bestPages

	switch {
	case successRow != nil:
		end := successRow.EndTs
		if end.IsZero() {
			end = now
		}
		d.status, d.terminal = models.JobSuccess, true
		d.resultText = successRow.ResultText
		d.completed = &end
	case len(rows) > 0 &&
		(!activeOK || (!inActive && seenActive)) &&
		now.Sub(submitted) > failureGrace:
		errText := lastRow.ResultText
		if errText == "" {
			errText = lastRow.HangupCause
		}
		d.status, d.terminal = models.JobFailed, true
		d.resultText = lastRow.ResultText
		d.lastErr = errText
		c := now
		d.completed = &c
	case inActive:
		d.status, d.sawActive = models.JobSending, true
	case seenActive:
		d.status = models.JobSending
	}
	return d
}

func (p *Poller) update(job *models.FaxJob, status string, attempts, pages int, resultText, lastErr string, ok bool, completed *time.Time) {
	updates := map[string]any{
		"status":      status,
		"attempts":    attempts,
		"pages":       pages,
		"result_text": resultText,
		"last_error":  lastErr,
		"seen_active": true,
	}
	if completed != nil {
		updates["completed_at"] = completed.UTC()
	}
	if err := p.DB.Model(job).Updates(updates).Error; err != nil {
		log.Printf("[poller] finalize %s: %v", job.JobUUID, err)
		return
	}
	log.Printf("[poller] job %s -> %s (%d pages, %d attempts)", job.JobUUID, status, pages, attempts)
}

func (p *Poller) svcCreds(orgID uint) (string, string, error) {
	var org models.Org
	if err := p.DB.First(&org, orgID).Error; err != nil {
		return "", "", fmt.Errorf("org %d: %w", orgID, err)
	}
	pass, err := crypto.OpenString(p.Box, org.SvcPasswordEnc)
	return org.SvcUsername, pass, err
}

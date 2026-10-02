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

package convert

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// PreparedDoc is a converted, fax-ready document awaiting user confirmation.
// Its directory holds doc.pdf, page-N.png previews and meta.json.
type PreparedDoc struct {
	ID           string    `json:"id"`
	UserID       uint      `json:"user_id"`
	OrgID        uint      `json:"org_id"`
	Filename     string    `json:"filename"` // original upload name
	Pages        int       `json:"pages"`
	PreviewPages int       `json:"preview_pages"`
	FitMode      FitMode   `json:"fit_mode,omitempty"`
	Cover        bool      `json:"cover"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`

	dir string // absolute storage root (not serialized)
}

var validID = regexp.MustCompile(`^[a-f0-9-]{36}$`)

// ErrNotFound is returned by Load for missing/invalid/expired documents.
var ErrNotFound = errors.New("prepared document not found")

func osTempDir() string { return os.TempDir() }

func newPreparedDoc(root, filename string, userID, orgID uint, now time.Time, ttl time.Duration) *PreparedDoc {
	return &PreparedDoc{
		ID:        uuid.NewString(),
		UserID:    userID,
		OrgID:     orgID,
		Filename:  filepath.Base(filename),
		CreatedAt: now.UTC(),
		ExpiresAt: now.Add(ttl).UTC(),
		dir:       root,
	}
}

// Dir is the document's private directory (0700).
func (d *PreparedDoc) Dir() string { return filepath.Join(d.dir, d.ID) }

// PDFPath is the normalized fax-ready PDF inside Dir.
func (d *PreparedDoc) PDFPath() string { return filepath.Join(d.Dir(), "doc.pdf") }

// PreviewPath is the B&W preview PNG for a 1-based page number.
func (d *PreparedDoc) PreviewPath(page int) string {
	return filepath.Join(d.Dir(), fmt.Sprintf("page-%d.png", page))
}

func (d *PreparedDoc) initDir() error {
	if err := os.MkdirAll(d.Dir(), 0o700); err != nil {
		return fmt.Errorf("create prepared dir: %w", err)
	}
	// MkdirAll no-ops on existing dirs; enforce mode regardless.
	return os.Chmod(d.Dir(), 0o700)
}

func (d *PreparedDoc) saveMeta() error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(d.Dir(), "meta.json"), b)
}

// ReadPDF returns the normalized document bytes (for the send path).
func (d *PreparedDoc) ReadPDF() ([]byte, error) {
	return os.ReadFile(d.PDFPath())
}

// Load reads and validates a prepared document. Expired documents are deleted
// and reported as ErrNotFound.
func (c *Converter) Load(id string) (*PreparedDoc, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(c.cfg.TempDir, id, "meta.json"))
	if err != nil {
		return nil, ErrNotFound
	}
	var d PreparedDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, ErrNotFound
	}
	d.dir = c.cfg.TempDir
	if c.now().After(d.ExpiresAt) {
		_ = c.Delete(id)
		return nil, ErrNotFound
	}
	return &d, nil
}

// Delete removes a prepared document directory. Missing is not an error.
func (c *Converter) Delete(id string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	return os.RemoveAll(filepath.Join(c.cfg.TempDir, id))
}

// sweepInterval is how often expired prepared documents are reaped.
const sweepInterval = time.Minute

// Run sweeps expired documents until stop is closed (pattern follows the
// retention package). Intended to run as a goroutine from main.
func (c *Converter) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	c.Sweep()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			c.Sweep()
		}
	}
}

// Sweep deletes every expired prepared document under TempDir.
func (c *Converter) Sweep() {
	entries, err := os.ReadDir(c.cfg.TempDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[convert] sweep: read temp dir: %v", err)
		}
		return
	}
	now := c.now()
	for _, e := range entries {
		if !e.IsDir() || !validID.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.cfg.TempDir, e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var d PreparedDoc
		if err := json.Unmarshal(b, &d); err != nil {
			continue
		}
		if now.After(d.ExpiresAt) {
			if err := os.RemoveAll(filepath.Join(c.cfg.TempDir, e.Name())); err != nil {
				log.Printf("[convert] sweep: delete %s: %v", e.Name(), err)
			}
		}
	}
}

// writeFileAtomic writes data to path via a sibling temp file + rename so a
// crash never leaves a truncated file. Files are owner-only.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// replaceFile moves src over dst.
func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}

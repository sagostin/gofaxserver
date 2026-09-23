package gofaxserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gofaxserver/gofaxlib"
)

// Temp-file janitor.
//
// faxing.temp_dir holds sensitive job content (uploaded documents converted
// to TIFF, received faxes, notification PDFs). The per-job flows delete their
// files on completion, but failures, crashes, or restarts can orphan files.
// The janitor periodically deletes orphaned files older than a configurable
// max age. Only files created by gofaxserver (matched by prefix) are
// considered — the directory may be shared with FreeSWITCH.
//
// Encryption at rest is deliberately NOT applied to these temporary files:
// they must be readable by FreeSWITCH (a separate process, potentially on a
// shared filesystem mount), so any scheme would reduce to obfuscation.
// Protection comes from restrictive directory permissions, prompt deletion
// on job completion, and this janitor bounding the worst-case exposure.

// tempFilePrefixes are the filename prefixes gofaxserver itself creates in
// temp_dir (fax_<uuid>.tiff/.pdf, temp_<id>.*, notify_<uuid>.pdf,
// first_<uuid>.pdf). Anything else in the directory is left untouched.
var tempFilePrefixes = []string{"fax_", "temp_", "notify_", "first_"}

// tempJanitorInterval is how often the janitor sweeps.
const tempJanitorInterval = 10 * time.Minute

// defaultTempMaxAge bounds how long an orphaned temp file may linger when
// faxing.temp_max_age is not configured.
const defaultTempMaxAge = 24 * time.Hour

// tempMaxAge returns the configured max age for temp files. Empty or invalid
// values fall back to the default; "0s" disables the janitor.
func tempMaxAge() time.Duration {
	cfg := strings.TrimSpace(gofaxlib.Config.Faxing.TempMaxAge)
	if cfg == "" {
		return defaultTempMaxAge
	}
	d, err := ParseDuration(cfg)
	if err != nil {
		return defaultTempMaxAge
	}
	return d
}

// sweepTempDir removes gofaxserver-created files in dir older than maxAge.
// Pure function for testability; returns the number of files removed.
func sweepTempDir(dir string, maxAge time.Duration, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		owned := false
		for _, p := range tempFilePrefixes {
			if strings.HasPrefix(name, p) {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue // still young enough — possibly an in-flight job
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed, nil
}

// startTempJanitor ensures temp_dir exists and periodically removes orphaned
// temp files. Intended to be called as a goroutine from Server.Start.
func (s *Server) startTempJanitor() {
	dir := gofaxlib.Config.Faxing.TempDir
	if dir == "" {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"TempJanitor", "faxing.temp_dir is empty — janitor disabled",
			logrus.WarnLevel, nil,
		))
		return
	}
	maxAge := tempMaxAge()
	if maxAge <= 0 {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"TempJanitor", "temp file janitor disabled (temp_max_age <= 0)",
			logrus.InfoLevel, nil,
		))
		return
	}
	// Make sure the directory exists (fresh installs, first mount of a
	// shared volume) with restrictive permissions — it holds fax content.
	if err := os.MkdirAll(dir, 0750); err != nil {
		s.LogManager.SendLog(s.LogManager.BuildLog(
			"TempJanitor", fmt.Sprintf("cannot create temp_dir %s: %v — janitor disabled", dir, err),
			logrus.ErrorLevel, nil,
		))
		return
	}
	s.LogManager.SendLog(s.LogManager.BuildLog(
		"TempJanitor",
		fmt.Sprintf("temp file janitor running every %s (max age %s, dir %s)", tempJanitorInterval, maxAge, dir),
		logrus.InfoLevel, nil,
	))
	ticker := time.NewTicker(tempJanitorInterval)
	defer ticker.Stop()
	for range ticker.C {
		removed, err := sweepTempDir(dir, maxAge, time.Now())
		switch {
		case err != nil:
			s.LogManager.SendLog(s.LogManager.BuildLog(
				"TempJanitor", fmt.Sprintf("sweep of %s failed: %v", dir, err),
				logrus.ErrorLevel, nil,
			))
		case removed > 0:
			s.LogManager.SendLog(s.LogManager.BuildLog(
				"TempJanitor",
				fmt.Sprintf("removed %d orphaned temp file(s) older than %s from %s", removed, maxAge, dir),
				logrus.InfoLevel,
				map[string]interface{}{"removed": removed, "max_age": maxAge.String(), "dir": dir},
			))
		}
	}
}

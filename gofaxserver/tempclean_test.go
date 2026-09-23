package gofaxserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gofaxserver/gofaxlib"
)

func writeAgedFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0640); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}

func TestSweepTempDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	maxAge := time.Hour

	oldFax := writeAgedFile(t, dir, "fax_deadbeef.tiff", 2*time.Hour)
	oldNotify := writeAgedFile(t, dir, "notify_deadbeef.pdf", 2*time.Hour)
	oldUpload := writeAgedFile(t, dir, "temp_deadbeef.pdf", 2*time.Hour)
	oldFirst := writeAgedFile(t, dir, "first_deadbeef.pdf", 2*time.Hour)
	youngFax := writeAgedFile(t, dir, "fax_inflight.tiff", 5*time.Minute)
	foreignOld := writeAgedFile(t, dir, "freeswitch_stale.db", 72*time.Hour)

	// A subdirectory with a matching prefix must be left alone.
	if err := os.Mkdir(filepath.Join(dir, "fax_dir"), 0750); err != nil {
		t.Fatal(err)
	}

	removed, err := sweepTempDir(dir, maxAge, now)
	if err != nil {
		t.Fatalf("sweepTempDir: %v", err)
	}
	if removed != 4 {
		t.Fatalf("want 4 removed, got %d", removed)
	}
	for _, p := range []string{oldFax, oldNotify, oldUpload, oldFirst} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s deleted", p)
		}
	}
	for _, p := range []string{youngFax, foreignOld} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s kept: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fax_dir")); err != nil {
		t.Errorf("expected subdirectory kept: %v", err)
	}
}

func TestSweepTempDirMissingDir(t *testing.T) {
	if _, err := sweepTempDir(filepath.Join(t.TempDir(), "nope"), time.Hour, time.Now()); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestTempMaxAgeDefaultsAndParsing(t *testing.T) {
	old := gofaxlib.Config.Faxing.TempMaxAge
	t.Cleanup(func() { gofaxlib.Config.Faxing.TempMaxAge = old })
	set := func(v string) { gofaxlib.Config.Faxing.TempMaxAge = v }

	set("")
	if got := tempMaxAge(); got != defaultTempMaxAge {
		t.Errorf("empty: want %v, got %v", defaultTempMaxAge, got)
	}

	set("6h")
	if got := tempMaxAge(); got != 6*time.Hour {
		t.Errorf("6h: want 6h, got %v", got)
	}

	set("2d")
	if got := tempMaxAge(); got != 48*time.Hour {
		t.Errorf("2d: want 48h, got %v", got)
	}

	set("0s")
	if got := tempMaxAge(); got != 0 {
		t.Errorf("0s: want 0 (disabled), got %v", got)
	}

	set("bogus")
	if got := tempMaxAge(); got != defaultTempMaxAge {
		t.Errorf("invalid: want default %v, got %v", defaultTempMaxAge, got)
	}
}

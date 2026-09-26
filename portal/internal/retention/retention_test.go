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

package retention

import (
	"testing"
	"time"
)

func TestRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	got := retentionCutoff(30, now)
	want := now.Add(-30 * 24 * time.Hour)
	if !got.Equal(want) {
		t.Errorf("cutoff = %v, want %v", got, want)
	}
	// Boundary: a fax received exactly at the cutoff is NOT deleted (query
	// uses received_at < cutoff), so it survives one extra sweep.
	if !retentionCutoff(1, now).Equal(now.Add(-24 * time.Hour)) {
		t.Error("1-day cutoff should be exactly 24h back")
	}
}

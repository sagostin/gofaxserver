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

package gofaxlib

import "testing"

func TestImageResizingEnabledDefaultsTrue(t *testing.T) {
	old := Config.Faxing.EnableImageResizing
	defer func() { Config.Faxing.EnableImageResizing = old }()

	Config.Faxing.EnableImageResizing = nil
	if !ImageResizingEnabled() {
		t.Error("expected image resizing to default to true when unset")
	}
}

func TestImageResizingEnabledExplicit(t *testing.T) {
	old := Config.Faxing.EnableImageResizing
	defer func() { Config.Faxing.EnableImageResizing = old }()

	on := true
	Config.Faxing.EnableImageResizing = &on
	if !ImageResizingEnabled() {
		t.Error("expected true when explicitly enabled")
	}

	off := false
	Config.Faxing.EnableImageResizing = &off
	if ImageResizingEnabled() {
		t.Error("expected false when explicitly disabled")
	}
}

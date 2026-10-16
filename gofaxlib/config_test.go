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

func TestRTPJitterBufferEnabledDefaultsTrue(t *testing.T) {
	old := Config.Faxing.RTPJitterBuffer
	defer func() { Config.Faxing.RTPJitterBuffer = old }()

	Config.Faxing.RTPJitterBuffer = nil
	if !RTPJitterBufferEnabled() {
		t.Error("expected RTP jitter buffer to default to true when unset")
	}

	off := false
	Config.Faxing.RTPJitterBuffer = &off
	if RTPJitterBufferEnabled() {
		t.Error("expected false when explicitly disabled")
	}
}

func TestRTPJitterBufferSpec(t *testing.T) {
	old := Config.Faxing.RTPJitterBufferMsec
	defer func() { Config.Faxing.RTPJitterBufferMsec = old }()

	Config.Faxing.RTPJitterBufferMsec = ""
	if got := RTPJitterBufferSpec(); got != "60:120" {
		t.Errorf("default spec = %q, want 60:120", got)
	}

	Config.Faxing.RTPJitterBufferMsec = "  "
	if got := RTPJitterBufferSpec(); got != "60:120" {
		t.Errorf("blank spec = %q, want 60:120", got)
	}

	Config.Faxing.RTPJitterBufferMsec = "40:200"
	if got := RTPJitterBufferSpec(); got != "40:200" {
		t.Errorf("custom spec = %q, want 40:200", got)
	}
}

func TestRTPJitterBufferDuringBridgeDefaultsTrue(t *testing.T) {
	old := Config.Faxing.RTPJitterBufferDuringBridge
	defer func() { Config.Faxing.RTPJitterBufferDuringBridge = old }()

	Config.Faxing.RTPJitterBufferDuringBridge = nil
	if !RTPJitterBufferDuringBridgeEnabled() {
		t.Error("expected during-bridge jitter buffer to default to true when unset")
	}

	off := false
	Config.Faxing.RTPJitterBufferDuringBridge = &off
	if RTPJitterBufferDuringBridgeEnabled() {
		t.Error("expected false when explicitly disabled")
	}
}

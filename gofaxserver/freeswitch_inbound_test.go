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

package gofaxserver

import (
	"testing"

	"gofaxserver/gofaxlib"
)

func TestRTPMediaVarsDefaults(t *testing.T) {
	oldJB := gofaxlib.Config.Faxing.RTPJitterBuffer
	oldSpec := gofaxlib.Config.Faxing.RTPJitterBufferMsec
	oldBridge := gofaxlib.Config.Faxing.RTPJitterBufferDuringBridge
	defer func() {
		gofaxlib.Config.Faxing.RTPJitterBuffer = oldJB
		gofaxlib.Config.Faxing.RTPJitterBufferMsec = oldSpec
		gofaxlib.Config.Faxing.RTPJitterBufferDuringBridge = oldBridge
	}()

	gofaxlib.Config.Faxing.RTPJitterBuffer = nil
	gofaxlib.Config.Faxing.RTPJitterBufferMsec = ""
	gofaxlib.Config.Faxing.RTPJitterBufferDuringBridge = nil

	vars := rtpMediaVars(nil)
	if vars == nil {
		t.Fatal("expected media vars by default, got nil")
	}
	if vars["jitterbuffer_msec"] != "60:120" {
		t.Errorf("jitterbuffer_msec = %q, want 60:120", vars["jitterbuffer_msec"])
	}
	if vars["rtp_jitter_buffer_during_bridge"] != "true" {
		t.Errorf("rtp_jitter_buffer_during_bridge = %q, want true", vars["rtp_jitter_buffer_during_bridge"])
	}
}

func TestRTPMediaVarsGloballyDisabled(t *testing.T) {
	oldJB := gofaxlib.Config.Faxing.RTPJitterBuffer
	defer func() { gofaxlib.Config.Faxing.RTPJitterBuffer = oldJB }()

	off := false
	gofaxlib.Config.Faxing.RTPJitterBuffer = &off

	if vars := rtpMediaVars(nil); vars != nil {
		t.Errorf("expected nil vars when globally disabled, got %v", vars)
	}
	// Overrides must not resurrect a globally disabled feature.
	if vars := rtpMediaVars(map[string]string{"jitterbuffer_msec": "40:200"}); vars != nil {
		t.Errorf("expected nil vars when globally disabled (override ignored), got %v", vars)
	}
}

func TestRTPMediaVarsOverrideWins(t *testing.T) {
	oldJB := gofaxlib.Config.Faxing.RTPJitterBuffer
	defer func() { gofaxlib.Config.Faxing.RTPJitterBuffer = oldJB }()
	gofaxlib.Config.Faxing.RTPJitterBuffer = nil

	vars := rtpMediaVars(map[string]string{
		"jitterbuffer_msec":               "disabled",
		"rtp_jitter_buffer_during_bridge": "false",
		"unrelated_var":                   "ignored",
	})
	if vars["jitterbuffer_msec"] != "disabled" {
		t.Errorf("jitterbuffer_msec = %q, want disabled (per-route override)", vars["jitterbuffer_msec"])
	}
	if vars["rtp_jitter_buffer_during_bridge"] != "false" {
		t.Errorf("rtp_jitter_buffer_during_bridge = %q, want false (per-route override)", vars["rtp_jitter_buffer_during_bridge"])
	}
	if _, ok := vars["unrelated_var"]; ok {
		t.Error("unrelated override must not leak into media vars")
	}
}

func TestChanVarPairsSorted(t *testing.T) {
	pairs := chanVarPairs(map[string]string{"b": "2", "a": "1"})
	if len(pairs) != 2 || pairs[0] != "a=1" || pairs[1] != "b=2" {
		t.Errorf("pairs not sorted/deterministic: %v", pairs)
	}
}

func TestChanVarPrefix(t *testing.T) {
	if got := chanVarPrefix(nil); got != "" {
		t.Errorf("nil vars prefix = %q, want empty", got)
	}
	if got := chanVarPrefix(map[string]string{}); got != "" {
		t.Errorf("empty vars prefix = %q, want empty", got)
	}
	got := chanVarPrefix(map[string]string{"jitterbuffer_msec": "60:120", "rtp_jitter_buffer_during_bridge": "true"})
	want := "{jitterbuffer_msec=60:120,rtp_jitter_buffer_during_bridge=true}"
	if got != want {
		t.Errorf("prefix = %q, want %q", got, want)
	}
}

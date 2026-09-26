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

package api

import (
	"testing"

	"gofaxportal/internal/fsclient"
)

func TestSanitizeNumber(t *testing.T) {
	cases := map[string]string{
		" 555-123-4567 ":  "5551234567",
		"+1 (555) 1234":   "+15551234",
		"abc":             "",
		"+1.555.999.8888": "+15559998888",
	}
	for in, want := range cases {
		if got := sanitizeNumber(in); got != want {
			t.Errorf("sanitizeNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClampLimit(t *testing.T) {
	if clampLimit(0, 100) != 50 {
		t.Error("zero should default to 50")
	}
	if clampLimit(-5, 100) != 50 {
		t.Error("negative should default to 50")
	}
	if clampLimit(500, 200) != 200 {
		t.Error("should clamp to max")
	}
	if clampLimit(25, 200) != 25 {
		t.Error("in-range values pass through")
	}
}

func TestValidRetentionDays(t *testing.T) {
	for _, ok := range []int{0, 1, 30, 3650} {
		if !validRetentionDays(ok) {
			t.Errorf("%d should be valid", ok)
		}
	}
	for _, bad := range []int{-1, -30, 3651, 100000} {
		if validRetentionDays(bad) {
			t.Errorf("%d should be rejected", bad)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Acme Corp": "acme-corp",
		"  ACME  ":  "acme",
		"":          "org",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCustomNotify(t *testing.T) {
	valid := map[string]string{
		"":                         "",
		"  ":                       "",
		"email_full->ops@acme.tld": "email_full->ops@acme.tld",
		" email_full->ops@acme.tld , webhook->https://hooks/x ": "email_full->ops@acme.tld,webhook->https://hooks/x",
		"email->a@b.c,,email->a@b.c":                            "email->a@b.c",              // empties + duplicates dropped
		"email_report->a@b.c;c@d.e":                             "email_report->a@b.c;c@d.e", // multi-recipient intact
	}
	for in, want := range valid {
		got, err := normalizeCustomNotify(in)
		if err != nil {
			t.Errorf("normalizeCustomNotify(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeCustomNotify(%q) = %q, want %q", in, got, want)
		}
	}
	invalid := []string{
		"no-arrow",
		"->dest",
		"type->",
		"email_full->ops@acme.tld,bad-segment",
		"type->  ",
	}
	for _, in := range invalid {
		if _, err := normalizeCustomNotify(in); err == nil {
			t.Errorf("normalizeCustomNotify(%q) should have failed", in)
		}
	}
}

func TestMergeNotifySegments(t *testing.T) {
	derived := []string{"email_report->alice@acme.tld", "portal->svc_acme"}

	// Customs append after derived, in order.
	got := mergeNotifySegments(derived, "email_full->ops@acme.tld,webhook->https://hooks/x")
	want := "email_report->alice@acme.tld,portal->svc_acme,email_full->ops@acme.tld,webhook->https://hooks/x"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A custom segment duplicating a derived one is dropped.
	got = mergeNotifySegments(derived, "portal->svc_acme,email_full->ops@acme.tld")
	want = "email_report->alice@acme.tld,portal->svc_acme,email_full->ops@acme.tld"
	if got != want {
		t.Errorf("dedup: got %q, want %q", got, want)
	}

	// Invalid customs never break a resync: derived survives untouched.
	got = mergeNotifySegments(derived, "bad-segment")
	if got != "email_report->alice@acme.tld,portal->svc_acme" {
		t.Errorf("invalid custom must be skipped, got %q", got)
	}

	// Empty custom = derived only.
	if got := mergeNotifySegments(derived, ""); got != "email_report->alice@acme.tld,portal->svc_acme" {
		t.Errorf("empty custom: got %q", got)
	}
}

func TestValidateEndpointRules(t *testing.T) {
	s := &Server{}

	badType := &fsclient.Endpoint{Type: "weird", EndpointType: "gateway", Endpoint: "x"}
	if msg := s.validateEndpoint(badType, "direct"); msg == "" {
		t.Fatal("invalid type must be rejected")
	}

	badKind := &fsclient.Endpoint{Type: "global", EndpointType: "smoke", Endpoint: "x"}
	if msg := s.validateEndpoint(badKind, "direct"); msg == "" {
		t.Fatal("invalid endpoint_type must be rejected")
	}

	emptyVal := &fsclient.Endpoint{Type: "global", EndpointType: "gateway", Endpoint: ""}
	if msg := s.validateEndpoint(emptyVal, "direct"); msg == "" {
		t.Fatal("empty endpoint value must be rejected")
	}

	g := &fsclient.Endpoint{Type: "global", TypeID: 55, EndpointType: "gateway", Endpoint: "sbc:1.1.1.1"}
	if msg := s.validateEndpoint(g, "direct"); msg != "" || g.TypeID != 0 {
		t.Fatalf("global scope must force type_id=0, got msg=%q id=%d", msg, g.TypeID)
	}
}

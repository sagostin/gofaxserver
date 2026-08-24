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

func TestValidateEndpointRules(t *testing.T) {
	s := &Server{}

	badType := &fsclient.Endpoint{Type: "weird", EndpointType: "gateway", Endpoint: "x"}
	if msg := s.validateEndpoint(badType); msg == "" {
		t.Fatal("invalid type must be rejected")
	}

	badKind := &fsclient.Endpoint{Type: "global", EndpointType: "smoke", Endpoint: "x"}
	if msg := s.validateEndpoint(badKind); msg == "" {
		t.Fatal("invalid endpoint_type must be rejected")
	}

	emptyVal := &fsclient.Endpoint{Type: "global", EndpointType: "gateway", Endpoint: ""}
	if msg := s.validateEndpoint(emptyVal); msg == "" {
		t.Fatal("empty endpoint value must be rejected")
	}

	g := &fsclient.Endpoint{Type: "global", TypeID: 55, EndpointType: "gateway", Endpoint: "sbc:1.1.1.1"}
	if msg := s.validateEndpoint(g); msg != "" || g.TypeID != 0 {
		t.Fatalf("global scope must force type_id=0, got msg=%q id=%d", msg, g.TypeID)
	}
}

package gofaxserver

import (
	"testing"

	"gofaxserver/gofaxlib"
)

func withDialplanConfig(t *testing.T, cfg *gofaxlib.DialplanConfig) {
	t.Helper()
	old := gofaxlib.Config.Dialplan
	gofaxlib.Config.Dialplan = cfg
	t.Cleanup(func() { gofaxlib.Config.Dialplan = old })
}

func TestLoadDialplanDefaultsWhenUnconfigured(t *testing.T) {
	withDialplanConfig(t, nil)

	d := loadDialplan()
	if len(d.TransformationRules) != len(DefaultTransformationRules()) {
		t.Fatalf("want %d default rules, got %d", len(DefaultTransformationRules()), len(d.TransformationRules))
	}

	// Existing default behavior must be preserved.
	if got := d.ApplyTransformationRules("15551234567"); got != "5551234567" {
		t.Fatalf("11-digit NANP strip: got %q", got)
	}
	if got := d.ApplyTransformationRules("5551234567"); got != "5551234567" {
		t.Fatalf("10-digit passthrough: got %q", got)
	}
}

func TestLoadDialplanCustomRules(t *testing.T) {
	withDialplanConfig(t, &gofaxlib.DialplanConfig{
		Rules: []gofaxlib.DialplanRule{
			{Pattern: `^\+(.*)$`, Replacement: "$1"},
			{Pattern: `^00(.*)$`, Replacement: "$1"},
		},
	})

	d := loadDialplan()
	if got := d.ApplyTransformationRules("+4930123456789"); got != "4930123456789" {
		t.Fatalf("custom + strip: got %q", got)
	}
	if got := d.ApplyTransformationRules("004930123456789"); got != "4930123456789" {
		t.Fatalf("custom 00 strip: got %q", got)
	}
	// Defaults must NOT apply anymore: 13 digits stay untouched.
	if got := d.ApplyTransformationRules("4930123456789"); got != "4930123456789" {
		t.Fatalf("custom rules replace defaults: got %q", got)
	}
}

func TestLoadDialplanEmptyRulesDisableTransformation(t *testing.T) {
	withDialplanConfig(t, &gofaxlib.DialplanConfig{Rules: []gofaxlib.DialplanRule{}})

	d := loadDialplan()
	// Non-NANP numbers must pass through untouched.
	if got := d.ApplyTransformationRules("4930123456789"); got != "4930123456789" {
		t.Fatalf("empty rules should not transform, got %q", got)
	}
	if got := d.ApplyTransformationRules("15551234567"); got != "15551234567" {
		t.Fatalf("empty rules should not transform, got %q", got)
	}
}

func TestLoadDialplanInvalidPatternFallsBackToDefaults(t *testing.T) {
	withDialplanConfig(t, &gofaxlib.DialplanConfig{
		Rules: []gofaxlib.DialplanRule{
			{Pattern: `^([0-9`, Replacement: "$1"}, // invalid regex
		},
	})

	d := loadDialplan()
	if len(d.TransformationRules) != len(DefaultTransformationRules()) {
		t.Fatalf("invalid pattern should fall back to defaults, got %d rules", len(d.TransformationRules))
	}
	if got := d.ApplyTransformationRules("15551234567"); got != "5551234567" {
		t.Fatalf("fallback defaults not applied: got %q", got)
	}
}

func TestApplyTransformationRulesSequential(t *testing.T) {
	d := NewDialplanManager([]TransformationRule{
		mustRule(t, `^\+(.*)$`, "$1"),
		mustRule(t, `^1(\d{10})$`, "$1"),
	})
	if got := d.ApplyTransformationRules("+15551234567"); got != "5551234567" {
		t.Fatalf("rules should apply in sequence, got %q", got)
	}
}

func mustRule(t *testing.T, pattern, replacement string) TransformationRule {
	t.Helper()
	r, err := compileRule(pattern, replacement)
	if err != nil {
		t.Fatalf("compile %q: %v", pattern, err)
	}
	return r
}

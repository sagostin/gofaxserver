package gofaxserver

import (
	"fmt"
	"log"
	"regexp"

	"gofaxserver/gofaxlib"
)

// TransformationRule represents a single dialplan transformation.
// When the rule's Pattern matches the input, its Replacement is applied.
// The Replacement string can contain regex capture group references.
type TransformationRule struct {
	Pattern     *regexp.Regexp // Regular expression to match
	Replacement string         // Replacement string, e.g., "011$1" to prefix "011"
}

// DialplanRule is a database-backed transformation rule, used when
// dialplan.source = "db". Rules apply in ascending Position order;
// disabled rules are skipped.
type DialplanRule struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Position    int    `gorm:"index" json:"position"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// dialplanSource reports where transformation rules come from:
// "config" (default — the config.json dialplan section, or built-in
// defaults) or "db" (the dialplan_rules table, hot-reloadable).
func dialplanSource() string {
	if gofaxlib.Config.Dialplan != nil && gofaxlib.Config.Dialplan.Source == "db" {
		return "db"
	}
	return "config"
}

// compileDialplanRules compiles DB rows (already ordered, enabled-only) into
// TransformationRules. A row with an invalid pattern is skipped with a
// warning rather than breaking routing entirely.
func compileDialplanRules(rows []DialplanRule) []TransformationRule {
	rules := make([]TransformationRule, 0, len(rows))
	for _, r := range rows {
		rule, err := compileRule(r.Pattern, r.Replacement)
		if err != nil {
			log.Printf("Dialplan: skipping rule %d with invalid pattern %q: %v", r.ID, r.Pattern, err)
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

// seedDialplanRules populates the dialplan_rules table on first use: from the
// config.json rules when a dialplan section exists, otherwise from the
// built-in defaults. Returns the seed rows (or nil when not seeded).
func (s *Server) seedDialplanRules() ([]DialplanRule, error) {
	var count int64
	if err := s.DB.Model(&DialplanRule{}).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, nil
	}

	type pair struct{ pattern, replacement string }
	var seeds []pair
	if cfg := gofaxlib.Config.Dialplan; cfg != nil && len(cfg.Rules) > 0 {
		for _, r := range cfg.Rules {
			seeds = append(seeds, pair{r.Pattern, r.Replacement})
		}
	} else {
		for _, r := range DefaultTransformationRules() {
			seeds = append(seeds, pair{r.Pattern.String(), r.Replacement})
		}
	}

	rows := make([]DialplanRule, 0, len(seeds))
	for i, p := range seeds {
		if _, err := regexp.Compile(p.pattern); err != nil {
			log.Printf("Dialplan: skipping invalid seed pattern %q: %v", p.pattern, err)
			continue
		}
		rows = append(rows, DialplanRule{
			Position:    i,
			Pattern:     p.pattern,
			Replacement: p.replacement,
			Enabled:     true,
			Description: "seeded from configuration/defaults",
		})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if err := s.DB.Create(&rows).Error; err != nil {
		return nil, fmt.Errorf("seed dialplan rules: %w", err)
	}
	log.Printf("Dialplan: seeded %d rule(s) into dialplan_rules", len(rows))
	return rows, nil
}

// loadDialplanFromDB builds a DialplanManager from the dialplan_rules table,
// seeding it first when empty.
func (s *Server) loadDialplanFromDB() (*DialplanManager, error) {
	if _, err := s.seedDialplanRules(); err != nil {
		return nil, err
	}
	var rows []DialplanRule
	if err := s.DB.Where("enabled = ?", true).Order("position ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return NewDialplanManager(compileDialplanRules(rows)), nil
}

// reloadDialplan rebuilds and atomically swaps the active DialplanManager.
// In "config" mode this is a no-op (config is read once at startup).
func (s *Server) reloadDialplan() error {
	if dialplanSource() != "db" {
		return nil
	}
	dm, err := s.loadDialplanFromDB()
	if err != nil {
		return err
	}
	s.dialplan.Store(dm)
	return nil
}

// DefaultTransformationRules returns the built-in NANP-oriented rules used
// when no dialplan is configured:
//  1. Strip a leading "1" from an 11-digit number (US/Canada country code).
//  2. Truncate a 10+ digit number to its first 10 digits.
//
// Rule 2 assumes tenant numbers are stored in 10-digit NANP format. It
// corrupts non-NANP (e.g. international/E.164) numbers, so deployments
// outside North America should configure their own `dialplan.rules` in
// config.json (an empty list disables transformation entirely).
func DefaultTransformationRules() []TransformationRule {
	return []TransformationRule{
		{
			Pattern:     regexp.MustCompile(`^1(\d{10}).*$`),
			Replacement: "$1",
		},
		{
			Pattern:     regexp.MustCompile(`^(\d{10}).*$`),
			Replacement: "$1",
		},
	}
}

// DialplanManager handles number normalization, regex-based transformations,
// and tenant number lookup for routing.
type DialplanManager struct {
	TransformationRules []TransformationRule
}

// NewDialplanManager creates a new DialplanManager with loaded tenant numbers and transformation rules.
func NewDialplanManager(rules []TransformationRule) *DialplanManager {
	return &DialplanManager{
		TransformationRules: rules,
	}
}

// compileRule compiles a pattern/replacement pair into a TransformationRule.
func compileRule(pattern, replacement string) (TransformationRule, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return TransformationRule{}, err
	}
	return TransformationRule{Pattern: re, Replacement: replacement}, nil
}

// NormalizeNumber removes non-digit characters from a number.
// This makes comparisons easier regardless of formatting.
func (d *DialplanManager) NormalizeNumber(number string) string {
	re := regexp.MustCompile(`\D`)
	return re.ReplaceAllString(number, "")
}

// ApplyTransformationRules applies each configured transformation rule to the input number in sequence.
// For example, if the number is in E.164 format, one rule might convert "+(.*)" to "011$1".
func (d *DialplanManager) ApplyTransformationRules(number string) string {
	transformed := number
	for _, rule := range d.TransformationRules {
		if rule.Pattern.MatchString(transformed) {
			transformed = rule.Pattern.ReplaceAllString(transformed, rule.Replacement)
		}
	}
	return transformed
}

// ConvertDialPlan first applies transformation rules then applies additional logic based on length.
// For instance, if the number becomes 11 digits starting with "1", it could strip the leading digit.
/*func (d *DialplanManager) ConvertDialPlan(number string) string {
	// Apply all regex-based transformations first.
	transformed := d.ApplyTransformationRules(number)

	// Further normalization: remove any remaining non-digits.
	cleaned := d.NormalizeNumber(transformed)
	switch len(cleaned) {
	case 11:
		// If the 11-digit number starts with "1", assume it's a US number and strip the 1.
		if cleaned[0] == '1' {
			return cleaned[1:]
		}
		return cleaned
	case 10:
		return cleaned
	default:
		return cleaned
	}
}*/

// FindTenantNumber looks up a tenant number record based on a dialed number.
// It normalizes the dialed number and compares it with stored tenant numbers.
/*func (d *DialplanManager) FindTenantNumber(number string) *TenantNumber {
	normalized := d.NormalizeNumber(number)
	for _, tn := range d.TenantNumbers {
		if d.NormalizeNumber(tn.Number) == normalized {
			return &tn
		}
	}
	return nil
}*/

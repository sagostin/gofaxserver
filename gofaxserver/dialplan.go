package gofaxserver

import (
	"regexp"
)

// TransformationRule represents a single dialplan transformation.
// When the rule's Pattern matches the input, its Replacement is applied.
// The Replacement string can contain regex capture group references.
type TransformationRule struct {
	Pattern     *regexp.Regexp // Regular expression to match
	Replacement string         // Replacement string, e.g., "011$1" to prefix "011"
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

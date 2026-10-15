package domain

import (
	"strings"
	"testing"
)

func TestCallReport_ValidateAPIKeySource(t *testing.T) {
	for _, source := range []string{"", "ANTHROPIC_API_KEY", "none", "apiKeyHelper", "/login managed key"} {
		if err := (CallReport{APIKeySource: source}).Validate(); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}
	for name, source := range map[string]string{
		"too long":      strings.Repeat("x", maxReportString+1),
		"non-printable": "a\x00b",
	} {
		if err := (CallReport{APIKeySource: source}).Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

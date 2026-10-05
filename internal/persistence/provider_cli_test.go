package persistence

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestValidateProviderCLI(t *testing.T) {
	ok := map[string]domain.ProviderCLI{"claude": {Version: "2.1.289 (Claude Code)", Verified: true}}
	if err := validateProviderCLI(ok); err != nil {
		t.Errorf("valid entry: %v", err)
	}
	for name, m := range map[string]map[string]domain.ProviderCLI{
		"unknown provider": {"gpt": {Version: "1"}},
		"too long":         {"claude": {Version: strings.Repeat("9", 65)}},
		"unprintable":      {"claude": {Version: "2.1.289\x1b[31m"}},
	} {
		if err := validateProviderCLI(m); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

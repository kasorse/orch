package domain

import "testing"

func TestIsEnvName(t *testing.T) {
	for name, want := range map[string]bool{
		"AWS_PROFILE": true, "_X": true, "a1": true,
		"": false, "1A": false, "AWS-PROFILE": false, "A B": false, "A=b": false, "Ä": false,
	} {
		if got := IsEnvName(name); got != want {
			t.Errorf("IsEnvName(%q) = %v, want %v", name, got, want)
		}
	}
}

// specs/claude-auth/SPEC.md §17.3: the variables that change the model a route reaches.
func TestClaudeModelVar(t *testing.T) {
	for name, want := range map[string]bool{
		"ANTHROPIC_MODEL": true, "ANTHROPIC_SMALL_FAST_MODEL": true, "ANTHROPIC_CUSTOM_MODEL_OPTION": true,
		"ANTHROPIC_DEFAULT_OPUS_MODEL": true, "ANTHROPIC_DEFAULT_FABLE_MODEL": true,
		"ANTHROPIC_DEFAULT__MODEL": false, "ANTHROPIC_BASE_URL": false, "AWS_PROFILE": false, "ANTHROPIC_DEFAULT_OPUS": false,
	} {
		if got := ClaudeModelVar(name); got != want {
			t.Errorf("ClaudeModelVar(%q) = %v, want %v", name, got, want)
		}
	}
}

package execution

import (
	"slices"
	"strings"
	"testing"
)

func TestScrubbedEnv(t *testing.T) {
	for name := range parentSessionVars {
		t.Setenv(name, "planted")
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1") // a similar prefix is not a match
	t.Setenv("ORCH_TEST_KEEP", "kept")

	env := ScrubbedEnv()
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if parentSessionVars[name] {
			t.Errorf("%s was not scrubbed", name)
		}
	}
	for _, want := range []string{"ORCH_TEST_KEEP=kept", "CLAUDE_CODE_USE_BEDROCK=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("expected %s to be inherited", want)
		}
	}
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Error("expected PATH to be inherited")
	}
}

// TestScrub_EmptyValueAndNoEquals: an entry without "=" or with an empty value is still
// matched by name.
func TestScrub_EmptyValueAndNoEquals(t *testing.T) {
	got := scrub([]string{"CLAUDECODE=", "CLAUDE_PID", "KEEP=1"})
	if !slices.Equal(got, []string{"KEEP=1"}) {
		t.Errorf("scrub = %q", got)
	}
}

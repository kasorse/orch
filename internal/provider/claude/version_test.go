package claude

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/execution"
)

func TestVersionRange(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"2.1.282", false}, {"2.1.283", true}, {"2.1.999", true}, {"2.2.0", false}, {"3.0.0", false},
		{"2.1.290-beta", false}, {"", false}, {"garbage", false}, {"2.1.289 (Claude Code)", true},
	} {
		v, ok := parseVersion(tc.in)
		if got := ok && inVerifiedRange(v); got != tc.want {
			t.Errorf("%q: verified = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCLIVersion(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "2.2.1 (Claude Code)\nmore\n"}}}
	v, ok, err := New(fake).CLIVersion(context.Background())
	if err != nil || ok || v != "2.2.1 (Claude Code)" {
		t.Errorf("got %q, %v, %v", v, ok, err)
	}
	if got := fake.Calls[0].Args; len(got) != 1 || got[0] != "--version" {
		t.Errorf("args = %q", got)
	}

	fake = &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{ExitCode: 1}}}
	if _, _, err := New(fake).CLIVersion(context.Background()); err == nil {
		t.Error("non-zero exit must be an error")
	}
	fake = &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}, Errs: []error{errors.New("boom")}}
	if _, _, err := New(fake).CLIVersion(context.Background()); err == nil {
		t.Error("exec failure must be an error")
	}
	fake = &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: strings.Repeat("9", 200)}}}
	if v, ok, _ := New(fake).CLIVersion(context.Background()); ok || len(v) > 64 {
		t.Errorf("long output: %d bytes, verified %v", len(v), ok)
	}
}

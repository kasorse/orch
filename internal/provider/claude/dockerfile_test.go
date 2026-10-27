package claude

import (
	"os"
	"regexp"
	"testing"
)

// The Dockerfile's default claude CLI must stay inside the adapter's verified range
// (specs/delivery/SPEC.md §11.4); raising verifiedMin or the range without the pin fails here.
func TestDockerfileClaudeVersionInVerifiedRange(t *testing.T) {
	data, err := os.ReadFile("../../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG CLAUDE_CODE_VERSION=(\S+)$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("Dockerfile has no ARG CLAUDE_CODE_VERSION default")
	}
	v, ok := parseVersion(string(m[1]))
	if !ok || !inVerifiedRange(v) {
		t.Errorf("Dockerfile pins claude %s, outside the verified range %s", m[1], (&Client{}).VerifiedRange())
	}
}

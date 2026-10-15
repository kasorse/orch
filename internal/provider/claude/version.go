package claude

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/execution"
)

// The claude versions every authority's flag set was verified on: a minimum and the next
// minor as the exclusive upper bound (specs/hardening/SPEC.md §7.1). Raising the minimum
// or crossing a minor needs a green `go test -tags security ./test/security/...` on that
// version and an entry in specs/permissions/DECISIONS.md.
var (
	verifiedMin          = [3]int{2, 1, 283}
	verifiedMaxExclusive = [3]int{2, 2, 0}
)

const (
	versionTimeout = 10 * time.Second
	maxVersionLen  = 64
)

var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(\s|$)`)

// parseVersion reads "2.1.289 (Claude Code)"; a pre-release suffix, empty or other output
// cannot be parsed.
func parseVersion(firstLine string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(firstLine)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func inVerifiedRange(v [3]int) bool {
	return !less(v, verifiedMin) && less(v, verifiedMaxExclusive)
}

func (c *Client) VerifiedRange() string {
	return fmt.Sprintf(">=%d.%d.%d <%d.%d.%d",
		verifiedMin[0], verifiedMin[1], verifiedMin[2],
		verifiedMaxExclusive[0], verifiedMaxExclusive[1], verifiedMaxExclusive[2])
}

// CLIVersion runs `claude --version`. An error means the CLI could not be run (or printed
// nothing); a version outside the verified range, or one that cannot be parsed, is
// verified == false with a nil error.
func (c *Client) CLIVersion(ctx context.Context) (string, bool, error) {
	// The probe runs under the same environment as a call (specs/claude-auth/SPEC.md §7).
	env, _, err := c.env()
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	res, err := c.Executor.Run(ctx, execution.Spec{
		Executable: executable,
		Args:       []string{"--version"},
		Env:        env,
	})
	if err != nil {
		return "", false, fmt.Errorf("%s --version: %w", executable, err)
	}
	if res.ExitCode != 0 {
		return "", false, fmt.Errorf("%s --version exited with code %d", executable, res.ExitCode)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(res.Stdout), "\n")
	line = strings.TrimSpace(line)
	if len(line) > maxVersionLen {
		line = line[:maxVersionLen]
	}
	v, ok := parseVersion(line)
	return line, ok && inVerifiedRange(v), nil
}

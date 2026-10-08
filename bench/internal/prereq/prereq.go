// Package prereq checks that the programs a command needs are there before it starts, and
// refuses with everything that is missing (specs/benchmark/SPEC.md §13.1).
package prereq

import (
	"fmt"
	"os/exec"
	"strings"

	"orchbench/internal/exitcode"
)

// Need is a program a command depends on.
type Need string

const (
	Git          Need = "git"
	Bwrap        Need = "bwrap"
	Orchestrator Need = "orchestrator"
	Claude       Need = "claude"
)

// Overrides are the --orchestrator and --claude paths; empty means look the name up on PATH.
type Overrides struct {
	Orchestrator string
	Claude       string
}

// LookPath resolves a program name or path to an executable, like exec.LookPath.
type LookPath func(file string) (string, error)

// Check looks up every need and returns one exitcode.UsageError listing all that are
// missing, or nil. Needs are checked and listed in the order given. A nil lookPath means
// exec.LookPath.
func Check(needs []Need, ov Overrides, lookPath LookPath) error {
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	var missing []string
	seen := make(map[Need]bool)
	for _, n := range needs {
		if seen[n] {
			continue
		}
		seen[n] = true

		target, flag := string(n), ""
		switch {
		case n == Orchestrator && ov.Orchestrator != "":
			target, flag = ov.Orchestrator, "--orchestrator"
		case n == Claude && ov.Claude != "":
			target, flag = ov.Claude, "--claude"
		}

		if _, err := lookPath(target); err != nil {
			if flag != "" {
				missing = append(missing, fmt.Sprintf("%s (%s %s: not found)", n, flag, target))
			} else {
				missing = append(missing, fmt.Sprintf("%s (not on PATH)", n))
			}
		}
	}

	if len(missing) == 0 {
		return nil
	}
	return exitcode.UsageError{Msg: "missing prerequisites: " + strings.Join(missing, ", ")}
}

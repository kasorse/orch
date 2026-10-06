package prereq

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"orchbench/internal/exitcode"
)

// fakeLookPath finds the names in have and fails for everything else.
func fakeLookPath(have ...string) (LookPath, *[]string) {
	var asked []string
	return func(file string) (string, error) {
		asked = append(asked, file)
		for _, h := range have {
			if h == file {
				return "/usr/bin/" + file, nil
			}
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}, &asked
}

func TestCheck(t *testing.T) {
	all := []Need{Git, Bwrap, Orchestrator, Claude}

	tests := []struct {
		name  string
		needs []Need
		ov    Overrides
		have  []string
		want  string // empty: no error
	}{
		{"all present", all, Overrides{}, []string{"git", "bwrap", "orchestrator", "claude"}, ""},
		{"nothing needed", nil, Overrides{}, nil, ""},
		{"one missing", all, Overrides{}, []string{"git", "orchestrator", "claude"}, "missing prerequisites: bwrap (not on PATH)"},
		{
			"several missing, listed together in the order asked",
			all, Overrides{}, []string{"git"},
			"missing prerequisites: bwrap (not on PATH), orchestrator (not on PATH), claude (not on PATH)",
		},
		{"only what is needed is checked", []Need{Git}, Overrides{}, []string{"git"}, ""},
		{"duplicate need is listed once", []Need{Claude, Claude}, Overrides{}, nil, "missing prerequisites: claude (not on PATH)"},
		{
			"override is looked up instead of the name",
			[]Need{Orchestrator, Claude}, Overrides{Orchestrator: "./bin/orchestrator", Claude: "/opt/claude"},
			[]string{"./bin/orchestrator", "/opt/claude"}, "",
		},
		{
			"the name on PATH does not satisfy an override",
			[]Need{Orchestrator, Claude}, Overrides{Orchestrator: "./bin/orchestrator", Claude: "/opt/claude"},
			[]string{"orchestrator", "claude"},
			"missing prerequisites: orchestrator (--orchestrator ./bin/orchestrator: not found), claude (--claude /opt/claude: not found)",
		},
		{"an override for a program not needed is ignored", []Need{Git}, Overrides{Claude: "/nope"}, []string{"git"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			look, _ := fakeLookPath(tt.have...)
			err := Check(tt.needs, tt.ov, look)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Check = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Check succeeded")
			}
			if err.Error() != tt.want {
				t.Fatalf("Check = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestCheckMapsToUsageExitCode(t *testing.T) {
	look, _ := fakeLookPath()

	err := Check([]Need{Git}, Overrides{}, look)
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Fatalf("exit code = %d, want %d", got, exitcode.Usage)
	}

	wrapped := fmt.Errorf("run: %w", err)
	var usage exitcode.UsageError
	if !errors.As(wrapped, &usage) {
		t.Fatal("not a UsageError")
	}
}

func TestCheckAsksForEachNeedOnce(t *testing.T) {
	look, asked := fakeLookPath("git", "bwrap")

	if err := Check([]Need{Git, Bwrap, Git}, Overrides{}, look); err != nil {
		t.Fatal(err)
	}
	if want := []string{"git", "bwrap"}; fmt.Sprint(*asked) != fmt.Sprint(want) {
		t.Fatalf("asked %v, want %v", *asked, want)
	}
}

func TestCheckDefaultsToExecLookPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := Check([]Need{Git}, Overrides{}, nil)
	if err == nil {
		t.Fatal("git found on an empty PATH")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Fatalf("exit code = %d", got)
	}
}

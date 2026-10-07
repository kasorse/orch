package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"orchbench/internal/exitcode"
	"orchbench/internal/prereq"
)

func runArgs(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), args, &out, &errOut)
	return out.String(), errOut.String(), exitcode.FromError(err)
}

func TestRootHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := runArgs(t, arg)
			if code != exitcode.OK {
				t.Fatalf("exit code = %d, stderr %q", code, stderr)
			}
			for _, want := range []string{
				"mine", "validate", "run", "summarize",
				"--cache-dir", "--orchestrator", "--claude", "-v",
				"Exit codes", "130", "--max-cost",
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("help does not mention %q:\n%s", want, stdout)
				}
			}
			if stderr != "" {
				t.Errorf("stderr = %q", stderr)
			}
		})
	}
}

func TestCommandHelp(t *testing.T) {
	tests := []struct {
		command  string
		synopsis string
		flags    []string
	}{
		{"mine", "orchbench mine --repo <path> --out <dir> [--since <date>] [--rev <rev>]", []string{"-repo", "-out", "-since", "-rev", "-max-files", "-max-lines", "-language", "-test-glob", "-remote"}},
		{"validate", "orchbench validate --tasks <glob> [--tasks <glob>...] [--ids a,b] [-j 1] [--keep-workspaces]", []string{"-tasks", "-ids", "-j", "-keep-workspaces"}},
		{"run", "orchbench run --tasks <glob> [--tasks <glob>...] --out <dir> --max-cost <usd>", []string{"-tasks", "-out", "-max-cost", "-configs", "-reps", "-ids", "-limit", "-j", "-redo", "-keep-workspaces", "-dry-run", "-allow-mixed", "-contributor"}},
		{"summarize", "orchbench summarize <out> [--baseline A] [--seed 1] [--by language] [--stdout]", []string{"-baseline", "-seed", "-by", "-stdout"}},
	}

	for _, tt := range tests {
		for _, helpFlag := range []string{"--help", "-h"} {
			t.Run(tt.command+" "+helpFlag, func(t *testing.T) {
				stdout, stderr, code := runArgs(t, tt.command, helpFlag)
				if code != exitcode.OK {
					t.Fatalf("exit code = %d, stderr %q", code, stderr)
				}
				if !strings.Contains(stdout, tt.synopsis) {
					t.Errorf("help does not contain the synopsis %q:\n%s", tt.synopsis, stdout)
				}
				for _, f := range append([]string{"-cache-dir", "-orchestrator", "-claude", "-v"}, tt.flags...) {
					if !strings.Contains(stdout, "\n  "+f) {
						t.Errorf("help does not list flag %s:\n%s", f, stdout)
					}
				}
				if !strings.Contains(stdout, "Exit codes") {
					t.Errorf("help has no exit codes:\n%s", stdout)
				}
				if stderr != "" {
					t.Errorf("stderr = %q", stderr)
				}
			})
		}
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", nil, "no command"},
		{"unknown command", []string{"frobnicate"}, `unknown command "frobnicate"`},
		{"a flag instead of a command", []string{"--cache-dir", "x", "run"}, "unknown command"},
		{"unknown flag", []string{"run", "--nope"}, "flag provided but not defined: -nope"},
		{"flag without its value", []string{"validate", "--tasks"}, "needs an argument"},
		{"bad value", []string{"run", "--reps", "many"}, "invalid value"},
		{"unexpected argument", []string{"run", "extra"}, `unexpected argument "extra"`},
		{"second argument", []string{"summarize", "a", "b"}, `unexpected argument "b"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := run(context.Background(), tt.args, &out, &errOut)
			if err == nil {
				t.Fatal("run succeeded")
			}
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Fatalf("exit code = %d, want %d (%v)", code, exitcode.Usage, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q", out.String())
			}
		})
	}
}

func TestNoArgumentsPrintsUsageToStderr(t *testing.T) {
	stdout, stderr, code := runArgs(t)
	if code != exitcode.Usage {
		t.Fatalf("exit code = %d", code)
	}
	if stdout != "" || !strings.Contains(stderr, "Usage: orchbench <command>") {
		t.Fatalf("stdout %q, stderr %q", stdout, stderr)
	}
}

func TestStubsAreNotImplemented(t *testing.T) {
	tests := []struct {
		command string
		args    []string
	}{
		{"run", []string{"--tasks", "t/*.yaml", "--out", "/o", "--max-cost", "30", "--configs", "A,B", "--dry-run", "-v"}},
	}

	for _, tt := range tests {
		t.Run(tt.command+" "+strings.Join(tt.args, " "), func(t *testing.T) {
			args := append([]string{tt.command}, tt.args...)
			args = append(args, "--cache-dir", t.TempDir(), "--orchestrator", "/x/orchestrator", "--claude", "/x/claude")

			var out, errOut bytes.Buffer
			err := run(context.Background(), args, &out, &errOut)
			if err == nil {
				t.Fatal("a stub succeeded")
			}
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Fatalf("exit code = %d, want %d", code, exitcode.Usage)
			}
			if want := "orchbench " + tt.command + ": not implemented"; err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
		})
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out, errOut bytes.Buffer
	err := run(ctx, []string{"run", "--max-cost", "1"}, &out, &errOut)
	if code := exitcode.FromError(err); code != exitcode.Interrupted {
		t.Fatalf("exit code = %d, want %d (%v)", code, exitcode.Interrupted, err)
	}
}

func TestCommandNeeds(t *testing.T) {
	want := map[string][]prereq.Need{
		"mine":      {prereq.Git},
		"validate":  {prereq.Git, prereq.Bwrap},
		"run":       {prereq.Git, prereq.Bwrap, prereq.Orchestrator, prereq.Claude},
		"summarize": nil,
	}

	if len(commands) != len(want) {
		t.Fatalf("%d commands, want %d", len(commands), len(want))
	}
	for _, c := range commands {
		needs, ok := want[c.name]
		if !ok {
			t.Errorf("unexpected command %q", c.name)
			continue
		}
		if len(c.needs) != len(needs) {
			t.Errorf("%s needs %v, want %v", c.name, c.needs, needs)
			continue
		}
		for i := range needs {
			if c.needs[i] != needs[i] {
				t.Errorf("%s needs %v, want %v", c.name, c.needs, needs)
			}
		}
	}
}

func TestDefaultCacheDir(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()

	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{"XDG_CACHE_HOME", xdg, filepath.Join(xdg, "orchbench")},
		{"unset", "", filepath.Join(home, ".cache", "orchbench")},
		{"relative is ignored", "relative/cache", filepath.Join(home, ".cache", "orchbench")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_CACHE_HOME", tt.xdg)
			if got := defaultCacheDir(); got != tt.want {
				t.Fatalf("defaultCacheDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHelpShowsCacheDirDefault(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)

	stdout, _, code := runArgs(t, "run", "--help")
	if code != exitcode.OK {
		t.Fatalf("exit code = %d", code)
	}
	if want := fmt.Sprintf("(default %q)", filepath.Join(xdg, "orchbench")); !strings.Contains(stdout, want) {
		t.Fatalf("help does not show %q:\n%s", want, stdout)
	}
}

func TestCommonFlagsAreParsed(t *testing.T) {
	var c commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c.register(fs)

	if _, err := parseInterspersed(fs, []string{"--cache-dir", "/c", "--orchestrator", "/o", "--claude", "/cl", "-v"}); err != nil {
		t.Fatal(err)
	}
	if c.cacheDir != "/c" || c.orchestrator != "/o" || c.claude != "/cl" || !c.verbose {
		t.Fatalf("flags = %+v", c)
	}
}

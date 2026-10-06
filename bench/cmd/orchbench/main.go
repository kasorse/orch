// Command orchbench is the benchmark driver of specs/benchmark/SPEC.md §13: it makes tasks
// from history, checks them, runs configurations over them and reports. This file is the
// frame; the subcommands are filled in by their own tasks (BM06, BM08, BM10, BM11).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"orchbench/internal/exitcode"
	"orchbench/internal/prereq"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchbench:", err)
	}
	os.Exit(exitcode.FromError(err))
}

// command is one subcommand: its §13 synopsis, the programs it needs and its own flags.
type command struct {
	name     string
	summary  string
	synopsis string
	needs    []prereq.Need
	args     int // positional arguments accepted
	flags    func(fs *flag.FlagSet)
}

const exitCodes = `Exit codes, the same for every command:
  0    done
  1    done, but something failed (an invalid task, a cell with infra_error left)
  2    usage or a missing prerequisite, nothing ran
  3    stopped by --max-cost
  130  interrupted (Ctrl-C)`

var commands = []command{
	{
		name:    "mine",
		summary: "draft tasks from a repository's history",
		synopsis: `orchbench mine --repo <path> --out <dir> [--since <date>] [--rev <rev>]
               [--max-files 10] [--max-lines 400] [--language <name>] [--test-glob <glob>...]
               [--remote <url>]`,
		needs: []prereq.Need{prereq.Git},
		flags: func(fs *flag.FlagSet) {
			fs.String("repo", "", "local clone to read; never changed (required)")
			fs.String("out", "", "`dir` where draft YAML files are written, one per candidate (required)")
			fs.String("since", "", "only commits after this `date` (YYYY-MM-DD); default the whole history")
			fs.String("rev", "HEAD", "the branch or commit whose history is walked")
			fs.Int("max-files", 10, "size bound on the code change in files, tests not counted")
			fs.Int("max-lines", 400, "size bound on the code change in lines, tests not counted")
			fs.String("language", "", "the `name` of the language whose defaults the drafts get; default from file extensions")
			fs.Var(&stringList{}, "test-glob", "what counts as a test file; repeatable; required for a language without defaults")
			fs.String("remote", "", "`url` written into repo: of the drafts; default the clone's origin URL, else --repo")
		},
	},
	{
		name:     "validate",
		summary:  "check tasks against their repository, no model",
		synopsis: `orchbench validate --tasks <glob> [--tasks <glob>...] [--ids a,b] [-j 1] [--keep-workspaces]`,
		needs:    []prereq.Need{prereq.Git, prereq.Bwrap},
		flags: func(fs *flag.FlagSet) {
			fs.Var(&stringList{}, "tasks", "task files (quoted glob); repeatable (required)")
			fs.String("ids", "", "only these task ids, comma-separated; default all")
			fs.Int("j", 1, "tasks checked in parallel")
			fs.Bool("keep-workspaces", false, "keep each workspace for inspection; the path is printed")
		},
	},
	{
		name:    "run",
		summary: "run every task × config × repetition and record the results",
		synopsis: `orchbench run --tasks <glob> [--tasks <glob>...] --out <dir> --max-cost <usd>
              [--configs A,B,C,D] [--reps 3] [--ids a,b] [--limit N] [-j 1]
              [--redo] [--keep-workspaces] [--dry-run] [--allow-mixed]`,
		needs: []prereq.Need{prereq.Git, prereq.Bwrap, prereq.Orchestrator, prereq.Claude},
		flags: func(fs *flag.FlagSet) {
			fs.Var(&stringList{}, "tasks", "task files (quoted glob); repeatable; each must be valid with the same hash (required)")
			fs.String("out", "", "results `dir`; if it exists, the run continues it (required)")
			fs.Float64("max-cost", 0, "dollars; no new cell starts once the recorded cost reaches it (required)")
			fs.String("configs", "A,B,C,D", "which configs, comma-separated")
			fs.Int("reps", 3, "repetitions per task and config")
			fs.String("ids", "", "only these task ids, comma-separated; default all")
			fs.Int("limit", 0, "only the first N tasks by id, after --ids; 0 is all")
			fs.Int("j", 1, "cells in parallel")
			fs.Bool("redo", false, "run the selected cells again even if they have a result; the new line replaces the old")
			fs.Bool("keep-workspaces", false, "keep each cell's workspace; the path is in the progress line")
			fs.Bool("dry-run", false, "print the cells in order and their count, run nothing")
			fs.Bool("allow-mixed", false, "continue an --out whose manifest.json names another orchestrator commit or CLI version")
			fs.String("contributor", "owner", "a short `name` written into manifest.json and every result line")
		},
	},
	{
		name:     "summarize",
		summary:  "write the report of a results directory",
		synopsis: `orchbench summarize <out> [--baseline A] [--seed 1] [--stdout]`,
		args:     1,
		flags: func(fs *flag.FlagSet) {
			fs.String("baseline", "A", "the `config` every other one is paired against")
			fs.Int("seed", 1, "the bootstrap's seed, so the same results give the same report")
			fs.Bool("stdout", false, "print instead of writing <out>/summary.md")
		},
	},
}

// commonFlags are accepted by every command (SPEC.md §13.2).
type commonFlags struct {
	cacheDir     string
	orchestrator string
	claude       string
	verbose      bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.cacheDir, "cache-dir", defaultCacheDir(), "mirrors, workspaces and validated.json live in this `dir`")
	fs.StringVar(&c.orchestrator, "orchestrator", "", "the binary under test; default `orchestrator` on PATH")
	fs.StringVar(&c.claude, "claude", "", "config A, and what the orchestrator's config routes to; default `claude` on PATH")
	fs.BoolVar(&c.verbose, "v", false, "print every command orchbench runs and its output live")
}

// defaultCacheDir is $XDG_CACHE_HOME/orchbench, or ~/.cache/orchbench when XDG_CACHE_HOME is
// unset or not an absolute path (as the XDG specification says to treat it).
func defaultCacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "orchbench")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "orchbench")
	}
	return ""
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// run is main without the process: it returns the error whose exitcode.FromError is the exit
// code. --help is not an error.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return exitcode.UsageError{Msg: "no command given"}
	}

	switch args[0] {
	case "-h", "-help", "--help", "help":
		printUsage(stdout)
		return nil
	}

	for _, c := range commands {
		if c.name == args[0] {
			return runCommand(ctx, c, args[1:], stdout)
		}
	}
	return exitcode.UsageError{Msg: fmt.Sprintf("unknown command %q; see 'orchbench --help'", args[0])}
}

func runCommand(ctx context.Context, c command, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("orchbench "+c.name, flag.ContinueOnError)
	// The flag package prints its own errors and usage; both are handled below instead, so
	// that --help goes to stdout and a mistake is reported once, by main.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var common commonFlags
	common.register(fs)
	c.flags(fs)

	positional, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		printCommandHelp(stdout, c, fs)
		return nil
	}
	if err != nil {
		return exitcode.UsageError{Msg: fmt.Sprintf("orchbench %s: %v; see 'orchbench %s --help'", c.name, err, c.name)}
	}
	if len(positional) > c.args {
		return exitcode.UsageError{Msg: fmt.Sprintf("orchbench %s: unexpected argument %q; see 'orchbench %s --help'", c.name, positional[c.args], c.name)}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	return exitcode.UsageError{Msg: fmt.Sprintf("orchbench %s: not implemented", c.name)}
}

// parseInterspersed parses flags that may come after positional arguments, as in
// `orchbench summarize <out> --seed 2`, which package flag alone would take as one
// positional and a stray flag.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "orchbench — measure the orchestrator against bare Claude Code on replayed commits")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: orchbench <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common flags, accepted by every command:")
	fmt.Fprintln(w, "  --cache-dir <dir>       mirrors, workspaces, validated.json (default $XDG_CACHE_HOME/orchbench, else ~/.cache/orchbench)")
	fmt.Fprintln(w, "  --orchestrator <path>   the binary under test (default orchestrator on PATH)")
	fmt.Fprintln(w, "  --claude <path>         config A, and what the orchestrator's config routes to (default claude on PATH)")
	fmt.Fprintln(w, "  -v                      print every command orchbench runs and its output live")
	fmt.Fprintln(w)
	fmt.Fprintln(w, exitCodes)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "'orchbench <command> --help' shows the synopsis and flags of a command.")
}

func printCommandHelp(w io.Writer, c command, fs *flag.FlagSet) {
	fmt.Fprintf(w, "orchbench %s — %s\n\nUsage:\n", c.name, c.summary)
	for _, line := range strings.Split(c.synopsis, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
	fmt.Fprintln(w, "\nFlags:")
	fs.SetOutput(w)
	fs.PrintDefaults()
	fmt.Fprintln(w)
	fmt.Fprintln(w, exitCodes)
}

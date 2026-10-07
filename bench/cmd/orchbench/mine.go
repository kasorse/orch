package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"orchbench/internal/exitcode"
	"orchbench/internal/mine"
)

// runMine is `orchbench mine` (SPEC.md §13.3).
func runMine(ctx context.Context, inv invocation) error {
	usage := func(format string, args ...any) error {
		return exitcode.UsageError{Msg: "orchbench mine: " + fmt.Sprintf(format, args...)}
	}
	get := func(name string) any { return inv.fs.Lookup(name).Value.(flag.Getter).Get() }

	repo, out := get("repo").(string), get("out").(string)
	switch {
	case repo == "":
		return usage("--repo is required")
	case out == "":
		return usage("--out is required")
	}
	since := get("since").(string)
	if since != "" {
		if _, err := time.Parse("2006-01-02", since); err != nil {
			return usage("--since %q: want a date as YYYY-MM-DD", since)
		}
	}
	maxFiles, maxLines := get("max-files").(int), get("max-lines").(int)
	if maxFiles < 1 || maxLines < 1 {
		return usage("--max-files and --max-lines must be at least 1")
	}
	if err := checkPrereqs(ctx, inv.cmd, inv.common); err != nil {
		return err
	}

	repoDir, err := expandHome(repo)
	if err != nil {
		return usage("--repo %q: %v", repo, err)
	}
	outDir, err := expandHome(out)
	if err != nil {
		return usage("--out %q: %v", out, err)
	}
	opts := mine.Options{
		Repo:      repoDir,
		Out:       outDir,
		Rev:       get("rev").(string),
		Since:     since,
		MaxFiles:  maxFiles,
		MaxLines:  maxLines,
		Language:  get("language").(string),
		TestGlobs: *inv.fs.Lookup("test-glob").Value.(*stringList),
		Remote:    get("remote").(string),
	}
	if inv.common.verbose {
		opts.Verbose = inv.stderr
	}

	_, err = mine.Run(ctx, opts, inv.stdout)
	if errors.Is(err, mine.ErrNoLanguage) {
		return usage("%v", err)
	}
	return err
}

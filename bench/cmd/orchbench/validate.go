package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"orchbench/internal/cache"
	"orchbench/internal/exitcode"
	"orchbench/internal/validate"
)

// runValidate is `orchbench validate` (SPEC.md §13.4).
func runValidate(ctx context.Context, inv invocation) error {
	paths, err := selectTasks(inv.fs)
	if err != nil {
		return err
	}
	jobs := inv.fs.Lookup("j").Value.(flag.Getter).Get().(int)
	if jobs < 1 {
		return exitcode.UsageError{Msg: fmt.Sprintf("orchbench validate: -j %d: must be at least 1", jobs)}
	}
	if err := checkPrereqs(ctx, inv.cmd, inv.common); err != nil {
		return err
	}

	dir := cache.Dir(inv.common.cacheDir)
	if err := dir.Ensure(); err != nil {
		return err
	}
	opts := validate.Options{
		Cache:          dir,
		Jobs:           jobs,
		KeepWorkspaces: inv.fs.Lookup("keep-workspaces").Value.(flag.Getter).Get().(bool),
	}
	if inv.common.verbose {
		opts.Verbose = inv.stderr
	}

	outcomes, err := validate.Run(ctx, paths, opts, inv.stdout)
	if err != nil {
		return err
	}
	invalid := 0
	for _, o := range outcomes {
		if !o.OK {
			invalid++
		}
	}
	if invalid > 0 {
		return fmt.Errorf("%d of %d tasks invalid", invalid, len(outcomes))
	}
	return nil
}

// selectTasks expands the --tasks globs (a leading ~ is the home directory, since the glob is
// quoted), keeps the --ids, and returns the files sorted by id. A glob that matches nothing,
// an --ids entry without a file and two files with one id are usage errors.
func selectTasks(fs *flag.FlagSet) ([]string, error) {
	name := strings.TrimPrefix(fs.Name(), "orchbench ")
	usage := func(format string, args ...any) error {
		return exitcode.UsageError{Msg: fmt.Sprintf("orchbench %s: ", name) + fmt.Sprintf(format, args...)}
	}

	globs := *fs.Lookup("tasks").Value.(*stringList)
	if len(globs) == 0 {
		return nil, usage("--tasks is required")
	}

	byID := make(map[string]string)
	for _, g := range globs {
		pattern, err := expandHome(g)
		if err != nil {
			return nil, usage("--tasks %q: %v", g, err)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, usage("--tasks %q: %v", g, err)
		}
		if len(matches) == 0 {
			return nil, usage("--tasks %q matches no file", g)
		}
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				return nil, err
			}
			id := fileID(abs)
			if prev, ok := byID[id]; ok && prev != abs {
				return nil, usage("two task files with id %q: %s and %s", id, prev, abs)
			}
			byID[id] = abs
		}
	}

	if ids := fs.Lookup("ids").Value.String(); ids != "" {
		keep := make(map[string]string)
		for _, id := range strings.Split(ids, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			p, ok := byID[id]
			if !ok {
				return nil, usage("--ids: no task file with id %q", id)
			}
			keep[id] = p
		}
		byID = keep
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	paths := make([]string, len(ids))
	for i, id := range ids {
		paths[i] = byID[id]
	}
	return paths, nil
}

func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot expand ~: no home directory")
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

func fileID(p string) string {
	name := filepath.Base(p)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"orchbench/internal/exitcode"
	"orchbench/internal/results"
	"orchbench/internal/summary"
)

// resultsFile and summaryFile are the files of a results directory that summarize reads and writes.
const (
	resultsFile = "results.jsonl"
	summaryFile = "summary.md"
)

// runSummarize is `orchbench summarize` (SPEC.md §13.6). It reads results.jsonl only, so it can run
// during a run, on the cells done so far.
func runSummarize(ctx context.Context, inv invocation) error {
	usage := func(format string, args ...any) error {
		return exitcode.UsageError{Msg: "orchbench summarize: " + fmt.Sprintf(format, args...)}
	}
	get := func(name string) any { return inv.fs.Lookup(name).Value.(flag.Getter).Get() }

	if len(inv.positional) == 0 {
		return usage("<out> is required; see 'orchbench summarize --help'")
	}
	by := get("by").(string)
	if !summary.ValidBy(by) {
		return usage("--by %q: want %s, %s or %s", by, summary.ByLanguage, summary.ByCategory, summary.ByContributor)
	}
	seed := get("seed").(int)
	if seed < 0 {
		return usage("--seed %d: must be 0 or more", seed)
	}

	out, err := expandHome(inv.positional[0])
	if err != nil {
		return usage("%q: %v", inv.positional[0], err)
	}
	if info, err := os.Stat(out); err != nil || !info.IsDir() {
		return usage("%q is not a results directory", inv.positional[0])
	}

	path := filepath.Join(out, resultsFile)
	recs, err := results.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return usage("%s not found: %q is not a results directory", resultsFile, inv.positional[0])
	}
	if err != nil {
		return err
	}

	report := summary.Compute(recs.Cells, summary.Options{
		Baseline: get("baseline").(string),
		Seed:     uint64(seed),
		By:       by,
	})
	report.Coverage.Replaced, report.Coverage.Partial = recs.Replaced, recs.Partial
	var buf bytes.Buffer
	if err := summary.Render(&buf, report); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if get("stdout").(bool) {
		_, err := inv.stdout.Write(buf.Bytes())
		return err
	}
	dest := filepath.Join(out, summaryFile)
	if err := writeAtomic(dest, buf.Bytes()); err != nil {
		return err
	}
	fmt.Fprintf(inv.stdout, "wrote %s (%d cells)\n", dest, report.Coverage.Cells)
	return nil
}

// writeAtomic writes data to a temporary file next to path and renames it over path, so a reader
// never sees half a report.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

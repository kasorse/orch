package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"orchbench/internal/cache"
	"orchbench/internal/config"
	"orchbench/internal/exitcode"
	"orchbench/internal/lang"
	"orchbench/internal/results"
	benchrun "orchbench/internal/run"
	"orchbench/internal/runner"
	"orchbench/internal/task"
	"orchbench/internal/workspace"
)

// contributorPattern is a short name: it lands in manifest.json and in every result line.
var contributorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// maxProblems is how many problems of one kind an error lists before "and N more".
const maxProblems = 5

// configDirs are where --configs-dir defaults to, relative to the current directory.
var configDirs = []string{filepath.Join("bench", "configs"), "configs"}

// probeBuild asks the programs under test who they are: the orchestrator's `version` output and
// the SHA-256 of its binary, and the claude CLI's `--version`. Tests replace it.
var probeBuild = func(ctx context.Context, common commonFlags) (results.Build, string, error) {
	orchPath, err := exec.LookPath(orDefault(common.orchestrator, "orchestrator"))
	if err != nil {
		return results.Build{}, "", err
	}
	version, err := programOutput(ctx, orchPath, "version")
	if err != nil {
		return results.Build{}, "", err
	}
	sum, err := fileSHA256(orchPath)
	if err != nil {
		return results.Build{}, "", err
	}

	claudePath, err := exec.LookPath(orDefault(common.claude, "claude"))
	if err != nil {
		return results.Build{}, "", err
	}
	cli, err := programOutput(ctx, claudePath, "--version")
	if err != nil {
		return results.Build{}, "", err
	}
	return results.Build{Version: version, SHA256: sum}, cli, nil
}

// newExec is how a cell is executed; tests replace it with a fake that calls no model.
var newExec = func(env benchrun.Env) benchrun.ExecFunc { return env.Exec }

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// programOutput is what a program prints on stdout, on one line, within half a minute.
func programOutput(ctx context.Context, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		detail := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			detail += ": " + strings.Join(strings.Fields(string(exitErr.Stderr)), " ")
		}
		return "", fmt.Errorf("%s %s: %s", bin, strings.Join(args, " "), detail)
	}
	return strings.Join(strings.Fields(string(out)), " "), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runRun is `orchbench run` (SPEC.md §13.5). What is wrong with the call, the tasks or the
// configs is found before anything is written or paid for, and is exit 2: a task that is not
// validated with its current hash, a config that cannot be rendered for a task, a manifest
// that names another build.
func runRun(ctx context.Context, inv invocation) error {
	usage := func(format string, args ...any) error {
		return exitcode.UsageError{Msg: "orchbench run: " + fmt.Sprintf(format, args...)}
	}
	get := func(name string) any { return inv.fs.Lookup(name).Value.(flag.Getter).Get() }

	outFlag := get("out").(string)
	if outFlag == "" {
		return usage("--out is required")
	}
	maxCost := get("max-cost").(float64)
	if !(maxCost > 0) || math.IsInf(maxCost, 0) {
		return usage("--max-cost is required and must be a positive number of dollars; there is no unlimited default")
	}
	reps := get("reps").(int)
	if reps < 1 {
		return usage("--reps %d: must be at least 1", reps)
	}
	jobs := get("j").(int)
	if jobs < 1 {
		return usage("-j %d: must be at least 1", jobs)
	}
	limit := get("limit").(int)
	if limit < 0 {
		return usage("--limit %d: must be 0 (all) or more", limit)
	}
	contributor := get("contributor").(string)
	if !contributorPattern.MatchString(contributor) {
		return usage("--contributor %q: want a short name of letters, digits, '_', '.' and '-'", contributor)
	}
	names, err := splitConfigs(get("configs").(string))
	if err != nil {
		return usage("%v", err)
	}
	if inv.common.cacheDir == "" {
		return usage("the cache directory is not set: pass --cache-dir")
	}
	out, err := expandHome(outFlag)
	if err != nil {
		return usage("--out %q: %v", outFlag, err)
	}

	paths, err := selectTasks(inv.fs)
	if err != nil {
		return err
	}
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	if len(paths) == 0 {
		return usage("no task selected")
	}
	cacheDir := cache.Dir(inv.common.cacheDir)
	sources, err := loadSources(paths, cache.OpenValidated(cacheDir))
	if err != nil {
		return err
	}

	dir, err := configsDir(get("configs-dir").(string))
	if err != nil {
		return err
	}
	cfgs := make([]config.Config, len(names))
	for i, name := range names {
		if cfgs[i], err = config.Load(dir, name); err != nil {
			return usage("config %s: %v", name, err)
		}
	}
	if err := preflight(sources, cfgs); err != nil {
		return err
	}

	resultsPath := filepath.Join(out, resultsFile)
	recs, err := results.ReadFile(resultsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	cells, skipped := benchrun.Plan(sources, cfgs, reps, benchrun.Done(recs.Cells), get("redo").(bool))
	header := benchrun.Header(len(cells), len(sources), len(cfgs), reps, skipped)

	if get("dry-run").(bool) {
		width := benchrun.Width(cells)
		for _, c := range cells {
			fmt.Fprintln(inv.stdout, c.Line(width))
		}
		fmt.Fprintln(inv.stdout, header)
		return nil
	}
	if len(cells) == 0 {
		fmt.Fprintln(inv.stdout, header)
		fmt.Fprintln(inv.stdout, "nothing to run: every selected cell has a result; --redo runs them again")
		return nil
	}

	if err := checkPrereqs(ctx, inv.cmd, inv.common); err != nil {
		return err
	}
	manifest, err := prepareManifest(ctx, inv, out, sources, cfgs, contributor, get("allow-mixed").(bool))
	if err != nil {
		return err
	}
	if err := cacheDir.Ensure(); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := results.SaveManifest(out, manifest); err != nil {
		return err
	}

	var verbose io.Writer
	if inv.common.verbose {
		verbose = inv.stderr
	}
	w, err := results.OpenWriter(resultsPath)
	if err != nil {
		return err
	}
	defer w.Close()

	env := benchrun.Env{
		Workspaces: workspace.New(cacheDir, verbose),
		OutDir:     out,
		Bins:       runner.Bins{Claude: inv.common.claude, Orchestrator: inv.common.orchestrator},

		KeepWorkspaces:      get("keep-workspaces").(bool),
		ClaudeCLI:           manifest.ClaudeCLI,
		OrchestratorVersion: manifest.Orchestrator.Version,
		Contributor:         contributor,
	}
	fmt.Fprintln(inv.stdout, header)
	_, err = benchrun.Run(ctx, cells, benchrun.Options{
		Jobs:        jobs,
		MaxCost:     maxCost,
		InitialCost: recs.CostUSD,
		Writer:      w,
		Exec:        newExec(env),
	}, inv.stdout)
	return err
}

// splitConfigs is the --configs list: names, none empty, none twice.
func splitConfigs(list string) ([]string, error) {
	var names []string
	seen := make(map[string]bool)
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return nil, fmt.Errorf("--configs %q: an empty name", list)
		case seen[name]:
			return nil, fmt.Errorf("--configs %q: %s is listed twice", list, name)
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

// loadSources loads the task files and refuses the whole call, naming every task, when one is a
// draft, is invalid, or was not validated with the hash of its file now (SPEC.md §6).
func loadSources(paths []string, store *cache.Validated) ([]benchrun.Source, error) {
	var (
		sources                      []benchrun.Source
		drafts, invalid, unvalidated []string
	)
	for _, p := range paths {
		id := fileID(p)
		hash, err := task.SHA256(p)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("%s (%v)", id, err))
			continue
		}
		t, err := task.Load(p)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("%s (%v)", id, err))
			continue
		}
		id = t.ID
		if t.IsDraft() {
			drafts = append(drafts, id)
			continue
		}
		if err := lang.Validate(t); err != nil {
			invalid = append(invalid, fmt.Sprintf("%s (%v)", id, err))
			continue
		}
		if err := lang.Resolve(&t); err != nil {
			invalid = append(invalid, fmt.Sprintf("%s (%v)", id, err))
			continue
		}
		if !store.IsValid(t.ID, hash) {
			unvalidated = append(unvalidated, id)
			continue
		}
		sources = append(sources, benchrun.Source{Task: t, SHA256: hash})
	}

	var problems []string
	if len(drafts) > 0 {
		problems = append(problems, "drafts, rewrite them and remove `draft`: "+listSome(drafts))
	}
	if len(invalid) > 0 {
		problems = append(problems, "invalid: "+listSome(invalid))
	}
	if len(unvalidated) > 0 {
		problems = append(problems, "not validated with this file hash, run 'orchbench validate' on them: "+listSome(unvalidated))
	}
	if len(problems) > 0 {
		return nil, exitcode.UsageError{Msg: "orchbench run: refusing the tasks; " + strings.Join(problems, "; ")}
	}
	return sources, nil
}

// listSome joins the first few items and counts the rest.
func listSome(items []string) string {
	if len(items) <= maxProblems {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(items[:maxProblems], ", "), len(items)-maxProblems)
}

// configsDir is the directory of the config files: --configs-dir, else the first of
// ./bench/configs and ./configs that holds a common.yaml.
func configsDir(flagValue string) (string, error) {
	hasCommon := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, config.CommonName+".yaml"))
		return err == nil
	}

	if flagValue != "" {
		dir, err := expandHome(flagValue)
		if err != nil {
			return "", exitcode.UsageError{Msg: fmt.Sprintf("orchbench run: --configs-dir %q: %v", flagValue, err)}
		}
		if !hasCommon(dir) {
			return "", exitcode.UsageError{Msg: fmt.Sprintf("orchbench run: --configs-dir %q has no %s.yaml", flagValue, config.CommonName)}
		}
		return dir, nil
	}
	for _, dir := range configDirs {
		if hasCommon(dir) {
			return dir, nil
		}
	}
	return "", exitcode.UsageError{Msg: fmt.Sprintf("orchbench run: no config directory: %s have no %s.yaml; pass --configs-dir",
		strings.Join(configDirs, " and "), config.CommonName)}
}

// preflight renders every config for every task, so that a pairing that could never run fails
// now and not after the cells before it were paid for.
func preflight(sources []benchrun.Source, cfgs []config.Config) error {
	var problems []string
	for _, s := range sources {
		for _, c := range cfgs {
			if err := renders(c, s.Task); err != nil {
				problems = append(problems, fmt.Sprintf("%s with config %s: %v", s.Task.ID, c.Name, err))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return exitcode.UsageError{Msg: "orchbench run: " + strings.Join(problems[:min(len(problems), maxProblems)], "; ") +
		moreNote(len(problems))}
}

func moreNote(n int) string {
	if n <= maxProblems {
		return ""
	}
	return fmt.Sprintf("; and %d more", n-maxProblems)
}

// renders is whether cfg can be run on t: its config or its prompt renders, and the task text is
// one the orchestrator takes as a prompt and not as a flag.
func renders(cfg config.Config, t task.Task) error {
	if cfg.Kind == config.KindClaude {
		_, err := cfg.RenderPrompt(t)
		return err
	}
	if text := strings.TrimSpace(t.Task); strings.HasPrefix(text, "-") {
		return fmt.Errorf("the task text starts with %q, which the orchestrator would read as a flag", text[:1])
	}
	_, _, err := cfg.RenderConfig(t)
	return err
}

// prepareManifest builds the manifest of this invocation and compares it with the one in out. A
// different build, CLI, contributor, config or task file is a usage error unless allowMixed,
// which records what differed. Nothing is written here.
func prepareManifest(ctx context.Context, inv invocation, out string, sources []benchrun.Source, cfgs []config.Config, contributor string, allowMixed bool) (results.Manifest, error) {
	build, cli, err := probeBuild(ctx, inv.common)
	if err != nil {
		return results.Manifest{}, err
	}
	cur := results.Manifest{
		Orchestrator: build,
		ClaudeCLI:    cli,
		Configs:      make(map[string]string, len(cfgs)),
		Tasks:        make(map[string]string, len(sources)),
		Contributor:  contributor,
	}
	for _, c := range cfgs {
		cur.Configs[c.Name] = c.SHA256
	}
	for _, s := range sources {
		cur.Tasks[s.Task.ID] = s.SHA256
	}

	now := time.Now()
	old, found, err := results.LoadManifest(out)
	if err != nil {
		return results.Manifest{}, err
	}
	if !found {
		return cur.New(now), nil
	}

	mismatches := results.Compare(old, cur)
	if len(mismatches) > 0 && !allowMixed {
		return results.Manifest{}, exitcode.UsageError{Msg: fmt.Sprintf("orchbench run: %s: %v; pass --allow-mixed to continue it anyway, or use a new --out",
			out, &results.MismatchError{List: mismatches})}
	}
	return old.Merge(cur, mismatches, now), nil
}

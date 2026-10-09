package action

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"orchestrator/internal/domain"
	"orchestrator/internal/repository"
)

// stepDiff counts what an implement step changed: the working tree now against before (a
// tree written before the call), and against the run's restore point. It also compares the
// files the step changed with the ones its artifact says it changed. It returns the tree
// it diffed against, or nil and "" when git could not tell; a diff is a record, never a
// reason to fail the step. Call it only after the guard has passed the step.
func (d *Dispatcher) stepDiff(ctx context.Context, run *domain.Run, before string, artifact domain.ImplementationArtifact) (*domain.StepDiff, string) {
	after, err := d.Repo.WorktreeTree(ctx)
	if err != nil {
		return nil, ""
	}
	exclude := d.runsExclude()
	files, err := d.Repo.DiffNumstat(ctx, before, after, exclude)
	if err != nil {
		return nil, ""
	}
	diff := &domain.StepDiff{Step: diffStat(files)}
	if run.PreRunRef != "" {
		if total, err := d.Repo.DiffNumstat(ctx, run.PreRunRef, after, exclude); err == nil {
			stat := diffStat(total)
			diff.Run = &stat
		}
	}
	if artifact.ChangedFiles != nil {
		top, err := d.Repo.TopLevel(ctx)
		if err == nil {
			changed := make([]string, len(files))
			for i, f := range files {
				changed[i] = filepath.ToSlash(f.Path)
			}
			claimed := reportedPaths(append(slices.Clone(artifact.ChangedFiles), artifact.TestsAdded...), top, d.Repo.Dir)
			diff.NotReported, diff.ReportedUnchanged = compareReported(changed, claimed)
		}
	}
	return diff, after
}

// runsExclude is the pathspec list that keeps the runs directory, the orchestrator's own
// bookkeeping, out of a diff.
func (d *Dispatcher) runsExclude() []string {
	if d.RunsDir == "" {
		return nil
	}
	return []string{d.RunsDir}
}

func diffStat(files []repository.FileChange) domain.DiffStat {
	var s domain.DiffStat
	for _, f := range files {
		s.Files++
		if f.Added < 0 {
			s.Binary++
			continue
		}
		s.Insertions += f.Added
		s.Deletions += f.Deleted
	}
	return s
}

// reportedPaths turns the paths an artifact names into repository-relative ones, as git
// lists them. The model works in dir and may name a file relative to it or absolute; a
// path that does not land inside the repository is dropped.
func reportedPaths(claims []string, top, dir string) []string {
	// git reports the top level with symlinks resolved; dir is as the operator gave it.
	if t, err := filepath.EvalSymlinks(top); err == nil {
		top = t
	}
	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	rel, err := filepath.Rel(top, resolved)
	if err != nil {
		rel = "."
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range claims {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if filepath.IsAbs(c) {
			r, err := filepath.Rel(dir, c)
			if err != nil {
				continue
			}
			c = r
		}
		p := path.Clean(filepath.ToSlash(filepath.Join(rel, c)))
		if !domain.CleanRelPath(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// compareReported returns the changed files the claims leave out, in git's order, and the
// claimed files that did not change, in the claims' order, each at most
// domain.MaxDiffPaths.
func compareReported(changed, claimed []string) (notReported, unchanged []string) {
	isChanged := map[string]bool{}
	for _, p := range changed {
		isChanged[p] = true
	}
	isClaimed := map[string]bool{}
	for _, p := range claimed {
		isClaimed[p] = true
		if !isChanged[p] && len(unchanged) < domain.MaxDiffPaths {
			unchanged = append(unchanged, p)
		}
	}
	for _, p := range changed {
		if !isClaimed[p] && len(notReported) < domain.MaxDiffPaths && domain.CleanRelPath(p) {
			notReported = append(notReported, p)
		}
	}
	return notReported, unchanged
}

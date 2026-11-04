// Package projectctx is the Context Manager (specs/JEV integration/SPEC.md §5, TASKS.md
// T08): it builds the compact ProjectContext sent to the decision provider when a task
// needs repository context. It sends paths, one-line summaries, rules, doc excerpts and
// metadata, and the clipped text of only the files the request names by path - never
// whole files otherwise - and nothing it collects passes through without the deny list,
// since the result leaves the machine.
package projectctx

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kasorse/orch/internal/domain"
)

// Repo is the slice of internal/repository the manager needs. Listing goes through git
// so .gitignore is honoured without reimplementing it.
type Repo interface {
	ListFiles(ctx context.Context) ([]string, error)
	Branch(ctx context.Context) (string, error)
	ChangedFiles(ctx context.Context) ([]string, error)
}

// Options bounds what is collected (config: context.*).
type Options struct {
	// MaxFiles caps the relevant files; MaxBytes caps the JSON-encoded context.
	MaxFiles int
	MaxBytes int
	// ExtraDeny adds glob patterns to the built-in secret deny list.
	ExtraDeny []string
}

type Manager struct {
	repo Repo
	root string
	opts Options
	deny denyList
}

// NewManager builds a manager for the repository whose working tree is root.
func NewManager(repo Repo, root string, opts Options) *Manager {
	return &Manager{repo: repo, root: root, opts: opts, deny: newDenyList(opts.ExtraDeny)}
}

// Build collects the project context for a request. request is the user's original
// text and requestEN its English translation (empty when none was made); both feed the
// search, see queryTokens. An error means no context at all; the caller continues with
// context_limited rather than failing the run (SPEC.md §14).
func (m *Manager) Build(ctx context.Context, request, requestEN string) (*domain.ProjectContext, error) {
	listed, err := m.repo.ListFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repository files: %w", err)
	}
	files := make([]string, 0, len(listed))
	for _, f := range listed {
		if !m.deny.denied(f) {
			files = append(files, f)
		}
	}

	q := newQueryTokens(request, requestEN)
	md := projectMetadata(m.root, files)
	pc := &domain.ProjectContext{
		Language:     md.language,
		ProjectType:  md.projectType,
		Module:       md.module,
		Dependencies: md.dependencies,
		TestInfo:     testInfo(m.root, files),
		Rules:        projectRules(m.root, files),
		Docs:         docExcerpts(m.root, contextDocFiles(files), q, maxDocExcerpts),
	}

	// One more than fits, so the budget can tell that something was left out.
	pc.RelevantFiles, err = relevantFiles(ctx, m.root, files, q, namedFiles(request, files), m.opts.MaxFiles+1)
	if err != nil {
		return nil, err
	}

	pc.Git, err = m.gitContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read git state: %w", err)
	}

	applyBudget(pc, m.opts.MaxFiles, m.opts.MaxBytes)
	return pc, nil
}

// applyBudget enforces max_files and max_bytes, dropping the least valuable content
// first: doc excerpts, then relevant files from the least relevant end, then rules from
// the end, then dependencies and changed paths. Truncated records that anything was
// dropped, so the provider knows the picture is incomplete.
func applyBudget(pc *domain.ProjectContext, maxFiles, maxBytes int) {
	if maxFiles >= 0 && len(pc.RelevantFiles) > maxFiles {
		pc.RelevantFiles = pc.RelevantFiles[:maxFiles]
		pc.Truncated = true
	}
	if maxBytes <= 0 {
		return
	}
	shrinkers := []func() bool{
		func() bool { return dropLast(&pc.Docs) },
		func() bool { return dropLast(&pc.RelevantFiles) },
		func() bool { return dropLast(&pc.Rules) },
		func() bool { return dropLast(&pc.Dependencies) },
		func() bool { return dropLast(&pc.Git.ChangedFiles) },
	}
	for _, shrink := range shrinkers {
		for encodedSize(pc) > maxBytes && shrink() {
			pc.Truncated = true
		}
	}
}

func dropLast[T any](s *[]T) bool {
	if len(*s) == 0 {
		return false
	}
	*s = (*s)[:len(*s)-1]
	if len(*s) == 0 {
		*s = nil
	}
	return true
}

func encodedSize(pc *domain.ProjectContext) int {
	data, err := json.Marshal(pc)
	if err != nil {
		return 0
	}
	return len(data)
}

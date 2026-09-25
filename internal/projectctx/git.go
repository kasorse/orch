package projectctx

import (
	"context"

	"orchestrator/internal/domain"
)

const maxChangedFiles = 50

// gitContext reports branch and uncommitted paths. Changed paths go through the same
// deny list as everything else: the name of a modified .env is itself a hint not to
// send.
func (m *Manager) gitContext(ctx context.Context) (domain.GitContext, error) {
	branch, err := m.repo.Branch(ctx)
	if err != nil {
		return domain.GitContext{}, err
	}
	changed, err := m.repo.ChangedFiles(ctx)
	if err != nil {
		return domain.GitContext{}, err
	}
	g := domain.GitContext{Branch: branch, Dirty: len(changed) > 0}
	for _, f := range changed {
		if m.deny.denied(f) {
			continue
		}
		if len(g.ChangedFiles) == maxChangedFiles {
			break
		}
		g.ChangedFiles = append(g.ChangedFiles, f)
	}
	return g, nil
}

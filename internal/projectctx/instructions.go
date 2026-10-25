package projectctx

import (
	"context"
	"fmt"

	"orchestrator/internal/domain"
)

// maxInstructionFiles bounds the files clarify.docs may make Docs read.
const maxInstructionFiles = 200

// DocsOptions configures Docs (config: clarify.docs, clarify.max_bytes, context.extra_deny).
type DocsOptions struct {
	// Repo lists the files through git, so .gitignore is honoured.
	Repo Repo
	// Globs select the Markdown files, matched against repository-relative paths; "**"
	// matches any number of directories.
	Globs []string
	// MaxBytes caps the rules and excerpts together.
	MaxBytes int
	// ExtraDeny adds glob patterns to the built-in secret deny list.
	ExtraDeny []string
}

// Docs selects the instructions the clarify call reads (specs/clarify, Approach §3): the
// rules of the root instruction files and the Markdown sections of the files opts.Globs
// match, ranked by how well they fit the request, cut to opts.MaxBytes. Only files git
// lists are considered, vendored paths are dropped, the deny list is applied before
// anything is read, and binaries, large files and symlinks are never read. When the budget
// is smaller than everything, excerpts go first from the lowest rank (the best-ranked one
// stays), then rules from the end, and Truncated is set.
func Docs(ctx context.Context, root string, opts DocsOptions, request, requestEN string) (domain.Instructions, error) {
	listed, err := opts.Repo.ListFiles(ctx)
	if err != nil {
		return domain.Instructions{}, fmt.Errorf("list repository files: %w", err)
	}
	deny := newDenyList(opts.ExtraDeny)
	var files, docs []string
	for _, f := range listed {
		if deny.denied(f) {
			continue
		}
		files = append(files, f)
		if isVendored(f) || len(docs) >= maxInstructionFiles {
			continue
		}
		for _, g := range opts.Globs {
			if domain.MatchGlob(g, f) {
				docs = append(docs, f)
				break
			}
		}
	}

	ins := domain.Instructions{
		Rules:    projectRules(root, files),
		Excerpts: docExcerpts(root, docs, newQueryTokens(request, requestEN), 0),
	}
	if opts.MaxBytes <= 0 {
		return ins, nil
	}
	for instructionsSize(ins) > opts.MaxBytes && len(ins.Excerpts) > 1 {
		ins.Excerpts = ins.Excerpts[:len(ins.Excerpts)-1]
		ins.Truncated = true
	}
	for instructionsSize(ins) > opts.MaxBytes && len(ins.Rules) > 0 {
		ins.Rules = ins.Rules[:len(ins.Rules)-1]
		ins.Truncated = true
	}
	if len(ins.Rules) == 0 {
		ins.Rules = nil
	}
	return ins, nil
}

func instructionsSize(ins domain.Instructions) int {
	n := 0
	for _, r := range ins.Rules {
		n += len(r)
	}
	for _, e := range ins.Excerpts {
		n += len(e.Path) + len(e.Section) + len(e.Text)
	}
	return n
}

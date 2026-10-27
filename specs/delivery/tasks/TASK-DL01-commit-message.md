---
id: DL01
repo: orche
depends_on: []
jira: none
---
# DL01 — Commit message: Conventional Commit type and the decision trailer

**Size** S

## Goal

The commit header gets the model's `commit_type` from a closed list, and the body gets the
`Orchestrator-Decision` trailer (`SPEC.md` §4).

## Files

- `internal/domain/artifact.go`: `ImplementationArtifact.CommitType string` (`json:"commit_type,omitempty"`);
  `domain.CommitTypes` (feat, fix, refactor, perf, test, docs, build, ci, chore) and
  `ValidCommitType(s) (string, bool)` (trim, lower-case, closed list). An unknown value never fails
  the artifact.
- `internal/action/prompts.go`: the `commit_type` rule next to `implementSummaryShortRule`, the
  field in implement's JSON shape; `go test ./internal/action -update`, read the golden diff.
- `internal/app/commit.go` (`commitOffer`): the type from the same implementation as
  `summary_short`; header `<type>: [KEY ]<summary_short>` or today's form without a type; the
  72-rune cut over the whole header; `Orchestrator-Decision: <complexity|manual>/<stages>` after
  `Orchestrator-Run`, before `Orchestrator-Story`. The story child's commit (`--story-task`) gets the
  same.

## Acceptance

- Table test of `commitOffer`: type × key × none, a type with spaces/capitals, an unknown type, a
  header that crosses 72 runes with the type, a manual decision's trailer, a story trailer order.
- The `e` answer still replaces the whole header.
- Golden implement prompts updated; no other prompt changes.

## Gates

`scripts/check.sh`

## Notes

Do not edit anything under `specs/delivery/` (story run writes its README and As built). Leave
MVP.md, CLAUDE.md, TODO.md and README.md to DL12.

## As built (2026-10-27)

Run `8ccfefc3-a789-4ca7-bd0d-dd1d4efc23d1` in orche: completed, 0 fix iterations, tests passed, no review.
Branch `story/delivery/DL01` on `spec/delivery` @ 6d3f006, commit 8d1ffc9.
Cost $0.72. Implementation summary: Add commit_type to commit header and Orchestrator-Decision trailer.
Conflicts with spec/delivery: none.

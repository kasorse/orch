# S06 — Integration scenarios

**Size** M (1 d) · **Depends on** S03, S04, S05 · **Blocks** S07

## Goal

End-to-end proof, with the fake executables, fake Jev and fake Jira (`test/integration`; no
network), that `--stage` behaves as `SPEC.md` §16 says and nothing else moved.

## Scenarios

Numbered after the last existing scenario (`20a`…).

- a. `run --stage review "task"` on a dirty tree: one provider call (`ACTION: review`); fake
  Jev and the translator get zero requests; `decision.json` has `source: manual`; no
  `project_context.json`.
- b. `--stage review` on a clean tree with no changes: fails with `nothing to review`; zero
  provider calls.
- c. `--stage review --stage testing` (flag order reversed): testing then review. Test failure
  → run fails. Review failure → completes with the verdict, no implement.
- d. `--stage development` on a dirty tree: refused as today; after the tree is cleaned,
  `resume` runs it.
- e. `--stage development --stage testing --stage review`: the existing fix loop, bounded by
  `max_fix_iterations`.
- f. `--stage planning "task"`: the answer is the plan; the gap warning for analysis on stderr.
- g. `--stage analyze --stage analysis`: one step.
- h. `--stage review --jira KEY`: one Jira GET, no decision call.
- i. `resume` of a manual run that failed at review: continues at review; no decision
  provider or translator constructed (fake request counts stay 0).
- j. A missing executable for a selected action fails before any call (fail-fast chain);
  `--stage` with `validate` is a usage error.
- k. `--mode plan`: shows stages and authorities, stops, calls nothing.
- l. Without `--stage`: scenarios 1–19 pass unchanged.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03. `test/integration/scenario20_stage_select_test.go`, scenarios 20a–20k as
listed; l is the existing scenarios 1–19 passing unchanged. 20b found the S03 problem above.

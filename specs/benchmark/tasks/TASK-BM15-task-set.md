# BM15 — The 30 tasks

**Size** L (about a week of the owner's time, in parallel with BM09–BM14) · **TODO** B1 ·
**Depends on** BM06, BM08 · **Blocks** BM16

## Goal

B1's "done when": 30 tasks, each reviewed by hand and green on `orchbench validate`
(`SPEC.md` §5.2–§5.4, §6). Owner's work, with the assistant drafting and checking.

## Composition (§5.2)

| dimension | target |
|---|---|
| languages | Go, Python, TypeScript (Node); none below 6 tasks |
| `question` | ≥ 6 (20%), with `answer_checks` and `reference_answer` |
| Russian | about 6 tasks as en/ru `pair`s of English ones |
| categories | bugfix, feature, refactor all present; sizes spread so simple, medium and complex are all plausible |
| sources | public tasks in `bench/tasks/`; tasks from private repositories in the owner's private directory, never committed (Q1) |

## Steps

1. The owner names the repositories (Q2): own Go repositories, 2–3 open-source Python and
   TypeScript ones.
2. `orchbench mine` on each; the assistant pre-screens drafts (discards the obvious misfits,
   proposes a rewritten task text and `expected_stages`); the owner decides and edits.
3. Every feature task names the interface its oracle calls (§5.4 step 2).
4. Question tasks written from code the owner knows, with a reference answer and checks.
5. `orchbench validate` until all 30 are green; record the composition in
   `bench/tasks/README.md` (counts only for private tasks).

## Acceptance

- `orchbench validate` green on all 30; the composition table met; every task text read by
  the owner.

## Gates

None beyond `validate` (no code in this task).

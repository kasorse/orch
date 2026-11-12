# Revise: a new requirement for a merge request already opened

Status: **completed** (6 of 6 tasks done, 2026-11-11; spec 2026-11-11, owner's answers R1–R9, defaults
A1–A12 confirmed 2026-11-11). Left to the operator: the live check on Jira Cloud and gitlab.com,
and the four `bench/` tests that skip under bwrap (nothing under `bench/` changed).

Changed after the build (owner, 2026-11-12), MVP.md §54.1, §54.6, §54.8 hold the result: the target
may be the ticket's `/browse/<KEY>` URL; an update no longer puts its worktree on the merge request's
branch (which refused a branch checked out in the operator's tree) but on `<branch>-revise-<run-id[:8]>` (a name the repository's branch hooks accept) from
the operator's local copy of it (its unpushed commits included, a diverged one refused), and at the
end fast-forwards that copy (`git merge --ff-only` where it is checked out) and pushes it (R10, R11);
review is forced on like development, so R5's review of the whole merge request always runs (R12).

| done | task | title | depends on |
|---|---|---|---|
| [x] | RV01 | `internal/gitlab`: the read-only client, `gitlab.api_token_env`, the scope check | — |
| [x] | RV02 | `internal/jira`: web links, read and write the description | — |
| [x] | RV03 | `orchestrator revise`: the target, its resolution, `task.source: revise` | RV01, RV02 |
| [x] | RV04 | The worktree on the existing branch, the prompts, the review of the whole MR | RV03 |
| [x] | RV05 | Delivery: the push that updates the MR, new mode, the description, resume | RV04 |
| [x] | RV06 | Integration scenarios and the changed invariants | RV05 |

## Ground rules

- `SPEC.md` is the design; MVP.md gets a new section (§54) written by the tasks as they build
  their parts, CLAUDE.md the changed invariants (a GitLab client exists, read-only; a push to an
  existing branch exists, fast-forward only, for `revise`).
- With no `revise` and no `gitlab.api_token_env`, nothing changes: every existing test passes
  unchanged, `run.json` without the new fields loads and resumes as before.
- `internal/gitlab` imports only `domain`, `apperr` and the standard library, and sends only GET
  (both enforced by tests). `internal/workflow` and `internal/policy` learn nothing of `revise`.
- Each task: its MVP.md/CLAUDE.md/README.md/`orchestrator.example.yaml` part, a tick here and in
  `TODO.md`, `## As built` in its task file. The gate is `scripts/check.sh`.
- The live check (a real ticket, a real merge request on gitlab.com) is the operator's gate,
  recorded here in `Status:`.

# WT01 — config: `execution.isolation`, `worktree.setup`, `run --inplace`, clean-start rule per mode

**Size** S (1 d) · **Depends on** — · **Blocks** WT04

## Goal

The two keys and the flag exist, are validated strictly, and the clean-start rule knows the
mode (`SPEC.md` §6). Nothing runs in a worktree yet.

## Files

- `internal/config/config.go`: `ExecutionConfig.Isolation` (`inplace` | `worktree`, default
  `worktree`, O1) in `ExecutionConfig.UnmarshalYAML`; `WorktreeConfig{Setup string}` under the top-level
  `worktree:` key; `Config.WorktreeRun(wf domain.Workflow) bool` — isolation is `worktree` and
  the workflow contains `implement` (O3)
- `internal/config/validator.go`: an unknown `isolation` value; `worktree.setup` with `inplace`
  (it would never run)
- `internal/app/app.go`: `run --inplace` in `parseArgs` and the usage line (not on `resume`);
  `checkWorktree` takes the mode: in a worktree run a dirty tree is the §6 warning (`The run
  starts from HEAD <short>; <n> uncommitted changes in your tree are not part of it (commit them
  first, or run with --inplace).`), never a refusal; `inplace` unchanged
- `orchestrator.example.yaml`: both keys, commented

## Acceptance

- Unit: default isolation; `isolation: sideways` and `worktree.setup` under `inplace` are config
  errors naming the key; an unknown key under `worktree:` is refused by the strict parse.
- `checkWorktree`: worktree + implement + dirty → warning; inplace + implement + dirty → today's
  refusal; a read-only workflow never refuses.
- The org floor `security.require_clean_worktree: true` does not refuse a worktree run.
- `--inplace` with `isolation: worktree` + implement + dirty tree → today's refusal; `resume
  --inplace` is a usage error.

## Gates

`scripts/check.sh`

# Spec: Run chosen stages (`run --stage`)

Status: **implemented** (2026-10-03). The design questions are answered (§13) and the defaults in §14 were kept. MVP.md §40 is the normative home
Scope: a new `--stage` flag on `orchestrator run` that lets the operator name the stages to run
and skips the whole decision layer. Workflow construction, the transition table, static routing,
authorities, the guard, resume and what is trusted (exit codes + git diff) stay the same.
Extends MVP.md §8–§9 (decision flow), §37 (resume), §38 (permissions), §39 (Jira).

---

## 1. Problem

Every `orchestrator run` asks a decision provider which stages the task needs. An operator who
already knows — "I changed the code by hand, I only want a review", "just run the tests",
"plan this, don't implement" — cannot say so. They pay for translation, a pre-check,
project-context collection and classification, and may get a workflow that is not the one they
wanted.

## 2. What already works

- `workflow.Build(domain.StageSelection)` turns any subset of stages into a workflow, in the
  fixed `StageOrder`, ignoring the order the caller built the selection in
  (`internal/workflow/builder.go`).
- `workflow.Next` is already subset-safe: a test failure retries `implement` only if the
  workflow contains it, otherwise the run fails; a review failure retries `implement` only if
  present, otherwise the run completes with the verdict (`internal/workflow/transitions.go`).
- `workflow.Verify` (resume) rebuilds the workflow from `run.Decision.Stages`, so a run whose
  decision carries the chosen stages resumes with no change.
- `executeWorkflow` is the shared tail of `run` and `resume`; it needs a `run` with `Decision`
  and `Workflow` set.

So the feature is: **build a `domain.Decision` from flags instead of from the decision layer**,
then enter the existing path at `workflow.Build`.

## 3. Principles

1. **Go still decides the order and the transitions.** The flags choose *which* stages run,
   never their order or the retry rules. `--stage review --stage testing` runs testing, then
   review.
2. **No decision layer.** With `--stage`: no translator, no decision provider, no Context Manager,
   no Jev or LLM call, no project-context collection (§6). Nothing in the fail-fast chain
   changes.
3. **Authority is untouched.** Each stage runs with `domain.AuthorityFor(action)` exactly as
   before. The flag cannot widen or narrow it (MVP.md §38).
4. **Same invariants as a decided run.** The clean-worktree rule, the guard, the baseline, the
   limits, the lock, the approval and mode prompt, and the final answer all apply unchanged.
5. **Resume continues, never re-decides.** A `--stage` run is resumable like any other; the
   persisted decision *is* the operator's choice.

## 4. CLI

```
orchestrator [--config path] [--mode …] [--verbose] \
    run --stage <name> [--stage <name> …] "<task>"
orchestrator run --stage review --jira PMC-2789 ["notes"]
```

- `--stage` is repeatable; `--stage=X` and `--stage X` both work, in any position, like
  `--mode`. Each occurrence takes one value. A comma-separated value (`--stage a,b`) is **not**
  accepted — one stage per flag, as requested.
- It is valid only with `run`. `validate`, `resume` and `version` with `--stage` fail with
  `--stage is only valid with run`, as `--jira` does.
- A task is **required**, exactly as today: `run --stage review "<task>"`. With `--jira` the
  ticket (plus optional notes) is the task.
- `--stage` given once or more switches the run to manual selection (§6). Without it, nothing
  changes.

### 4.1 Names

Canonical names are the six stages; the action names are accepted as aliases. Matching is
case-insensitive after trimming.

| canonical (stage) | alias (action) | runs action |
|---|---|---|
| `research` | — | research |
| `analysis` | `analyze` | analyze |
| `planning` | `plan` | plan |
| `development` | `implement` | implement |
| `testing` | `test` | test |
| `review` | — | review |

An unknown name is a usage error that lists the valid names (`unknown stage "reveiw"; valid:
research, analysis, planning, development, testing, review (aliases: analyze, plan, implement,
test)`). The same stage given twice — by either spelling — is collapsed, not an error, and the
console shows it once.

Parsing lives in `domain` (`ParseStage` already exists, `internal/domain/stage.go:49`; it gains
the aliases, or a thin wrapper does — see §14 D3). Persisted artifacts always use the
canonical name.

## 5. The decision for a manual selection

`run.Decision` is built by Go from the flags, not by a provider:

```
Decision{
  Source:       "manual"          // new DecisionSource; "user" stays the provider-failure fallback
  ContextDepth: {Selected: none} // nothing was collected
  Complexity:   {Selected: ""}   // not assessed — see §14 D1
  Stages:       one StageOutcome per stage in StageOrder:
                  Enabled = in the flags,
                  Reason  = "selected with --stage" / "not selected",
                  Probability and Threshold absent (not 0 — they were never asked)
}
```

`policy.Policy.force` (`policy.stages.force[complexity]`) is **not applied**: with no complexity
there is nothing to force, and the operator asked for exactly these stages. If no stage is given
the flag does not exist, so "nothing enabled → analysis" never fires.

`workflow.Build(decision.Stages)` is then called as for any run. `decision.json` is written with
`source: manual`, the request, `workflow`, and zeroed latencies; `provider`/`model` are empty,
`translation` is `{detected: "", translated: false}`, `context_limited` is false. `project_context.json`
is not written.

## 6. Flow with `--stage`

```
[--jira] → fetch the issue, render it as the task     (unchanged)
parse --stage values → StageSelection                  (new, before the fail-fast chain finishes? see below)
        → Decision{source: manual}                     (§5)
        → workflow.Build                               (unchanged)
        → persist decision / workflow / run            (unchanged)
        → checkWorktree → Decided → executeWorkflow    (unchanged)
```

Order inside `runTask`:

1. `parseArgs` validates the `--stage` values (unknown name → `ConfigError`, exit code as other
   usage errors). This happens before anything else, including config loading.
2. `loadAndValidateConfig` runs in full, as today. `ValidateDependencies` already checks every
   action's executable, so a missing Codex for `review` fails before anything is called. The
   decision provider's and translator's executables are **still checked** (the config declares
   them, and the same config must stay valid for plain `run` and `resume`); they are simply not
   called. See §14 D4 if you would rather skip those checks.
3. The Jira fetch (`--jira`) runs as today, after the chain and before `store.NewRun`.
4. `newDecisionProvider`, `registry.Resolve(cfg.Translation.Provider)`, the `classification.Service`
   and `svc.Classify` are **not reached**. The decision/translation/context code is skipped, not
   bypassed with a flag inside it.
5. The rest (`workflow.Build` onward) is the existing code.

To keep the shared tail shared (CLAUDE.md: "keep it that way"), the split is a small function in
`app` that returns the `domain.Decision` — from `svc.Classify` or from the flags — and the persist
and `executeWorkflow` code is not duplicated.

## 7. Console output

The header is the same. Instead of `Classifying task…` and the decision box, the console shows
the chosen stages once, before the approval prompt:

```
Stages (manual): testing → review
```

followed by the existing authority line and mode prompt. Gap warnings (§8) print right after it,
to stderr, one line each. `--verbose` changes nothing here (no provider is streaming).

## 8. Missing inputs: warn on gaps

A stage normally consumes the output of the stages before it (analysis → planning →
development → testing → review). The operator may skip any of them, so a gap is a **warning,
never a refusal**, and never changes what runs.

A gap exists when a selected stage's canonical predecessor chain has an unselected stage
immediately before it in `StageOrder`, **and the stage actually reads that predecessor's
artifact**. The table is code (a small function in `workflow`, depending only on `domain`), not
config:

| selected stage | warns when not selected | warning text |
|---|---|---|
| planning | analysis | `planning runs without analysis; the plan will rely on the task text alone` |
| development | planning (and analysis) | `development runs without a plan; it works from the task text alone` |
| testing | development | none — testing runs the test command on the current tree; this is the "I changed code by hand" case |
| review | development | none — review reads the real `git diff`, not an implementation summary |

So the two main use cases (`--stage review`, `--stage testing`) are silent. Exact wording and
the table are finalised in the implementation; they change no behaviour.

Review with an empty diff is not a gap but an error: §9.

## 9. Review on a hand-changed tree

`review` keeps its current input: `Repo.Diff` — the working tree against `HEAD`, which is exactly
the "I already changed code" case.

- **Empty diff:** a review of nothing is a false pass. If the diff is empty the review step
  **fails the run before calling the model**, with: `nothing to review: the working tree has no
  changes against HEAD`. This also applies to a decided run, where today the model is asked to
  review an empty diff; the check lives in `action.review`, so it is one rule for both.
  (Impact on existing decided runs: review after an `implement` that changed nothing now fails
  instead of passing/failing on prose. Confirm in §14 D5.)
- **No `--base <ref>`:** reviewing committed work is out of scope for this feature.
- A review failure with no `implement` selected completes the run with the verdict
  (existing rule, `StateDone`, "review found issues"); the exit code and output are the existing
  ones for that case.

## 10. Worktree rule

Unchanged (answered Q: keep the rule).

- A selection with `development` needs a clean worktree unless `security.require_clean_worktree:
  false`; otherwise `checkWorktree` refuses before any prompt, as today. The refused run keeps
  its decision and workflow and is resumable once the tree is clean (resume spec G10).
- A selection without `development` never needs a clean tree; a dirty tree only prints the
  existing warning. `--stage review` on a hand-edited tree therefore just works.
- `pre_run_ref`, the baseline and the guard are recorded and compared as today. Test commands
  still run under the guard.

## 11. Retries and limits

Unchanged (answered Q: keep existing rules).

| selection | test fails | review fails |
|---|---|---|
| `testing` alone | run fails ("tests failed") | — |
| `review` alone | — | run completes with the verdict |
| `testing`+`review`, no development | run fails | n/a (review not reached) |
| includes `development` | back to implement, bounded | back to implement, bounded |

`max_steps` and `max_fix_iterations` bound loops. Nothing new is added.

Without `implement`, the last read-only step (research/analyze/plan) writes the final `answer`,
as today. `review` and `test` do not.

## 12. Persistence and resume

- `run.json`: `decision.source = "manual"`; `task.source` as usual for `--jira`. The decision
  validation on load (`domain`/`persistence`) must accept `manual` and a missing complexity (§14
  D1). `run.json` records nothing else new.
- `decision.json`: as §5.
- `resume <run-id>` works unchanged: `workflow.Verify` rebuilds from `decision.stages` and gets
  the same workflow; resume builds no decision provider and no translator, as ever.
- Old runs (source `jev`/`llm`/`user`) and old `decision.json` files load as before.

## 13. Decisions taken (from the design questions)

| # | Question | Decision |
|---|---|---|
| Q1 | Is a task required with `--stage`? | **Required**; `--jira` works as the task. |
| Q2 | Which decision-layer parts run? | **None** — no translation, pre-check, project context, classification. |
| Q3 | Stage names | Canonical stage names, action names as aliases. |
| Q4 | Retry loop | Existing rules; retries only if `implement` is selected. |
| Q5 | Order of several flags | Fixed canonical order; duplicates collapsed. |
| Q6 | Review scope | Working tree vs `HEAD`; no `--base`. |
| Q7 | Dirty tree with `development` | Keep the clean-worktree rule. |
| Q8 | Missing earlier-stage inputs | **Warn** on gaps (§8), still run. |

## 14. Defaults (confirmed: all kept as proposed)

- **D1 — Complexity of a manual run.** Nothing assesses it and nothing downstream reads it
  (only the decision box and `decision.json` show it). Default: leave it empty and let the
  console/trace print `—` for `manual`. Alternative: add a `ComplexityNotAssessed` value.
  Empty needs the loaders that currently require a valid complexity to relax for `manual` only.
- **D2 — `DecisionSource` value.** Default: new `manual`. Alternative: reuse `user` (already
  means "the operator picked after a provider failure"); the trace would then not distinguish
  "picked up front" from "provider failed".
- **D3 — Alias parsing site.** Default: aliases in a new `domain.ParseStageFlag`, leaving
  `ParseStage` (used to read persisted data) strict on canonical names.
- **D4 — Dependency checks.** Default: the decision provider's and translator's executables are
  still validated (same config for all commands). Alternative: skip them when `--stage` is
  given, so a machine with only `claude` and no Jev key can run `--stage review`. Note the Jev
  API key check is in the same chain; skipping it would be the only way to run offline without
  the key set.
- **D5 — Empty review diff.** Default: fail in `action.review` for all runs (§9). Alternative:
  only for `--stage` runs.
- **D6 — `--stage` with `--mode plan`.** Default: works; plan mode shows the stage list and the
  authorities, approves and stops, as for a decided run.

## 15. Security

No new boundary is crossed: no new network call (fewer, in fact), no new input to a prompt, no
authority change, no new path read or written. The flag value is parsed against a closed set;
nothing from it reaches a shell, a path or a prompt. Because the decision providers are not
constructed, their credentials are read/passed only if D4 keeps the dependency check (reading
the variable's presence, as today).

## 16. Tests

Unit:
- `parseArgs`: `--stage x` / `--stage=x`, repeated, any position, an aliased value, a duplicate
  (collapsed), an unknown name (error naming the valid ones), a missing value, `--stage` with
  `validate`/`resume`/`version` (error), `--stage` and `--jira` together.
- `domain`: alias table; `ParseStage` stays strict.
- Selection → `Decision` → `workflow.Build`: flag order irrelevant; each single stage; all six;
  `Verify` accepts the result.
- Gap warnings: the §8 table, including the silent `testing`/`review` cases.
- `action.review`: empty diff fails before the model call (and the fake-provider call count
  stays 0).

Integration (`test/integration`, fakes only, no network):
- `run --stage review "task"` on a dirty tree: exactly one provider call (`ACTION: review`), the
  fake Jev and fake translator receive zero requests, `decision.json` has `source: manual`.
- `run --stage testing --stage review`: runs testing then review in that order regardless of flag
  order; test failure → run fails; review failure → completes with the verdict, no implement.
- `run --stage development` on a dirty tree: refused as today; the run is resumable after the
  tree is cleaned.
- `run --stage development --stage testing --stage review`: the existing fix loop, bounded.
- `run --stage planning "task"`: the answer is the plan; a gap warning for the missing analysis.
- `run --stage review --jira KEY`: one Jira GET, no decision call.
- `resume` of a manual run that failed at review: continues at review; no decision provider
  constructed.
- Missing executable for a selected action fails before any call (fail-fast chain), including a
  missing Codex for `--stage review`.

Golden files: `decision.json` for a manual run; the `Stages (manual): …` console line.

## 17. Documentation

MVP.md: new section after §39 (CLI, decision for a manual selection, gap warnings), plus §8–§9 and
§27 amended to mention the flag. CLAUDE.md: add `--stage` to the decision-flow diagram and the
"Decision providers return probabilities; Go decides" invariant (a manual selection is the one
case where the operator, not a provider, supplies the stage set — still never an LLM, still never
the order). README and the example config comment: usage line. `orchestrator` usage string:
`run --stage <name> […] "<task>"`.

## 18. Out of scope

- A stage order or repeat count chosen by the operator.
- `--skip-stage` / "everything but X".
- `--base <ref>` and reviewing committed history.
- Per-stage provider or model flags (routing stays in config).
- Mixing `--stage` with a partial decision (e.g. stage list from flags, complexity from a
  provider).
- A config key for default stages.

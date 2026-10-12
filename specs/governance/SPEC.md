# Spec: Governance — control for teams (phase 4)

Status: **specified** (2026-10-12). The author's defaults are in §11; the owner's decisions so
far are in §10. Nothing is built yet. Once GV08 is done, MVP.md (a new §45) becomes the
normative home and this file stays the design record.

Scope: TODO.md phase 4, items G1 (path rules), G2 (organization config layer), G3 (budgets) and
G5 (threshold calibration). G4 (audit export) is dropped. §9 explains why.

The spec touches these MVP.md sections:
- §7: fail fast;
- §9: policy;
- §12 and §25: limits;
- §31.2: providers return probabilities;
- §37: resume;
- §38.3: guard;
- §40: `--stage`;
- §42: commit;
- §43: summary.

---

## 1. Problem

The orchestrator already guards what a model may touch (authority, protected paths, the guard,
the secret scan). A team lead or a security officer still cannot express four things.
master was at `ca1465e` when each gap below was checked:

1. **"Changes here need more care."** `security.protected_paths` forbids a path outright. No
   rule can say that a change under `migrations/` needs review and testing in the workflow, or
   that it must never be committed without a person's yes. The workflow comes from
   probabilities, and a task the decision provider calls simple can still touch a migration.
2. **"Whatever a repository says, these rules hold."** All configuration lives in the
   repository's own `orchestrator.yaml` (`app.defaultConfigPath`). Anyone who can edit it can
   turn off the sandbox, the clean-worktree rule or the secret scan.
3. **"One run costs at most this much."** `max_steps` and `max_fix_iterations` bound loops, not
   money. A run that ends up on an expensive model with extra reviewers and verify calls has no
   ceiling. `domain.Usage` even says "nothing decides anything from these numbers".
4. **"Do the thresholds suit this repository?"** The stage thresholds in the example were raised
   by hand after the first benchmark round (MVP.md §9.1). No tool reads the run history and
   says which stage pays off at which probability.

## 2. What already works

- **Path patterns.** `domain.MatchProtected` and `domain.ValidateProtectedPattern`
  (`internal/domain/protected.go`) match repository-relative paths, with `dir/**` and
  `path.Match` globs, at any depth.
- **Changed paths.** `Repository.DiffNumstat(from, to, exclude)` lists every changed path
  between two trees. `Dispatcher.stepDiff` already calls it twice: against the step's
  before-tree and against `pre_run_ref` (`internal/action/diffstat.go`).
- **Usage per call.** The claude adapter parses `total_cost_usd` and per-model `costUSD` from
  stream-json (`internal/provider/claude/stream.go`). Each execution's `usage` is persisted,
  and `domain.ComputeOutcome` sums the executions and the decision calls. Codex and gemini
  report no usage.
- **A limit that stops a run.** `max_steps` is checked before each step. It sets
  `RunStopped`, `failure_reason`, and returns `LimitExceededError`
  (`internal/workflow/engine.go:144`). A stopped run with executions is resumable from its
  entry state (MVP.md §37.3).
- **Run-start options.** `domain.RunOptions` (`options.review_fix_medium`) records a config
  value at run start, so a resume follows the rule the run started with.
- **The commit offer.** `app.offerCommit` (`internal/app/commit.go`) runs only for a completed
  run with implement. It checks the baseline first, asks through `interaction.AskCommit`, and
  under `auto_commit: true` skips itself with a reason on any warning.
- **Strict config.** `config.Load` decodes a version-2 file strictly. A key the validator
  rejects with its own message must be declared in the schema.
- **Reports over persisted runs.** `orchestrator decisions report` and `runs report`
  (`internal/report`) read `decision.json` and `run.json` only. They call no provider.
- **Decision traces.** `decision.json` keeps each stage's probability, threshold, forced flag
  and reason, the decision model, and the model routes (`DecisionTrace.Models`).
- **The run id exists before the workflow.** `store.NewRun` (`internal/app/app.go:510`) runs
  before `workflow.Build` (`:550`).

## 3. Principles

1. **The diff is the truth, not the plan.** Path rules match the files git says changed, never
   `affected_files` or `changed_files` from a model.
2. **No workflow rebuilt mid-run.** A path rule that needs a stage the workflow lacks stops the
   run with a message. It never inserts the stage. The same decision always gives the same
   workflow (MVP.md §9).
3. **Go decides, from config and the persisted run.** Budgets, path rules and the org layer
   are Go code over config and `run.json`. Rules recorded at run start are what a resume
   applies.
4. **Fail closed.** A budget no routed provider can measure, an org file that cannot be read
   and a repository setting that weakens the org layer are each a config error before any
   call. None of them is a warning.
5. **Calibration proposes, a person applies.** `decisions calibrate` prints a config diff and
   its evidence. It never writes config, and it never runs as part of `run`.
6. **Honest about the boundary.** On a developer's machine every rule here holds only while
   the developer cooperates: they own the machine, the files and the CLI. The rules are a
   real boundary only where the operator does not control the machine (CI, a managed host).
   The spec says so wherever it matters and claims nothing more.

---

## 4. Path rules (GV02, GV03) — TODO G1

### 4.1 Config

```yaml
policy:
  paths:
    - match: "migrations/**"
      require_stages: [review, testing]
      approval: manual
    - match: "**/*.sql"
      approval: manual
```

- `match`: one pattern in `security.protected_paths` syntax, checked by
  `ValidateProtectedPattern`.
- `require_stages`: stage names (`research`, `analysis`, `planning`, `development`,
  `testing`, `review`), with no duplicates.
- `approval`: `manual`, or absent.
- A rule needs `require_stages`, `approval`, or both. An empty rule is an error.
- `deny_change` is declared in the schema and rejected with the message "use
  `security.protected_paths`: the guard already refuses any change there, git-ignored files
  included". Forbidding a path is already the guard's job, so a second mechanism would only
  be a weaker copy.
- Rules apply in file order, and every matching rule applies. Requirements add up.

### 4.2 When the rules are checked

**At run start**, after `workflow.Build` and before the first step. This happens only when the
workflow contains `implement`. For each rule whose `require_stages` the workflow does not
fully contain, the run prints one warning:

```
Warning: policy.paths "migrations/**" requires review, testing; this workflow has no testing.
A change there will stop the run. Add the stage with --stage, or start a new run.
```

The warning saves nothing yet, because the files are not known before `implement`. It tells
the operator early, while stopping the run is still cheap.

**After each `implement`** that passed the guard and the secret scan, the files come from the
run's net diff, `DiffNumstat(pre_run_ref, after)`. That is what a commit would contain, so a
file one attempt changed and a later attempt reverted does not count. Without a `pre_run_ref`
(`security.require_clean_worktree: false` on a dirty tree), the check uses the step's own
diff. The runs directory is excluded, as in `stepDiff`. Git-ignored files are not in the diff,
so they are never matched here. A path that must not change, ignored or not, belongs in
`protected_paths`.

For every rule that matches at least one file:
- the missing stages are `require_stages` minus the workflow's stages;
- if any stage is missing, the step fails with `failure_reason`:
  `policy.paths "migrations/**" requires testing, which this workflow does not run (changed:
  migrations/0042_users.sql)`. "Any other failure fails the run" (MVP.md §11), so the run
  fails. The working tree is left as it is, as after any failed step, and the operator
  decides what to do with it. A resume would run `implement` again and stop again, so the
  message points at a new run with `--stage`.

### 4.3 Record

Each `implement` execution gets `path_rules`, a list of the rules that matched:

```json
"path_rules": [
  {"match": "migrations/**", "files": ["migrations/0042_users.sql"],
   "require_stages": ["review", "testing"], "missing": [], "approval": "manual"}
]
```

`files` holds at most `MaxDiffPaths` entries, the same bound as `not_reported`. Then:
- `summary.md` gets a "Path rules" section for the last `implement`;
- `outcome` gets `approval_required: true` when that `implement` matched an `approval: manual`
  rule.

TODO.md asked for the rules in `decision.json`. It is written once, before any file has
changed, so the record lives in `run.json` instead.

### 4.4 Resume

The rules are recorded at run start in `options.path_rules`, a copy of `policy.paths`. A
resume reads them from there and never from the current config, the same way it reads
`review_fix_medium`. If the current config's rules differ, the resume prints the warning
`policy.paths changed since the run started; this run keeps the rules it started with`.

### 4.5 Approval (GV03)

`approval: manual` changes only the commit offer (MVP.md §42):

- **Interactive.** The question lists the matched rules and their files before the yes/no. The
  answer is already an explicit yes, so nothing else changes.
- **`auto_commit: true`.** No commit. The reason printed is `policy.paths requires manual
  approval: migrations/** (1 file); commit what you want yourself.` This uses the same `skip`
  path as a warning does today.
- **`--mode auto` without `auto_commit`.** There is no offer today, so nothing changes.

The commit offer never changes the run's status or exit code (§42). The approval is a gate on
the commit, not on the run. In CI it means the change stays uncommitted, which is the refusal
TODO.md asks for. A CI job can read `outcome.approval_required`.

---

## 5. Budgets (GV01) — TODO G3

### 5.1 Config

```yaml
limits:
  max_cost_usd: 5.00
```

The value is a positive, finite number. If the key is absent, there is no budget, which is how
runs work today.

Two pieces of TODO.md G3 are dropped:
- **`pricing:` in config.** The claude CLI already reports `total_cost_usd` per call, and
  Anthropic changes prices on its side. A price table in our config would be a second source
  of truth that goes stale.
- **`max_tokens`.** A tokens budget mixes cheap cache reads with expensive output tokens. Cost
  already covers it for the one provider that reports usage.

Either can come back when a provider reports tokens but no cost.

### 5.2 Fail fast

A budget is enforceable only if every provider call it covers reports a cost. Covered calls
are the workflow actions' routes, every extra reviewer in `actions.review.models`, and every
model under `by_complexity`. `provider.CostReporter` is an optional interface next to
`VersionVerifier`: `ReportsCost() bool`. Only the claude adapter implements it.

With `max_cost_usd` set, `ValidateDependencies` fails for any routed provider that does not
implement it:
`limits.max_cost_usd cannot be enforced: codex (actions.review) reports no cost`. A silently
unenforced budget is the one wrong answer.

### 5.3 Check

`domain.LimitsView.MaxCostUSD` (0 means none). Like `max_steps`, the engine checks it
**before each step**. If the sum of `CostUSD` over `run.Executions` has reached the budget:
- `RunStopped`;
- `failure_reason`: `budget exceeded: $5.12 of $5.00`;
- `LimitExceededError`.

The engine counts only persisted executions, so a resume counts what was already spent
without any new field. Raising the budget in the config and resuming continues the run, the
same way a raised `max_steps` does.

Limits of the check, written into MVP.md and not hidden:
- **Overshoot.** A step that starts under budget can end over it, by one step's cost at most.
  The engine has no way to stop a provider call partway through. That is "stop after the
  current call" from TODO.md.
- **Decision calls.** They are not counted. They happen before the run exists, and
  `decision_overhead` reports them.
- **Partial calls.** A call cut off before its final report (`usage.partial`) has no cost.
  The sum is then a lower bound, and the stop message says so: `(lower bound: 1 call without
  a final report)`.
- **Verify calls.** These are test commands, not provider calls, and cost nothing here.

---

## 6. Organization config layer (GV04) — TODO G2

### 6.1 Where it comes from

The org file is `/etc/orchestrator/org.yaml`. `ORCHESTRATOR_ORG_CONFIG` names another path
**only when that file does not exist**, which covers tests, CI images and machines without
root. A user can therefore not point a machine-wide policy away with an environment variable.

When neither exists, there is no org layer and nothing changes. When the file exists but
cannot be read or parsed, that is a config error, and the run does not fall back to "no
policy".

This keeps the honesty of §3.6. On a developer's own machine, root can delete the file. The
layer protects against a repository's config, not against the machine's owner.

### 6.2 What the org file may contain

The org file has its own strict schema (`config.OrgConfig`, version 1). Any other key is an
error, so nothing can be "set at org level" by accident:

| org key | what the org value means | the repository config may | error example |
|---|---|---|---|
| `security.require_clean_worktree: true` | a floor | not set it `false` | `orchestrator.yaml: security.require_clean_worktree: false is weaker than /etc/orchestrator/org.yaml` |
| `security.protected_paths` | added to the repository's list | only add | — |
| `security.allow_unverified_cli: false` | a floor | not set it `true` | as above |
| `security.secret_scan.mode` | a floor in the order `off` < `warn` < `stop` | set it equal or stricter | as above |
| `security.secret_scan.allow_paths` | owned by the org when the org sets `secret_scan` | not set its own | `… allow_paths is set by the org config` |
| `test.sandbox: bwrap` | a floor | not set it `none` | as above |
| `policy.paths` | org rules first, then the repository's | only add | — |
| `policy.stages.force` | added to the repository's, per complexity | only add | — |
| `limits.max_cost_usd` | a ceiling | set it lower, never higher or absent | `… max_cost_usd 20 is above the org's 5` |
| `allowed_models` (org only) | a list of model names | route only to these | `actions.review: model opus is not in allowed_models (/etc/orchestrator/org.yaml)` |

`allowed_models` covers every model the run can call:
- every action's base model and every model under `by_complexity`;
- every extra reviewer;
- the translator;
- the LLM decision provider.

A route with no model, which leaves the CLI's default, is an error under `allowed_models`,
because an allowlist cannot check a model nobody named.

Kept out on purpose:
- **Stage thresholds.** "Stricter" has no single meaning for a probability threshold. An org
  that wants a stage always forces it.
- **`auto_commit`.** `policy.paths` with `approval: manual` already covers what an org cares
  about.
- **Decision provider, Jev and Jira settings.** These are operational, not policy.

### 6.3 Where it applies

The new function `config.LoadLayered(repoPath, orgPath)`:
- loads both files;
- returns a conflict error naming both sources;
- returns the merged `Config`.

It replaces `config.Load` in `loadAndValidateConfig`, which both `run` and `resume` go through.
`validate` reports the org source. The fail-fast order is unchanged: the merge happens inside
the "Load" step, before `Validate`.

`run.json` records `org_config: {path, sha256}`, and each `resumes[]` entry records its own,
which is the evidence of which policy a run ran under. A resume under a changed org file prints
a warning and continues. The org layer applies to the current call. Rules a run recorded at its
start (`options.path_rules`) still win, per §4.4.

---

## 7. Threshold calibration (GV05, GV06) — TODO G5

### 7.1 What a run already says

A run sees only the outcome of the workflow it ran. There is no counterfactual. What
`run.json` and `decision.json` already give, per stage:

| stage | "paid off" signal in this run | source |
|---|---|---|
| testing | a test step failed after an `implement`, and a later test passed, or the run ended with the failure exposed | `executions[].test_result.success` |
| review | a review returned `fail` (or `medium` under `fix_medium`) and an `implement` followed | `review_result`, the next execution |
| research, analysis, planning | none per run. Only a comparison: same complexity, with versus without the stage, on `status` and `fix_iterations` | `outcome` |
| development | never calibrated: it is the task | — |

Every trace is used except these (`report.skipOrder` already names them):
- `manual`, from `--stage`: there are no probabilities;
- chosen by the user;
- mock.

Probabilities from different decision models are not comparable, so traces are grouped by
`decision.json: provider` and `model`.

### 7.2 `policy_hash` (GV05)

`decision.json` gets `policy_hash`, the sha256 of the canonical JSON of the
`domain.DecisionPolicy` the decision used. TODO.md suggested a `policy.version` key. A hash
needs no operator discipline, and it changes exactly when a threshold does.

`decisions calibrate` and `decisions report` group by it, so runs before and after a change are
compared and never mixed.

### 7.3 `orchestrator decisions calibrate [--runs <dir>] [--min-runs N]` (GV05)

The command is read-only, like `decisions report`, and lives in `internal/report`. For each
decision model and each of testing and review it prints:
- the runs where the stage ran, in probability buckets of 0.1;
- the paid-off rate in each bucket.

**Raising a threshold.** The proposal is the lowest bucket edge `t` above the current
threshold such that the stage paid off in **none** of the at least N runs (default 10) with
`P` in `[current, t)`.

**Lowering a threshold.** The proposal needs exploration data (§7.4): runs where the stage was
turned on below its threshold. It is the lowest edge below which explored runs paid off at
least as often as runs above the threshold, with N runs or more.

Research, analysis and planning get the comparison table and **no proposal**: there is no
per-run signal to base one on. A bucket under N runs prints `not enough data`.

The output ends with a YAML diff of `policy.stages.thresholds` and the rows behind each line,
for the operator to apply by hand. Nothing is written.

Models (TODO G5, "models — the same scheme"): one more table per complexity and model (from
`decision.json: models`), showing runs, completed share, mean `fix_iterations` and mean cost.
There is no proposal. A model change is a cost and quality trade-off, and the operator makes
it.

### 7.4 Exploration, opt-in (GV06)

```yaml
policy:
  explore:
    band: 0.05
    rate: 0.1
```

The section is absent by default. When set, for each stage that the policy left **off**:
- the stage is not forced;
- its `P` is within `band` below its threshold;
- if `hash(run_id, stage) mod 1000 < rate × 1000`, the stage is turned **on**.

Rules for exploration:
- **It only turns stages on.** Exploration never removes testing or review, so it never makes
  a run less checked. It only costs more. The data for raising a threshold already comes from
  runs where the stage ran, so turning stages on is the only data missing.
- **Recording.** An explored stage is recorded in `decision.json` as
  `stages[].explored: true`, with the reason `explored (P 0.47, threshold 0.50)`.
- **Same decision, same workflow.** The input to that guarantee becomes the decision plus the
  run id, and the trace shows both. A resume reads the persisted workflow, so it never
  explores.
- **Never with `--stage`.** That run has no probabilities.
- **Code location.** `policy.Explore(decision, policy, seed)` is a pure function in
  `internal/policy`, imports only `domain`, and `app` passes the seed from the run id. `rate`
  is in `(0, 0.5]` and `band` is in `(0, 0.2]`.

Per TODO.md G5, comparing a cheaper model on a share of runs at the same complexity is
**not** in this phase. It would mean a model chosen by chance, which breaks MVP.md §31.1
static routing harder than turning a stage on does. It can come after GV06 has shown that
exploration is used at all.

### 7.5 What is not done

TODO.md G5 has one more idea: a `--stage` run still asks the decision provider and records the
probabilities. That idea is **not** done in this phase. MVP.md §40 says `--stage` calls no
decision provider, and changing that costs a decision round on every manual run, for a label
that only means "the operator wanted this stage". Revisit after GV05 shows whether the data is
too thin without it.

---

## 8. Persistence

New fields:

| file | field | written | validated on load |
|---|---|---|---|
| run.json | `options.path_rules` | at run start | each rule as in config |
| run.json | `executions[].path_rules` | after the path check of an `implement` | `match` valid, `files` ≤ `MaxDiffPaths`, stages valid, `missing` ⊆ `require_stages` |
| run.json | `outcome.approval_required` | with the outcome | bool |
| run.json | `org_config`, `resumes[].org_config` | at run and resume start | `path` absolute, `sha256` 64 hex |
| decision.json | `policy_hash` | with the decision (absent for `manual`) | 64 hex |
| decision.json | `stages[].explored` | with the decision | only when `enabled` and not `forced` |

The budget needs no field. The limit lives in the config, and what was spent lives in the
executions.

Old runs without these fields load as before.

## 9. Why G4 (audit export) is dropped

Who reviews an audit trail, and where it lives, depends on how the orchestrator is deployed:

- **Developers run it locally.** Today it is a local CLI on each developer's machine, like
  Claude Code. An export that reads the local `.orchestrator/runs` covers one machine. A
  central audit needs every run to push its events to a collector, which would be an
  OpenTelemetry exporter configured by the org layer (§6). Even then, the developer owns the
  machine and can turn it off, so the trail only holds while developers cooperate.
- **It runs centrally.** Where the operator does not own the machine (CI, which is W4, or a
  hosted runner), the platform's logs already collect `run.json` and `summary.md`. A remote
  service would break MVP.md §30 ("no remote repository execution", "no REST API").
- **A hash chain** in a local file protects nothing. Whoever can edit the file can recompute
  the chain.

The commit trailer `Orchestrator-Run` already links a commit to its run. G4 is marked `[-]`
in TODO.md with this reason. Revisit it after W4, or when a customer asks for it. In the
distributed model it would be a push through OTel under the org config, not an export.

## 10. Owner's decisions

- D1 (2026-10-12): one spec for phase 4, `specs/governance/`.
- D2 (2026-10-12): G4 is dropped (§9).

## 11. Author's defaults (change before the task starts if wrong)

- A1: `deny_change` is rejected in favor of `security.protected_paths` (§4.1).
- A2: path rules are checked on the run's net diff, not per step (§4.2).
- A3: a missing required stage fails the run. There is no resumable stop, since a resume
  would hit the same rule.
- A4: no `pricing:` and no `max_tokens` (§5.1). The budget covers steps, not decision calls
  (§5.3).
- A5: the org file is found at `/etc` first, then the environment variable only as a fallback
  (§6.1).
- A6: the org layer does not touch thresholds or `auto_commit` (§6.2).
- A7: `policy_hash` replaces `policy.version` (§7.2).
- A8: exploration only turns stages on, and never compares models (§7.4).
- A9: `--stage` still asks nothing (§7.5).

## 12. Integration scenarios (GV07), from 47 on

| # | scenario |
|---|---|
| 47 | budget: a fake claude reports `total_cost_usd` 3 per call, `max_cost_usd: 5` → `stopped`, "budget exceeded"; resume with `10` continues |
| 48 | budget with `actions.review` on codex → config error before any call |
| 49 | path rule requiring testing, workflow without testing, implement touches `migrations/x.sql` → warning at start, step fails, `path_rules.missing: [testing]` |
| 50 | same rule, workflow with testing → passes, `path_rules` recorded, `missing: []` |
| 51 | `approval: manual` + `auto_commit: true` → no commit, the reason printed, `outcome.approval_required` |
| 52 | an implement attempt changes a matched file, a later attempt reverts it → no match on the second check |
| 53 | org file: repo `test.sandbox: none` against org `bwrap` → config error naming both files |
| 54 | org file: `allowed_models` without the routed model → config error; `protected_paths` union is enforced by the guard |
| 55 | `ORCHESTRATOR_ORG_CONFIG` ignored when `/etc` file exists (the test uses an injected path for the `/etc` location) |
| 56 | exploration: a fixed run id that hashes under `rate` turns a stage on, recorded `explored`; one that does not, does not |

`decisions calibrate` gets golden-file tests in `internal/report`, like `decisions report`.

## 13. Out of scope

- G4 (§9).
- Writing a calibrated config back.
- Calibrating research, analysis or planning thresholds automatically.
- Exploring models.
- Per-user or per-team policy beyond one org file.
- Fetching the org config over the network.
- Enforcing anything against the machine's owner.

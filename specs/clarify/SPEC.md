# Spec: Clarify — a clear task before the decision; one stage that reads the code

Status: **draft** (2026-10-25). The owner's decisions are in §2 (D13 and D14 settled A2 and A3);
the author's defaults, open to change, are in §13. Once CL07 is done, MVP.md (§49, and the sections listed below) becomes the
normative home and this file stays the design record.

The spec touches these MVP.md sections:
- §8: the decision flow (the clarity question in the pre-check, the clarify step);
- §9: the policy (two asked stages, the fallback, `policy.clarify.threshold`);
- §10, §17, §20: the actions, their artifacts and prompts (`investigate` replaces `analyze` and
  `plan`);
- §21, §43: persistence and `summary.md` (`task.clarification`, the clarity trace);
- §37: resume (old workflows refused, the clarification read back);
- §38.1: authorities (the clarify call runs with `none`);
- §40: `run --stage` (the names, and clarify before the stages);
- §44.4: review criteria from the investigation;
- §48.6/§11.2: a story task skips clarify and investigation.

---

## 1. Problem

`analyze` and `plan` do the same work today. Both read the repository and return the places to
change: analysis gives `root_cause`, `affected_files`, `recommendation`; the plan that follows it
gives `steps`, `affected_files`, `risks` and the acceptance criteria. A complex task pays for two
read-only passes over the same code, and the plan mostly restates the analysis it was given
(`internal/action/testdata/prompts/plan_analysis.golden`).

Nothing checks the one thing a reader of a ticket checks first: **is it clear what to do?** An
unclear task is classified anyway, its complexity and stages are estimated from a text that does
not say what it wants, and the run spends implement, test and review on a guess.

## 2. Decisions (owner, 2026-10-25)

| # | decision |
|---|---|
| D1 | Analysis is about the **completeness of the request**, not about the code. It never reads the repository's code. |
| D2 | The clarity check runs **before** the stage questions, and analysis is no longer a stage question. |
| D3 | Clarity is a **probability from the decision provider**, judged against a threshold in Go, like every other decision. |
| D4 | The decision provider sees **text only** (the request, later the request with its clarification), never the instructions: a provider like Jev has a small context. |
| D5 | The instructions (Markdown by default, a config key lists the files) go only to the **clarify call**, an LLM CLI, and only the parts Go selects, within a byte limit. |
| D6 | Interactive (a terminal, not `--mode auto`): the operator answers the questions. Auto: the model analyses the request against the instructions itself. |
| D7 | After the clarify step the provider is asked again. Still unclear: interactive asks the operator to add to the task (bounded rounds); auto stops the run with `needs input` and the list of what is unclear. A new run with a fuller task follows; `resume` takes no new input. |
| D8 | `analyze` becomes the **clarify** step (not a stage); `planning` / `plan` becomes **investigation** / `investigate`, which reads the code and writes the plan, or, without development, the answer. |
| D9 | A config that still names `analysis`, `planning`, `actions.analyze` or `actions.plan` is a **config error** with a message, never reinterpreted. Same for `run --stage analysis`. |
| D10 | Thresholds are recalibrated later; `questions_hash` changes on purpose. |
| D11 | Jev answers the new question; with no Jev access, only the mock and the mapper change. |
| D12 | A story task skips clarify (its task file is the plan). `run --stage` runs clarify. |
| D13 | A failed clarify call (not an auth failure, which is fatal anyway) **fails the run** (`ProviderError`): an unclear task never goes on unclarified. |
| D14 | `clarify.enabled: false` turns the clarify step off, for the benchmark. The clarity question is still asked and recorded (shadow data for calibration); nothing acts on it. |

## 3. Names

| before | after | kind | authority |
|---|---|---|---|
| stage `analysis`, action `analyze` | **clarify** step (`clarify` in logs and config) | before the decision, not a stage, never in a workflow | `none` |
| stage `planning`, action `plan` | stage **`investigation`**, action **`investigate`** | workflow stage | `read` |

`analysis` is not kept as the name of the merged stage: in every `decision.json` written so far
it means "the cause or the place to change is unknown", and `decisions calibrate` would mix the
two meanings. The new names have no history.

Stage order becomes `research → investigation → development → testing → review`
(`domain.StageOrder`). `run --stage` accepts `investigation` and the alias `investigate`;
`analysis`, `analyze`, `planning` and `plan` are refused with a message naming this spec.

## 4. The decision flow

```
[--jira] → fetch the issue (unchanged)
request → translate (unchanged)
        → pre-check: P(none), P(project), P(clear)          one call (decision.Provider.ContextDepth)
        → Go: P(clear) ≥ policy.clarify.threshold ?
            yes → go on
            no  → clarify step (§5)                          LLM CLI, authority none, instructions
                → re-check: P(clear) of request + clarification   (decision.Provider.Clarity)
                → still below: interactive → the operator adds to the task → re-check,
                                             up to clarify.max_rounds
                               auto, or rounds spent → the run stops: needs input (§6)
        → policy: project if P(project) ≥ project_threshold (unchanged)
        → Context Manager (request + clarification as its query)
        → classification: complexity + research + development (+ scope_story)
        → policy → workflow
```

- **One extra question, no extra call for a clear task.** `clarity` is asked in the pre-check's
  call, against the pre-check's state (`decision.PreCheckState{Request}`: the request alone).
  Only an unclear task costs the clarify call and the re-check.
- **The re-check** asks only `clarity`, against the request and the clarification as text
  (`decision.ClarityState{Request, Clarification}`, §7). It is a decision call: it records usage
  like `context_depth`, and its failure takes the provider-failure path that exists today
  (`classification.Service.manual`: the manual choice under `on_failure: ask` at a terminal,
  else the error).
- **The text the decision provider sees after a clarification** is the request followed by the
  rendered clarification (§7.2). Translation applies to that text as a whole, by the existing
  rule (≥30% Cyrillic prose); a second translation call is made only when the clarification was
  added. The Context Manager builds its query from the same text.
- **`run --stage`** (D12): the pre-check is asked for `clarity` alone (`decision.Provider.Clarity`
  on the request), clarify runs as above, and the stages are the operator's. The decision stays
  `source: manual`; the clarity trace is recorded beside it.
- **`clarify.enabled: false`** (D14): the clarity question is asked and recorded as above, the
  clarify step and the re-check never run, and the run goes on whatever `P(clear)` is.
- **A story task** (`run --story-task`): no clarity question, no clarify step. `scope_story`
  stays skipped as today.
- **A pre-check failure** goes to the manual choice as today; clarify is not run, because the
  operator choosing stages by hand has read the task.

## 5. The clarify step

### 5.1 The call

One provider call per clarify step, routed by the `clarify` config section (§9), with
`domain.AuthorityNone` passed explicitly (like the translator and the LLM decision provider,
never from `AuthorityFor`). No tools: the model cannot open a file, so "do not read the code" is
enforced by the authority, not asked for in the prompt. It runs with `execution.ScrubbedEnv()`,
its prompt on stdin, its `apiKeySource` checked (`FailureAuth` fatal).

The prompt (`action.BuildClarifyPrompt`, marker `ACTION: clarify`) holds:
1. the request, through `taskBlock`;
2. the project's rules (§5.2) and the selected instruction excerpts, each with its path and
   heading, through `fence`;
3. on a second step in the same run (interactive, §6), the clarification so far;
4. the mode: `interactive` (questions for an operator) or `auto` (answer what the instructions
   support, assume the rest, say which is which).

It asks for one JSON object:

```json
{
  "points": [
    {
      "unclear": "which export format is meant",
      "question": "Should the export be CSV or JSON?",
      "answer": "CSV",
      "basis": "docs",
      "source": "docs/export.md#Formats"
    }
  ],
  "summary_short": "..."
}
```

- `basis` is `docs` (the excerpts say so; `source` names the excerpt), `assumption` (auto only:
  a reasonable default, stated as such) or `none` (no answer).
- At most 8 points, each field at most 300 bytes, cut in Go like `capCriteria`. The answer is
  validated strictly: unknown keys, a `basis` outside the three, `source` on a non-`docs` point,
  or `assumption` in interactive mode is an `InvalidResponse`. Unlike analysis and plan today,
  a prose answer is not accepted: the points decide what the operator is asked and what the
  run stops on.
- No points: the model found nothing unclear. The re-check still runs; Go decides.

### 5.2 What the instructions are (D5)

Go selects them in `internal/projectctx` (exported `Docs`), reusing what the Context Manager
already does for documentation:
- the files are listed by git (`git ls-files`, so `.gitignore` holds), matched against
  `clarify.docs` (globs, default `["**/*.md"]`), vendored paths dropped (`isVendored`);
- the deny list (`.env*`, `*.pem`, `*.key`, `credentials*`, `secrets*`, `context.extra_deny`)
  applies to every path segment before anything is read; binaries, files over the Context
  Manager's size limit and symlinks are never read;
- the rules: the bullets of `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md` at the root
  (`projectRules`, unchanged);
- the excerpts: the Markdown sections (`splitSections`) of the matched files, ranked by
  `queryTokens` of the request (and its translation), the best first (`docExcerpts`,
  generalised from `docs/*.md` to the matched files);
- rules and excerpts together are cut to `clarify.max_bytes` (default 16384), excerpts dropped
  from the lowest rank first, then rules from the end.

What was sent is recorded in the clarify artifact (paths, headings, bytes), never the files'
full text outside the prompt.

### 5.3 Interactive

The operator sees each point with its question and the model's answer, if any:

```
The task is not clear enough (P(clear) 0.42 < 0.80). 2 questions:

1. Which export format is meant?
   from docs/export.md#Formats: CSV
   answer [Enter keeps it, - leaves it open]:
2. Which users get the button?
   answer [- leaves it open]:
```

Enter keeps the model's answer (`by: docs`); text replaces it (`by: operator`); `-` leaves the
point open. Input waits on `UserCtx` (the time is not the run's, as for the manual choice).
EOF or Ctrl-C cancels the run (`UserCancellation`).

### 5.4 Auto

Points with `basis: docs` or `assumption` are taken as answered (`by: docs`, `by: assumption`);
`none` stays open. No operator is involved.

## 6. Still unclear

After the re-check, `P(clear) ≥ threshold` ends the step: the clarification is final. Otherwise:

- **Interactive, rounds left** (`clarify.max_rounds`, default 2, counts the operator's
  additions): the open points (or, with none open, "the task is still rated unclear") are shown
  and the operator types an addition to the task, one paragraph, ended by an empty line. The
  addition joins the clarification (`by: operator`, no question) and the re-check runs again. An
  empty addition stops the run as below.
- **Auto, or no rounds left**: the run ends with status `stopped`, `failure_reason:
  "task needs input"`, exit code 4 (`WorkflowOrLimit`: the run stopped on the orchestrator's
  own rule). The console and `summary.md` list what is unclear: the open points with their
  questions, and the last `P(clear)` against the threshold. The run has no decision and no
  workflow; `resume` refuses it ("nothing to resume: the run stopped before its workflow"), and
  the operator starts a new run with a fuller task.

## 7. Data

### 7.1 `run.json: task.clarification`

```json
"clarification": {
  "threshold": 0.8,
  "checks": [0.42, 0.86],
  "status": "clear",
  "rounds": [
    {
      "kind": "clarify",
      "mode": "interactive",
      "points": [
        {"unclear": "...", "question": "...", "answer": "CSV", "by": "docs", "source": "docs/export.md#Formats"},
        {"unclear": "...", "question": "...", "answer": "admins only", "by": "operator"}
      ]
    },
    {"kind": "addition", "text": "..."}
  ]
}
```

- Absent when the first check cleared the threshold, under `clarify.enabled: false`, for a story
  task and for a pre-check that failed. `status` is `clear` or `unclear` (the run stopped). `checks` holds every `P(clear)` in
  order, the pre-check's first. `by` is `operator`, `docs`, `assumption` or absent (open).
- Loaded strictly (`persistence`): probabilities in [0, 1], `len(checks) == 1 + len(rounds)` (a re-check
  follows every round), the first round of kind `clarify`, the closed sets above, the byte caps of §5.1. A run written
  before this spec has no field and loads.
- It is written once, before the decision, and never changed: a resume reads it back, never
  re-asks (MVP.md §37).

### 7.2 What the stages get

The invariant "stages always get the user's original request" holds. Every stage prompt that
takes the task (`research`, `investigate`, `implement`, `review`) gets the request through
`taskBlock`, as today, and after it the rendered clarification through `fence`:

```
Clarification (agreed before the run; the task above stays the source):
- Which export format is meant? CSV  [from docs/export.md#Formats]
- Which users get the button? admins only  [operator]
- Assumed: the button is hidden for guests  [assumption]
Added by the operator:
  ...
```

Open points of a clear task are listed as `Open: <question>`. The rendering is one Go function
(`domain.Clarification.Render`), used for the prompts, the re-check's state and `summary.md`.

### 7.3 `decision.json`

- `clarity`: `{threshold, checks, status}` (the same values as in `run.json`) and the reason
  (`P(clear) 0.86 >= 0.80 after 1 clarify step`). Under `clarify.enabled: false` it is
  `{threshold, checks: [p], status: "disabled"}`, so calibration still sees the answer.
- `usage.clarify[]` and `usage.clarity_recheck[]`, one entry per call; they count toward
  `outcome.decision_overhead` and toward `limits.max_cost_usd`.
- `policy_hash` covers `policy.clarify.threshold`; `questions_hash` covers the clarity question
  and the removal of `stage_analysis`.

### 7.4 Logs and artifacts

`logs/clarify-<N>.{stdout,stderr}.log`, `artifacts/clarify-<N>.json` (the prompt as sent, the
excerpts' paths and headings, the parsed points), `logs/decide-clarity-<N>.*` for each re-check,
N from 1.

## 8. Stages after the change

### 8.1 Asked stages

`domain.AskedStages()` is `research, development`. `stage_analysis` is removed from
`decision.StageQuestions` (Jev and LLM alike; an answer to it is an `unexpected answer`). The
policy rule is otherwise unchanged:

- stages = `policy.stages.force[complexity]` ∪ the asked stages at or above their threshold ∪
  testing whenever development is on; without development, testing and review are off;
- **nothing enabled → `investigation`** (it was `analysis`): the run reads the code and answers.

### 8.2 Investigation

`investigate` is today's `plan` with analysis folded in:
- authority `read`;
- with development in the workflow, its artifact is the plan: `summary`, `summary_short`,
  `root_cause` (new, optional: what analysis used to give), `steps`, `affected_files`, `risks`,
  `acceptance_criteria` (capped as today), read by implement (the first implement starts from
  `affected_files`, DS08) and by review (the criteria, MVP.md §44.4);
- without development and as the last read-only step, it writes `answer`
  (`isFinalReadOnlyStep`, unchanged in shape);
- it gets research's artifact and the clarification; there is no analysis artifact any more.

Research without development still answers itself, as today.

### 8.3 Templates

The example config becomes:

```yaml
policy:
  stages:
    thresholds: {research: 0.81, development: 0.81}
    force:
      medium: [investigation, review]
      complex: [investigation, review]
  clarify:
    threshold: 0.80
```

A simple task with development gets no investigation unless the template says so: implement
reads the code itself (it has `edit`, which includes reading).

### 8.4 Story tasks

`policy.SkipPlanning` turns off research and investigation (it turned off research, analysis and
planning). The name stays; the story's task file is still the plan.

## 9. Configuration

```yaml
clarify:                       # required (D9: every config is rewritten for this spec anyway)
  enabled: true                # false: no clarify step (D14); the keys below are then optional
  provider: claude
  model: sonnet
  effort: medium               # optional, as for actions
  timeout: 120s
  docs: ["**/*.md"]            # globs over git-listed files; deny list applies first
  max_bytes: 16384             # rules + excerpts in the prompt
  max_rounds: 2                # operator additions after the re-check, 0..5

policy:
  clarify:
    threshold: 0.80            # (0, 1]; in DecisionPolicy, so in policy_hash
```

- `clarify` is required: a route, or `enabled: false` written out (`clarify: {enabled: false}`),
  never an implicit default. With `enabled: false`, a route given anyway is still validated
  but not used.
- With `enabled: true`, `clarify` is checked in the fail-fast chain like `translation`: the executable
  (`ValidateDependencies`), `Supports(AuthorityNone)` (`ValidateAuthorities`), the CLI version,
  `allowed_models` of the org layer, and `provider.CostReporter` when `limits.max_cost_usd` is
  set.
- Rejected with a message naming this spec (D9): `policy.stages.thresholds.analysis`,
  `.planning`; `analysis` or `planning` in `policy.stages.force` (repository and org layer);
  `actions.analyze`, `actions.plan`; `policy.explore` naming them. Their replacements:
  `policy.clarify.threshold`, `investigation`, `actions.investigate`.
- `actions.investigate` takes what `actions.plan` took, `by_complexity` included.

## 10. Resume

- A run whose `workflow.json` has `analyze` or `plan`, or whose decision has `analysis` or
  `planning`, is refused: "this run was started before specs/clarify; it cannot be resumed by
  this version". Strict reading already refuses unknown actions; the message makes it clear.
- `task.clarification` is read back and passed to the stages; nothing is asked again.
- A `needs input` run is refused (§6).

## 11. What does not change

- Go decides: clarity is a probability and a threshold; the clarify model's points never decide
  whether the run goes on, the re-check does.
- No authority grows: clarify has `none`, investigation keeps `read`, implement `edit`.
- The decision provider never sees an instruction file, only text a person or the clarify model
  wrote.
- The same decision gives the same workflow; the same persisted run gives the same resume.

## 12. Tasks

| task | title | size | depends on |
|---|---|---|---|
| CL01 | Analysis leaves the stages: two asked stages, fallback, config errors | M | — |
| CL02 | `planning`/`plan` → `investigation`/`investigate`, with `root_cause` | M | CL01 |
| CL03 | The clarity question: pre-check, re-check, policy threshold, trace | M | — |
| CL04 | `projectctx.Docs`: instruction excerpts for clarify | S | — |
| CL05 | The clarify step: call, operator, auto, needs input, `task.clarification` | L | CL03, CL04 |
| CL06 | Stages get the clarification; summary.md | S | CL02, CL05 |
| CL07 | Scenarios, MVP.md §49 and the touched sections, CLAUDE.md, README, example, bench, TODO.md | M | CL01–CL06 |

## 13. Author's defaults (open to change)

| # | default | alternative |
|---|---|---|
| A1 | `policy.clarify.threshold: 0.80`, uncalibrated; `decisions calibrate` learns `clarity` in CL07 (grouping only, proposals after data) | none until data exists |
| A4 | The org layer gains no clarify keys (no threshold floor) | a floor on `policy.clarify.threshold` |
| A5 | A clarification is not edited on the review screen; the operator edits the task by starting a new run | an edit field |
| A6 | `--mode plan` at a terminal is interactive for clarify, like `manual` | auto |

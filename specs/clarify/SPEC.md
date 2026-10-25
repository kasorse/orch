# Clarify: a clear task before the decision, one stage that reads the code

Status: **planned** (2026-10-25). A story spec: `orchestrator story run specs/clarify` runs its
tasks CL01–CL08 on the repository `orche`. The owner's decisions are under Decisions (D1–D14);
the author's defaults A1, A4–A6 are open. Once CL08 is done, MVP.md §49 (and the sections it
touches: §8, §9, §10, §17, §20, §21, §37, §38.1, §40, §43, §44.4, §48) is the normative home and
this file stays the design record.

## Problem

`analyze` and `plan` do the same work. Both read the repository and return the places to change:
analysis gives `root_cause`, `affected_files`, `recommendation`; the plan after it gives `steps`,
`affected_files`, `risks` and the acceptance criteria, mostly restating the analysis it was given
(`internal/action/testdata/prompts/plan_analysis.golden`). A complex task pays for two read-only
passes over the same code.

Nothing checks what a reader of a ticket checks first: is it clear what to do? An unclear task is
classified anyway, its complexity and stages are estimated from a text that does not say what it
wants, and the run spends implement, test and review on a guess.

## Approach

### Ground rules for the tasks of this story

- This is a story task. `story run` writes `specs/clarify/README.md` and each task's `## As built`:
  **do not edit anything under `specs/clarify/`**. CLAUDE.md's bookkeeping for `specs/*/tasks`
  (ticking, the `Status:` line, As built) is done by `story run` for these tasks.
- Only CL08 edits the shared documents (MVP.md, CLAUDE.md, TODO.md, README.md,
  `orchestrator.example.yaml`, `bench/README.md`). Earlier tasks change those only where a test
  reads them (a config example a test loads), and say so in the commit.
- `orchestrator.yaml` is the operator's own file, outside the repository: never rely on it or
  try to edit it. Tests build their configs themselves.
- The tasks form one chain (CL01 → … → CL08): each starts on the previous one's branch, so no two
  tasks edit the same file in parallel.
- Every task leaves `scripts/check.sh` green (root and `bench/`). Golden files are rewritten with
  `go test <pkg> -update`; read the diff of every rewritten golden before committing it.
- CLAUDE.md's invariants hold: Go decides from probabilities and config, never a model;
  `internal/workflow` and `internal/policy` import only `domain`, `internal/report` only
  `domain`; every new `run.json` and `decision.json` field is validated strictly on load and old
  files still load; every new config key is declared in the schema; tests need no network and no
  real CLI.
- No silent change of meaning: a key or name whose meaning changes is rejected with a message
  naming its replacement and `specs/clarify`, never mapped to the new one.
- Any change to `internal/decision/questions.go` changes `questions_hash`; that is intended here.

### 1. Names

| before | after | kind | authority |
|---|---|---|---|
| stage `analysis`, action `analyze` | the **clarify** step | before the decision, never a stage, never in a workflow | `none` |
| stage `planning`, action `plan` | stage **`investigation`**, action **`investigate`** | workflow stage | `read` |

`analysis` is not reused for the merged stage: in every `decision.json` so far it means "the cause
or the place to change is unknown", and `decisions calibrate` would mix the two meanings.

Stage order: `research → investigation → development → testing → review`. `run --stage` accepts
`investigation` and the alias `investigate`; `analysis`, `analyze`, `planning`, `plan` are refused
with a message naming `specs/clarify`.

### 2. The decision flow

```
[--jira] → fetch the issue (unchanged)
request → translate (unchanged)
        → pre-check: P(none), P(project), P(clear)        one call (decision.Provider.ContextDepth)
        → Go: P(clear) ≥ policy.clarify.threshold ?
            yes → go on
            no  → clarify step (§3)                        LLM CLI, authority none, instructions
                → re-check: P(clear) of request + clarification   (decision.Provider.Clarity)
                → below: interactive → operator adds to the task → re-check, up to clarify.max_rounds
                         auto, or rounds spent → the run stops: task needs input (§4)
        → policy: project if P(project) ≥ project_threshold (unchanged)
        → Context Manager (query: request + clarification)
        → classification: complexity + research + development (+ scope_story)
        → policy → workflow
```

- `clarity` is asked in the pre-check's call, against the pre-check's state (the request alone):
  a clear task costs no extra call.
- The re-check asks only `clarity`, against `decision.ClarityState{Request, Clarification}` (text
  only). It records usage like `context_depth`; its failure takes the existing provider-failure
  path (`classification.Service.manual`).
- After a clarification, the text the decision provider sees is the request followed by the
  rendered clarification (Contracts: Render). Translation applies to that text by the existing
  rule; a second translation call is made only when a clarification was added. The Context
  Manager builds its query from the same text.
- `run --stage`: `decision.Provider.Clarity` is asked on the request, clarify runs as above, the
  stages are the operator's; the decision stays `source: manual`, the clarity trace beside it.
- `run --story-task`: no clarity question, no clarify step.
- `clarify.enabled: false`: the clarity question is asked and recorded; no clarify step, no
  re-check, the run goes on whatever `P(clear)` is.
- A pre-check failure goes to the manual choice as today; clarify does not run.
- Interactive is the existing `mode != auto && isTerminal(stdin)` computed before the decision
  (`--mode plan` at a terminal is interactive, A6).

### 3. The clarify step

**The call.** One provider call per clarify step, routed by the `clarify` config section, with
`domain.AuthorityNone` passed explicitly (like the translator; never through `AuthorityFor`). No
tools: the model cannot open a file, so "do not read the code" is enforced by the authority. It
runs with `execution.ScrubbedEnv()`, the prompt on stdin, `apiKeySource` checked (`FailureAuth`
fatal). Any other failure fails the run with `ProviderError` (D13): an unclear task never goes on
unclarified.

**The prompt** (`action.BuildClarifyPrompt`, first line `ACTION: clarify`): the request through
`taskBlock`; the rules and the selected excerpts, each with path and heading, through `fence`; the
clarification so far, if any; the mode (`interactive`: questions for an operator; `auto`: answer
what the instructions support, assume the rest, say which is which). It asks for the JSON of
Contracts: clarify answer.

**The instructions** are chosen by Go in `internal/projectctx` (`Docs`), reusing the Context
Manager's documentation code: files listed by `git ls-files`, matched against `clarify.docs`
(default `["**/*.md"]`), vendored paths dropped; the deny list applied to every path segment
before anything is read; binaries, files over the size limit and symlinks never read; the rules
are the bullets of root `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md` (`projectRules`); the excerpts
are the Markdown sections of the matched files ranked by `queryTokens` of the request and its
translation (`docExcerpts`, generalised from `docs/*.md`); together cut to `clarify.max_bytes`,
excerpts dropped from the lowest rank first, then rules from the end. The Context Manager keeps
calling `docExcerpts` with `docs/*.md`, so `project_context.json` does not change.

**Interactive.** The operator sees each point with its question and the model's answer:

```
The task is not clear enough (P(clear) 0.42 < 0.80). 2 questions:

1. Which export format is meant?
   from docs/export.md#Formats: CSV
   answer [Enter keeps it, - leaves it open]:
2. Which users get the button?
   answer [- leaves it open]:
```

Enter keeps the model's answer (`by: docs`), text replaces it (`by: operator`), `-` leaves the
point open. Input waits on `UserCtx` (not the run's time); EOF or Ctrl-C cancels the run.

**Auto.** `basis: docs` and `basis: assumption` are taken as answered (`by: docs`,
`by: assumption`); `none` stays open.

### 4. Still unclear

After the re-check, `P(clear) ≥ threshold` makes the clarification final. Otherwise:
- interactive with rounds left (`clarify.max_rounds`, default 2, counts additions): the open points
  (or "the task is still rated unclear") are shown; the operator types an addition ended by an
  empty line; it joins the clarification (`kind: addition`) and the re-check runs again. An empty
  addition stops the run as below;
- auto, or no rounds left: the run ends `status: stopped`, `failure_reason: "task needs input"`,
  exit code 4 (`WorkflowOrLimit`). The console and `summary.md` list the open points and the last
  `P(clear)` against the threshold. The run has no decision and no workflow; `resume` refuses it
  ("nothing to resume: the run stopped before its workflow"); the operator starts a new run.

### 5. Stages after the change

- `domain.AskedStages()` is `research, development`; `stage_analysis` is removed from
  `decision.StageQuestions` (an answer to it is an `unexpected answer`, Jev and LLM alike).
- stages = `policy.stages.force[complexity]` ∪ asked stages at or above their threshold ∪ testing
  whenever development is on; without development testing and review are off; **nothing enabled
  → `investigation`**, reason `forced: no stage cleared its threshold, defaulting to
  investigation`.
- `investigate` is today's `plan` with analysis folded in: authority `read`; with development its
  artifact is the plan (`summary`, `summary_short`, `root_cause` new and optional, `steps`,
  `affected_files`, `risks`, `acceptance_criteria` capped as today), read by implement (the first
  implement starts from `affected_files`) and by review (the criteria); without development, as
  the last read-only step, it writes `answer` (`isFinalReadOnlyStep`). It gets research's artifact
  and the clarification; there is no analysis artifact.
- Research without development still answers itself.
- `policy.SkipPlanning` (story tasks) turns off research and investigation.
- The example template: `force: {medium: [investigation, review], complex: [investigation,
  review]}`, `thresholds: {research: 0.81, development: 0.81}`. A simple task with development gets
  no investigation unless the template says so: implement reads the code itself.

### 6. Resume

- A run whose `workflow.json` has `analyze` or `plan`, or whose decision has `analysis` or
  `planning`, is refused: "this run was started before specs/clarify; it cannot be resumed by this
  version".
- `task.clarification` is read back and given to the stages; nothing is asked again.
- A run stopped with `task needs input` is refused (§4).

## Contracts

**The clarity question** (`decision.ClarityQuestion()`, yes/no, ID `clarity`, in `QuestionsHash`):
- instructions: "Is `request` clear enough to act on: is it stated what has to be achieved, where,
  and how to tell that it is done?"
- yes: "A competent engineer new to the task could start without asking anything: the goal, the
  part of the system it concerns and the expected result are stated or follow unambiguously."
- no: "Something essential is missing or ambiguous: the goal, the affected part, the expected
  behaviour or result, or a choice the request leaves open."

`ContextDepthDecision` carries `PClear`. `decision.Provider` gains `Clarity(ctx, request,
clarification string) (domain.ClarityDecision, error)`. Jev's pre-check request asks `clarity`
beside `context_depth`; a `Clarity` request asks it alone; the LLM provider renders the re-check
with first line `ACTION: decide-clarity`. `policy.ClearEnough(p, pol)` is inclusive.

**Clarify answer** (strict: unknown keys, a `basis` outside the three, `source` on a non-`docs`
point, `assumption` in interactive mode are `InvalidResponse`; a prose answer is not accepted; at
most 8 points, each field at most 300 bytes, cut in Go on a rune boundary):

```json
{"points": [{"unclear": "which export format is meant", "question": "Should the export be CSV or JSON?",
             "answer": "CSV", "basis": "docs", "source": "docs/export.md#Formats"}],
 "summary_short": "..."}
```

`basis`: `docs` (the excerpts say so; `source` names the excerpt), `assumption` (auto only), `none`.
No points: the re-check still runs; Go decides.

**`run.json: task.clarification`** (written once before the decision, never changed):

```json
"clarification": {
  "threshold": 0.8, "checks": [0.42, 0.86], "status": "clear",
  "rounds": [
    {"kind": "clarify", "mode": "interactive", "points": [
      {"unclear": "...", "question": "...", "answer": "CSV", "by": "docs", "source": "docs/export.md#Formats"},
      {"unclear": "...", "question": "...", "answer": "admins only", "by": "operator"}]},
    {"kind": "addition", "text": "..."}
  ]
}
```

Absent when the first check cleared the threshold, under `clarify.enabled: false`, for a story
task and after a failed pre-check. `status`: `clear` | `unclear`. `by`: `operator` | `docs` |
`assumption` | absent (open). Validation on load: probabilities in [0, 1], `len(checks) == 1 +
len(rounds)`, the first round of kind `clarify`, the closed sets, the byte caps.

**Render** (`domain.Clarification.Render()`, one function for the stage prompts, the re-check's
state and `summary.md`):

```
Clarification (agreed before the run; the task above stays the source):
- Which export format is meant? CSV  [from docs/export.md#Formats]
- Which users get the button? admins only  [operator]
- Assumed: the button is hidden for guests  [assumption]
- Open: Which locale is the date in?
Added by the operator:
  ...
```

The stages that take the task (research, investigate, implement first and retry, review) get the
request through `taskBlock` as today and, after it, the rendered clarification through `fence`;
an empty clarification adds nothing, so existing goldens do not change.

**`decision.json`**: `clarity {threshold, checks, status, reason}` (`status` `disabled` under
`clarify.enabled: false`); `usage.clarify[]`, `usage.clarity_recheck[]`, counted in
`outcome.decision_overhead` and `limits.max_cost_usd`; `policy_hash` covers
`policy.clarify.threshold`.

**Logs and artifacts**: `logs/clarify-<N>.{stdout,stderr}.log`, `artifacts/clarify-<N>.json` (the
prompt as sent, the excerpts' paths and headings, the parsed points), `logs/decide-clarity-<N>.*`
per re-check; N from 1.

**Configuration**:

```yaml
clarify:                 # required: a route, or `clarify: {enabled: false}` written out
  enabled: true          # false: no clarify step (the benchmark); the keys below optional
  provider: claude
  model: sonnet
  effort: medium         # optional
  timeout: 120s
  docs: ["**/*.md"]      # globs over git-listed files; the deny list applies first
  max_bytes: 16384       # rules + excerpts, 1024..131072
  max_rounds: 2          # operator additions after the re-check, 0..5
policy:
  clarify:
    threshold: 0.80      # (0, 1], required; in DecisionPolicy and policy_hash
```

With `enabled: true`, `clarify` is checked in the fail-fast chain like `translation`: the
executable, `Supports(AuthorityNone)`, the CLI version, the org layer's `allowed_models`, and
`provider.CostReporter` under `limits.max_cost_usd`. Rejected with a message naming the
replacement and `specs/clarify`: `policy.stages.thresholds.analysis` (→ `policy.clarify.threshold`)
and `.planning`; `analysis`/`planning` in `policy.stages.force` (repository and org layer) and in
`policy.explore` (→ `investigation`); `actions.analyze`, `actions.plan` (→ `actions.investigate`,
which takes what `actions.plan` took, `by_complexity` included).

**What does not change**: Go decides (the clarify model's points never decide whether the run goes
on, the re-check does); no authority grows (clarify `none`, investigation `read`, implement
`edit`); the decision provider never sees an instruction file; the same decision gives the same
workflow and the same persisted run the same resume.

## Out of scope

- A floor on `policy.clarify.threshold` in the org layer (A4).
- Editing a clarification on the review screen (A5); `resume` taking new input.
- Threshold proposals for `clarity` in `decisions calibrate` (CL08 shows the distribution only,
  A1); recalibrating the other thresholds after the question change (D10).
- A live check of the new question against the real Jev (no access, D11).

## Risks

- `policy.clarify.threshold: 0.80` is uncalibrated: too high stops clear tasks in auto, too low lets
  unclear ones through. Watch the share of `task needs input` runs (CL08's report).
- The rename touches many files; a missed reference to `analyze`/`plan` shows up as a strict-load
  refusal or a failing golden, not silently.
- Old runs with `analyze`/`plan` cannot be resumed after CL01/CL02; finish them first.

## Decisions

Owner, 2026-10-25:

| # | decision |
|---|---|
| D1 | Analysis is about the completeness of the request, never about the code; it does not read the repository. |
| D2 | The clarity check runs before the stage questions; analysis is no longer a stage question. |
| D3 | Clarity is a probability from the decision provider, judged by a threshold in Go. |
| D4 | The decision provider sees text only (request, later request + clarification), never the instructions: Jev has a small context. |
| D5 | The instructions (Markdown by default, a config key lists the files) go only to the clarify call, as excerpts Go selects within a byte limit. |
| D6 | Interactive: the operator answers the questions. Auto: the model analyses the request against the instructions itself. |
| D7 | After clarify the provider is asked again; still unclear: interactive asks for an addition (bounded rounds), auto stops `task needs input` with what is unclear; a new run follows. |
| D8 | `analyze` becomes the clarify step; `planning`/`plan` becomes `investigation`/`investigate` (plan, or the answer without development). |
| D9 | Old names in config or `--stage` are config errors with a message, never reinterpreted. |
| D10 | Thresholds are recalibrated later; `questions_hash` changes on purpose. |
| D11 | Jev answers the new question; with no Jev access, only the mock and the mapper change. |
| D12 | A story task skips clarify; `run --stage` runs it. |
| D13 | A failed clarify call (not auth, which is fatal anyway) fails the run. |
| D14 | `clarify.enabled: false` turns the step off for the benchmark; the clarity question is still asked and recorded. |

Author's defaults, open:

| # | default | alternative |
|---|---|---|
| A1 | `threshold: 0.80`, uncalibrated; calibrate shows the distribution only | none until data exists |
| A4 | The org layer gains no clarify keys | a threshold floor |
| A5 | No edit of the clarification on the review screen | an edit field |
| A6 | `--mode plan` at a terminal is interactive for clarify | auto |

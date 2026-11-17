# Listener requirements: an Epic is understood before it is split

Status: approved, 2026-11-17. Owner's answers R1–R4 (§0); the author's defaults B1–B9 (§10),
confirmed by the owner 2026-11-17.

## Problem

An Epic's plan (specs/listener-story, MVP.md §51.13) surveys every target service, then the
references, then specifies. Seen on PMC-2636 (2026-11-17, three attempts): the target surveys come
first and cost the most ($0.60–1.25 each on opus), the references that hold the logic come last,
and a misunderstanding of that logic is found only after the money is spent. The target surveys
are also repeated: every child ticket's run investigates its own service before it implements.

The owner's flow: the listener first studies the task and the references, understands the
requirements and writes them on the Epic; if anything is unclear it stops and asks; otherwise it
creates the children from the requirements. Studying a target service is its child's run's job.

## 0. The owner's answers (2026-11-17)

| # | answer |
|---|---|
| R1 | The plan studies the Epic and its references first and writes the requirements it understood as a comment on the Epic. |
| R2 | Any open question stops the plan: the Epic goes to `blocked` with the questions; no child is created. |
| R3 | Target services are **not surveyed** by the plan, and not checked out: a child's run studies its own service. No option keeps the old behaviour. |
| R4 | The children are created from the requirements (and the references' surveys); everything else is the child's run's. |

## Approach

```text
plan(Epic):
  text, labels, children check (unchanged, listener-story §9)
  targets: gitlab: labels, else select over the catalog (unchanged, §5)
  checkouts: the references only                      (was: targets and references)
  survey: each reference                              (was: each target, then each reference)
  requirements: one tool-less call (§2)               (new)
  comment on the Epic: the requirements, the questions (§3)
  questions → blocked, stop                           (new)
  specify: text + requirements + reference surveys + the targets by name (§4)
  validate, chain, children, links, the planned comment (unchanged, §8–§9 of listener-story)
```

### Ground rules for the tasks of this story

- A story task, run with `orchestrator story run specs/listener-requirements`. `story run`
  writes this directory's README.md and each task's `## As built`: **do not edit anything under
  `specs/listener-requirements/`**.
- Only LR03 edits the shared documents (MVP.md, CLAUDE.md, README.md, TODO.md,
  `orchestrator.example.yaml`, `specs/listener-story/SPEC.md`'s amendment notes). LR01 and LR02
  say in their commits what LR03 must document.
- The tasks form one chain (LR01 → LR02 → LR03), each stacked on the one before.
- `internal/listener` stays pure (the comment bodies); `internal/action` holds the calls and their
  prompts; `app` the flow. `story plan` / `story run` (MVP.md §48) are not changed: their specify
  keeps its surveys.
- The gate is `scripts/check.sh`.

## 1. Configuration

None. No key is added or removed. `listener.story.max_cost_usd` counts the requirements call;
`actions.investigate` is its route and timeout, as for select, survey and specify.

## 2. The requirements call

`action.Requirements(ctx, call, RequirementsInput{Request, References []SurveyInput}) →
RequirementsResult{Prompt, Output, Usage, Answer}`: tool-less (`AuthorityNone`), on the listener's
`actions.investigate` route. It runs also when the Epic has no reference (B1): the Epic's text alone.

The prompt: the Epic's text fenced as the task (with its discussion, listener-story's rendering),
the references' surveys fenced as untrusted data, and the instruction: state what must be built —
data, dictionaries and their values' source, rules, behaviour per country, UI — as requirements a
developer can implement without reading the references; name what the references show only as
behaviour to reproduce; ask a question only for what neither the Epic nor the references settle
and that changes what is built; no question about code layout, naming or tests, which the
children's runs decide.

The answer, strict JSON, unknown keys refused (`story.ParseRequirements`, B2):

```json
{"summary": "...", "requirements": ["..."], "questions": ["..."]}
```

`summary` 1..4000 bytes; `requirements` 1..30 items, each 1..600 bytes; `questions` 0..10 items,
each 1..600 bytes. Any violation is an `InvalidResponseError` naming the field → the Epic `failed`
with the answer kept (as specify's). The prompt and the answer go in the plan's directory as
`requirements` (artifacts/requirements.json, logs/requirements.stdout.log).

## 3. The comment and the questions

Right after the requirements call, one comment on the Epic (`listener.RequirementsComment`, pure,
golden-tested), its first line `BotMarker` (`[orchestrator]`) so that the next plan's text holds the
discussion that follows it (MVP.md §51.7):

```text
[orchestrator] Requirements of PMC-2636, from the Epic and its references (product-master, …):
<summary>
* <requirement>
…
Open questions — answer them in a comment and move the Epic back to To Do:   (only with questions)
# <question>
…
```

* **No question**: the comment is written (a failure to write it is logged, the plan goes on, B3)
  and the plan goes on to specify. The final "Planned" comment (listener-story §9) follows as today.
* **Any question** (R2): the Epic ends `blocked` with this comment as its outcome's comment (one
  comment, not two), `transitions.blocked`, no attempt counted. `plan.json` records
  `outcome: blocked`, the requirements and the questions.
* **Restart**: a plan cut off after the comment and before the children writes it again on restart
  (B4); the children check (listener-story §9) is unchanged.
* The comment is secret-scanned like the children's descriptions before it is written: a finding
  ends the Epic `failed`, nothing written (B5).

## 4. Specify without target surveys

`action.SpecifyInput` gains `Requirements *story.RequirementsAnswer` and `Notes map[string]string`
(a target's catalog description, or the select's `why`, by name). With `Requirements` set:

* `Surveys` is empty and allowed to be; the prompt says the target repositories were not studied:
  each is known by its name and note; a task describes behaviour and contracts, never files or
  packages; the child's run studies its repository.
* The requirements are fenced after the task, before the reference surveys; every requirement must
  be covered by at least one task (said in the prompt, not checked in Go).
* The rest is as today: `Parallel: true`, `story.Validate`, the per-service chain.

`story plan` never sets `Requirements`: its prompt is byte-identical to today's (a golden test).

## 5. The children

`jira.WikiTask` gains `Requirements []string`: each child's description lists, under
`h3. Requirements of the story`, the requirements (all of them: a child does not know which ones are
its own), between the task's body and the `From the plan` section, inside the existing cut to
`jira.max_bytes / 2` (the requirements are cut first, from the end, with `…` — B6).

## 6. What goes

* `checkouts` of the targets during a plan; `surveys` of the targets; `planRecord`'s target survey
  artifacts (`survey-<target>`). A target's checkout is made by its child's run, as for any ticket.
* listener-story §6's "one survey per target" and §5.3's "every target's checkout": amended.

## 7. Records

`plan.json` gains `requirements` (the answer) and `questions` (a copy, for `blocked`). The plan's
cost includes the requirements call. The log lines: `PMC-1: requirements: 7, questions: 0`,
`PMC-1: blocked by 2 open questions`.

## 8. Testing

* `internal/story`: `ParseRequirements` (table: the bounds, unknown keys, empty lists).
* `internal/action`: the requirements prompt (golden, with and without references); the specify
  prompt with requirements (golden) and without (byte-identical to today's golden).
* `internal/listener`: `RequirementsComment` (golden, with and without questions; escaping).
* `internal/app` (fake Jira, fake provider, as listener-story's): an Epic with two targets and one
  reference → calls `survey` (the reference only), `requirements`, `specify`; no target checkout;
  one requirements comment, then the children, then the planned comment; each child's description
  holds the requirements. An answer with a question → `blocked`, one comment holding the
  questions, no `specify` call, no child. A second plan of that Epic after a human comment → its
  requirements prompt holds that comment. An invalid answer → `failed`, the answer kept. A secret in
  the requirements → `failed`, no comment. The budget stops before specify.
* The end-to-end scenario of listener-story (LS07) is updated: references first, no target survey.

## 9. Changes to the documents (LR03)

MVP.md §51.13 (the plan's steps, its calls and costs), §51.2 (the outcomes: blocked by questions);
`specs/listener-story/SPEC.md`: an "Amended by specs/listener-requirements" note at §5.3, §6, §7.1
and §9; README.md's listener section; CLAUDE.md if it names the plan's steps; TODO.md: "a child
ticket waits for the contract of another service's child" (see Risks).

## Risks

* Contracts between services are written from the requirements, not from the targets' code, so
  they are intentions ("the BE serves the dictionary filtered by country"), not endpoints. A FE child
  running in parallel with its BE child may guess a different shape. Left to the operator: queue
  the FE child after the BE child is merged (`listener.story.queue_children: false` already lets
  the operator choose the order).
* A requirements answer that asks nothing it should have asked: the plan goes on. The comment on
  the Epic is the operator's chance to stop it (a later check, not in this spec).

## 10. The author's defaults (confirmed 2026-11-17)

| # | default (confirmed 2026-11-17) |
|---|---|
| B1 | The requirements call runs also without references, on the Epic's text alone. |
| B2 | The bounds of §2 (summary 4000 bytes, 30 requirements, 10 questions, 600 bytes each). |
| B3 | Failing to write the requirements comment when there is no question does not stop the plan (it is logged); with questions it is the outcome's comment and follows the outcome's rules. |
| B4 | A restarted plan writes the requirements comment again; no de-duplication. |
| B5 | The requirements comment is secret-scanned; a finding fails the Epic. |
| B6 | Every child gets all the requirements, cut first when the description is too long. |
| B7 | The reference surveys stay one per reference, as today (listener-story §6). |
| B8 | The select call (no gitlab: labels) is unchanged and still runs before the references. |
| B9 | No new configuration key. |

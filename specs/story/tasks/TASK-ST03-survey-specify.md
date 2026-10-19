# ST03 — survey and specify: prompts, calls, strict answers

**Size** M–L (3 d) · **Depends on** ST02 · **Blocks** ST04

## Goal

The two planning calls of `SPEC.md` §7 run through the provider registry with fixed
authorities and return validated answers.

## Files

- `internal/action/story.go`: `BuildSurveyPrompt(request, repo)` and
  `BuildSpecifyPrompt(request, surveys, repos, maxTasks)` — the request through `taskBlock`, the
  surveys through `fence`, the rules of §7.3 as fixed text; `Survey(ctx, repoDir, route)` with
  `domain.AuthorityRead`, the repository's deny list (`projectctx.DenyPatterns` + its runs
  directory) and working directory; `Specify(ctx, route)` with `domain.AuthorityNone`; both
  record usage
- `internal/story/answer.go`: `SurveyAnswer` and `SpecifyAnswer` with strict decoding (unknown
  fields, sizes and patterns of §7.2 and §7.4); `SpecifyAnswer.Tasks()` → `[]story.Task` for
  `story.Validate`
- `internal/action/testdata/prompts/`: `survey.txt`, `specify.txt` (golden, fixed nonce)
- survey runs only for the story's services (§7.1), never for every candidate
- routes: the service's `actions.analyze` base route for survey, the home's `actions.plan`
  base route for specify (A2); timeouts from those actions

## Acceptance

- Golden prompts; the request with a forged closing delimiter stays inside its block.
- Unit: every answer error is an `InvalidResponse` naming the field; a specify answer that
  names a repository outside the surveyed set is refused.
- Fake executables: the survey call's argv carries `read` authority's tools and the deny list;
  specify's carries none.

## Gates

`scripts/check.sh`

## As built

2026-10-18. `scripts/check.sh` is the run's test step; it was not run while writing the change.

- `internal/story/answer.go`: `ParseSurvey(obj)` and `ParseSpecify(obj, repos, maxTasks,
  specDirExists)` decode one JSON object strictly (unknown keys, wrong types and trailing data
  refused) and return a `*story.AnswerError{Field, Msg}` naming the field (`slug`,
  `tasks[1].repo`, `relevant_files[3]`; `""` for the answer as a whole). The story package may
  not import `apperr` or do I/O, so the "`<spec_dir>/<slug>` must not exist" check is the
  `specDirExists` callback, and `internal/action` wraps the error into a new
  `*apperr.InvalidResponseError{Call, Repo, Field, Msg, Err}` (exit code 3, like an invalid
  decision answer). apperr had no generic invalid-response type.
- The field rules run before `story.Validate`, so a foreign repository is `tasks[i].repo` and a
  wrong prefix `tasks[i].id`, not a graph message; a cycle is `tasks`, and a task with two bases
  in one repository (`story.Base`, §9.2) is `tasks[i].depends_on`.
- Bounds the spec leaves open, chosen here and stated in the prompts: an interface, constraint or
  path of a survey is at most 1 KiB; a title is one line of at most 200 bytes; "8 KiB for a
  task" is the task's title, goal, acceptance and notes together, and "16 KiB for the spec" its
  five sections together, so a rendered task file stays under `MaxBodyBytes`. `goal` and `notes`
  may not hold a `#`/`##` heading, a `## As built` line or an unclosed code fence: each would
  change what `ParseTaskFile` reads back from the file.
- `SpecifyAnswer.Tasks()` exists as asked, so the answer's task list is the field `TaskAnswers`
  (JSON `tasks`): a Go struct cannot have a field and a method of the same name. `Body` is the
  `## Goal`, `## Acceptance` and `## Notes` sections, and `RenderTaskFile` of a task parses back
  to the same `Task` (tested). Size stays on `AnswerTask` for ST04's `**Size**` line.
- `Survey(ctx, call, repo, request)` and `Specify(ctx, call, in)` take a `PlanningCall`
  (registry, the base route and timeout of `actions.analyze` / `actions.plan`, observer, stream,
  an optional `PlanningLogger` for `logs/survey-<repo>.*` and `logs/specify.*`) instead of a bare
  route, and a `SurveyRepo` carrying the repository's `context.extra_deny` and runs directory.
  Nothing in them reads a config: ST04 loads each service's route and surveys only the story's
  services (§7.1). The results carry the prompt, the raw output (also when the answer is
  refused, for `artifacts/`) and the usage. A timeout applies only when it is set, and is then
  reported as `actions.analyze.timeout` / `actions.plan.timeout`.
- The deny list and the heartbeat moved out of `Dispatcher` into `denyList` and
  `startHeartbeat`, shared with these calls.
- Every survey is fenced with the label `survey` (the repository's name is in the fence's
  description), not `survey-<repo>`: a service name may hold `.`, `_` and `-`, which the
  delimiter should not. List entries go through `oneLine`.
- Goldens: `survey.txt` and `specify.txt`, as named here, are cases of `TestPrompts_Golden` with a
  file-name override (`goldenFile`); `-update` writes them like the `.golden` files. They were
  written by hand, as the change could not run `-update`.
- The fake CLI (`internal/testutil/fakeexec`) routes `ACTION: survey` and `ACTION: specify`;
  `TestPlanningCalls_FakeCLIArgv` runs the real claude adapter against it.
- For ST04: specify runs on `actions.plan`'s provider with `none` authority, which
  `config.ValidateAuthorities` does not check for that route (it checks `read`, plan's own).
  Today only claude supports any authority, and it supports `none`; ST04's fail-fast should
  check it all the same.

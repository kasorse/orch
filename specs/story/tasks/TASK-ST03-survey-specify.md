# ST03 — survey and specify: prompts, calls, strict answers

**Size** M–L (3 d) · **Depends on** ST02 · **Blocks** ST04

## Goal

The two planning calls of `SPEC.md` §7 run through the provider registry with fixed
authorities and return validated answers.

## Files

- `internal/action/story.go`: `BuildSurveyPrompt(request, repo)` and
  `BuildSpecifyPrompt(request, surveys, repos, maxTasks)` — the request through `taskBlock`, the
  surveys through `fence`, the rules of §7.2 as fixed text; `Survey(ctx, repoDir, route)` with
  `domain.AuthorityRead`, the repository's deny list (`projectctx.DenyPatterns` + its runs
  directory) and working directory; `Specify(ctx, route)` with `domain.AuthorityNone`; both
  record usage
- `internal/story/answer.go`: `SurveyAnswer` and `SpecifyAnswer` with strict decoding (unknown
  fields, sizes and patterns of §7.1 and §7.3); `SpecifyAnswer.Tasks()` → `[]story.Task` for
  `story.Validate`
- `internal/action/testdata/prompts/`: `survey.txt`, `specify.txt` (golden, fixed nonce)
- routes: the repository's `actions.analyze` base route for survey, the home's `actions.plan`
  base route for specify (A2); timeouts from those actions

## Acceptance

- Golden prompts; the request with a forged closing delimiter stays inside its block.
- Unit: every answer error is an `InvalidResponse` naming the field; a specify answer that
  names a repository outside the surveyed set is refused.
- Fake executables: the survey call's argv carries `read` authority's tools and the deny list;
  specify's carries none.

## Gates

`scripts/check.sh`

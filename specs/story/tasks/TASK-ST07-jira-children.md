# ST07 — Jira children

**Size** M (2.5 d) · **Depends on** ST06 · **Blocks** ST10

## Goal

`story run` creates the missing child issues once, before the first dispatch (`SPEC.md` §12.2).

## Files

- `internal/jira/create.go`: `(*Client).Create(ctx, Issue) (key string, err)` —
  `POST /rest/api/2/issue` to `base_url` only, no redirects, unexported wire types, the 201
  answer strictly validated (`key` `<PROJECT>-<n>`, the project the one sent), errors
  (`errorMessages`, `errors`) redacted and truncated; retried only on 429/503 with
  `Retry-After`; never after a transport error once the request may have been sent
- `internal/jira/wiki.go`: the task's sections (title, goal, acceptance, notes) rendered as
  wiki markup — headings, lists, code blocks, inline code; text escaped where wiki syntax would
  change its meaning
- `internal/app/story_jira.go`: for each task without `jira` in the story's order: the intent
  saved, `Create`, the key into `story.json` and `story.SetJira` into the task file, the intent
  cleared; an intent without a key on start → the stop of §12.2; `link: parent` or the field;
  components by repository; the project from the parent's key
- MVP.md §39 and §30 notes are ST10's

## Acceptance

- Fake Jira: both link modes in the request body; a 400 with `errors` named in the message, the
  credentials absent from it; a 429 with `Retry-After` retried once; a timeout after the request
  was sent not retried, the intent kept, the next start stopped with the message; a redirect not
  followed; keys written to the task files without touching anything else in them.
- A story without a parent or without `jira.create` sends nothing.

## Gates

`scripts/check.sh`

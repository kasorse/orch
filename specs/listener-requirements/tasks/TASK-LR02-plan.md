---
id: LR02
repo: orch
depends_on: [LR01]
jira: none
---
# LR02 — The plan: references first, the requirements comment, questions block

**Size** L

## Goal

SPEC §3, §5, §6, §7: an Epic's plan checks out and surveys only its references, makes the
requirements call, comments them on the Epic, ends `blocked` on any question, and otherwise
specifies from the requirements and creates children whose descriptions hold them.

## Files

- `internal/listener` (pure): `RequirementsComment(esc, epic, refs, answer) []string` (§3), golden
  files with and without questions.
- `internal/jira/wiki.go`: `WikiTask.Requirements` and its section in the listener's description,
  cut first (§5, B6).
- `internal/app/listen_plan.go`: checkouts and surveys of the references only; the requirements
  call (budget before it, artifacts as every call's); the secret scan and the comment; questions →
  `blocked` with that comment as the outcome's (one comment); specify with `Requirements` and
  `Notes`; `plan.json`'s `requirements` and `questions`; the log lines of §7.
- `internal/app/listen_children.go`: the requirements into every child's `WikiTask`.
- Tests: the scenarios of SPEC §8's `internal/app` list; the existing plan and children tests
  updated for "no target survey".

## Acceptance

- No target is checked out or surveyed during a plan (the fake checkout and provider see none).
- An Epic with a question gets exactly one comment and no child.
- `scripts/check.sh` passes.

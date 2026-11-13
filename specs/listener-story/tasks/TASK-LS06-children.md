---
id: LS06
repo: orch
depends_on: [LS05]
jira: none
---
# LS06 — The children: creation, links, the Epic's comment and outcomes

**Size** M

## Goal

SPEC §8, §9: a validated plan becomes child tickets with their labels and description, the links
of each service's chain, and a comment on the Epic, which stays at `start`.

## Files

- `internal/jira/wiki.go`: `WikiTask` gains the story line, the service, the references' URLs, the
  `Needs:` keys and the plan's approach and contracts, cut to `jira.max_bytes / 2` (§8.2).
- `internal/listener` (pure): `ChildLabels(...)` (§8.1), the Epic's comment bodies (§9), golden
  files.
- `internal/app`: the secret scan of every description before the first POST; the creations and
  then the links with intents in the Epic's entry (`createJiraChild`'s rules); the read-back of one
  child's status (§8.1); the children check before the catalog and the restart label (§9, A9);
  a plan cut off by a stop with an intent in doubt → `failed` naming it (A10); the start-time check
  that the listener's account is in `query.reporters` (A15).
- Tests: three children with their labels and parent, two of one service linked inward/outward the
  right way round; a creation that times out after sending → no second POST, `failed`, the comment
  lists what was created; a refused link → `failed`; an Epic with children → `blocked`; with
  `ai-restart` → a new plan and the label removed; a description with a secret → nothing created.

## Acceptance

- The fake Jira sees each child created exactly once, then each link exactly once.
- A child created by the plan is picked by the next poll as an ordinary ticket (the reporter check
  passes).
- `scripts/check.sh` passes.

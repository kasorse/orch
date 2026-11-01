---
id: AT10
depends_on: [AT07, AT08]
---
# AT10 — authority `edit`: the text editor tool, `apply_patch` (V4A)

**Size** L

## Goal

SPEC.md §5.1 (`edit`): writes through `tools.Root` with `forWrite`. `Supports` becomes true for
`edit`; after this task implement runs over HTTP.

## Files

- `internal/provider/tools/`: `view`/`create`/`str_replace`/`insert` (`str_replace` needs exactly
  one match); a V4A parser and applier for `create_file`/`update_file`/`delete_file`, all-or-nothing
  per operation; writes refused under `.git`, `.claude`, on a deny-listed path, through a symlink.
- Anthropic: `text_editor_20250728` declared without a schema. OpenAI: `apply_patch` and
  `apply_patch_call_output` with `completed`/`failed`.
- Tests: the V4A fixtures (context lines, several hunks, a hunk that does not match, a new file, a
  delete), each refused write; an integration scenario: implement → test → review over HTTP, with
  the guard and the secret scan around it unchanged.
- MVP.md §38.2 and CLAUDE.md (least authority under the API transport).

## Acceptance

- A guard violation planted through an edit (a hook under `.git/hooks`) is refused by the tool, and
  one planted any other way still fails the step through the guard.
- `scripts/check.sh` passes.

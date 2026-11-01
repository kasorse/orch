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

## As built (2026-11-02)

- `tools.EditorTool(root)` runs Anthropic's text editor: `view` (numbered lines, `view_range`
  with -1 for the end, a directory two levels deep), `create` (parents made, permissions of an
  existing file kept), `str_replace` (exactly one match, else the count), `insert` (after line N,
  0 = top); `undo_edit` refused. An edit call gets it with `grep` and `glob`; `read_file` is left
  out beside the editor's `view`. Display names follow the command: Read, Write, Edit.
- `Root.ApplyPatch(PatchOp)` runs OpenAI's operations. The V4A parser and applier are written
  here from the format's description (the Agents SDK's code carries no license): `@@` anchors
  (stacked ones in turn), ` `/`-`/`+` lines, an empty line as empty context, `*** End of File`
  pinning a hunk to the end, any other `***` line ending the diff; hunks apply in order from where
  the previous one ended, each matched exactly, then ignoring trailing space, then ignoring space at
  both ends; a pure addition needs an anchor or `*** End of File`. CRLF and the final-newline state
  are kept. A create_file diff is `+` lines only, and the file gets a final newline (the SDK
  leaves it out; a source file without one trips formatters). Display names: Write, Edit, Delete.
- The codex loop handles `apply_patch_call` items in order with `function_call`s; each gets an
  `apply_patch_call_output` with `completed` or `failed` and the message. `tools.Set` became a
  pointer type that can carry the root apply_patch writes in.
- Both clients now `Supports` every defined authority; `Execute` still refuses an empty or unknown
  one.
- The integration scenario that checked implement's refusal is replaced by one whose whole run -
  translate, decision, investigate, implement (the fix written by the client's editor in the run's
  worktree), test, review - goes over HTTP with no CLI invocation.

---
id: CL04
repo: orche
depends_on: [CL03]
jira: none
---
# CL04 — projectctx.Docs: instruction excerpts for clarify

**Size** S

## Goal

Go selects the instructions the clarify call reads (Approach §3, "The instructions"): rules and
ranked Markdown sections from the files `clarify.docs` matches, behind the deny list, within
`clarify.max_bytes`. Only the selection: nothing calls it yet (CL06 does).

## Files

- `internal/projectctx/docs.go`: `Docs(ctx, root string, opts DocsOptions, request, requestEN
  string) (domain.Instructions, error)`; `docExcerpts` takes the matched file list instead of
  finding `docs/*.md` itself, and the Context Manager keeps calling it with `docs/*.md`.
- `internal/domain`: `Instructions{Rules []string; Excerpts []DocExcerpt; Truncated bool}`
  (`DocExcerpt` exists).
- `internal/config`: the `clarify` section's `docs` (globs with `**`, default `["**/*.md"]`) and
  `max_bytes` (default 16384, 1024..131072). The rest of the section is CL05's; until then the
  section is optional.

## Steps

1. Reuse the `git ls-files` listing, the deny list, `readText` (binaries, size, symlinks) and
   `isVendored`.
2. Match the globs; split sections; rank by `queryTokens`; cut excerpts from the lowest rank, then
   rules from the end; set `Truncated`.

## Acceptance

- A file `secrets/notes.md` and a file `.env.md` are never read by `Docs`.
- A git-ignored `.md` file is not listed, and a symlinked `.md` file is skipped.
- With a budget smaller than everything, the best-ranked excerpt is kept and `Truncated` is set.
- An existing Context Manager test's `project_context.json` output is unchanged.
- An invalid glob in `clarify.docs` and `clarify.max_bytes: 100` are config errors.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/clarify/`.

## As built (2026-10-25)

Run `8eee0b07-a38a-4e6a-8627-5bd3670738ba` in orche: completed, 0 fix iterations, tests passed, no review.
Branch `story/clarify/CL04` on `story/clarify/CL03` @ 233b749, commit 0a7ab49.
Cost $0.69. Implementation summary: Add projectctx.Docs and clarify.docs/max_bytes config.
Conflicts with spec/clarify: internal/action/extensions_test.go.

# CL04 — `projectctx.Docs`: instruction excerpts for clarify

**Size** S (1 d) · **Depends on** — · **Blocks** CL05

## Goal

Go selects the instructions the clarify call reads (`SPEC.md` §5.2): rules and ranked Markdown
sections from the files `clarify.docs` matches, behind the deny list, within `clarify.max_bytes`.

## Files

- `internal/projectctx/docs.go`: `Docs(ctx, root string, opts DocsOptions, request, requestEN
  string) (domain.Instructions, error)`; `docExcerpts` takes the matched file list instead of
  `docs/*.md`; the existing Context Manager keeps calling it with `docs/*.md`, so
  `project_context.json` does not change
- `internal/domain`: `Instructions{Rules []string; Excerpts []DocExcerpt; Truncated bool}`
  (`DocExcerpt` already exists)
- `internal/config`: the `clarify` section's `docs` (globs, default `["**/*.md"]`, each a valid
  `path.Match`-style pattern with `**`) and `max_bytes` (default 16384, 1024..131072)

## Steps

1. Reuse `git ls-files` listing, the deny list, `readText` (binaries, size, symlinks), `isVendored`.
2. Match globs; split sections; rank by `queryTokens`; cut excerpts from the lowest rank, then
   rules from the end; set `Truncated`.

## Acceptance

- Unit: a `secrets/notes.md` and a `.env.md` are never read; a git-ignored `.md` is not listed;
  a symlinked `.md` is skipped.
- Unit: with a budget smaller than everything, the best-ranked excerpt is kept and `Truncated`
  is set.
- Unit: `project_context.json` of an existing Context Manager test is unchanged.

## Gates

`scripts/check.sh`

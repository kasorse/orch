# H03 — Review diff: new files, deny list, summarize, budget

**Size** M (2 d) · **TODO** S1 · **Depends on** H02 · **Blocks** H09

## Goal

The review prompt shows the real change set — new files included — within
`limits.prompt.max_diff_bytes`, never shows a deny-listed file's content, and the review
artifact records what the reviewer saw (`SPEC.md` §5.3).

## Files

- `internal/repository/git.go` (+ `git_real_test.go`): `ReviewDiff`
- `internal/projectctx/secrets.go`: export a matcher (e.g. `DenyMatcher(extra) func(rel string) bool`)
  if `denyList.denied` is not reachable; `repository` must not import `projectctx` — pass the
  matcher in through `ReviewDiffOptions`
- `internal/action/review.go`, `prompts.go` (+ tests)
- `internal/domain/artifact.go`: `ReviewResult.Diff` (or a sibling field on the saved artifact)

## Steps

1. Types in `repository`:
   ```go
   type ReviewDiffOptions struct {
       Exclude     []string           // runs dir
       Denied      func(string) bool  // secret deny list
       Summarize   []string           // default list + limits.prompt.diff_summarize
       MaxBytes    int
       MaxUntracked int               // 500
   }
   type FileChange struct { Path string; Added, Deleted int; Untracked bool; Treatment string }
   type ReviewDiff struct { Text string; Files []FileChange; Truncated bool }
   ```
   Treatments: `full`, `summarized`, `withheld`, `binary`, `symlink`, `budget`.
2. Tracked: `git diff HEAD --numstat -z -- . :(exclude)...` for counts (`-` = binary), and
   `git diff HEAD -- ...` split at lines starting with `diff --git ` into per-file chunks.
   Map chunks to paths through the numstat order (renames: use the new path; numstat -z
   gives `\0old\0new`).
3. Untracked: `git ls-files --others --exclude-standard -z -- . :(exclude)...`. For each,
   `os.Lstat`: symlink → `symlink`; read the first 8 KiB: NUL → `binary`; otherwise full
   content via `git diff --no-index -- /dev/null <path>` (exit 0 or 1 = success). Count added
   lines for the header. Beyond `MaxUntracked`, stop listing and add `... and N more`.
4. Precedence per `SPEC.md` §5.3 table: deny list → symlink → binary → summarize → full.
   Summarize globs: base-name match without `/`, path match with `/`.
5. Assembly: header (every file: `path  +A -D  treatment`), then full chunks in git order while
   the running total fits `MaxBytes`; a chunk that does not fit → treatment `budget`; continue
   with later (smaller) chunks. `Truncated` = any `budget`.
6. `executeReview` uses `ReviewDiff` (the old `Diff` stays for other callers). The empty-diff
   check uses `len(Files) == 0`. The prompt gains a short paragraph: which files were not
   shown in full and that the reviewer may Read them (not the withheld ones — they are denied).
7. Persist on the review artifact: `diff: {bytes, files, shown, summarized, withheld, truncated}`.
   `persistence` validation accepts it (counts ≥ 0).

## Acceptance

Real-git tests in a temp repo:
- a new untracked `.go` file → its content is in the diff (`+` lines);
- untracked binary, symlink, `.env`, `certs/server.pem`, and a tracked `secrets.yaml` modified
  → name only, with the right treatment;
- `go.sum` and `web/app.min.js` → stat line; `go.mod` → full;
- a 300 KiB tracked change with `MaxBytes` 64 KiB → `budget` on it, a later small file still
  `full`, `Truncated` true, header lists all files;
- 600 untracked files → 500 listed + `... and 100 more`;
- the runs directory never appears;
- a rename shows under its new path.
Review prompt golden test updated; review artifact carries `diff`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-05. As planned, with these differences:

- **New files are rendered in Go, not by `git diff --no-index`.** One git process per
  untracked file (up to 500) was needless, and the patch is trivial to produce: header,
  `new file mode 100644|100755`, one `@@ -0,0 +1,N @@` hunk, `\ No newline at end of file`
  when it applies. An untracked file over 16 MiB is not read: listed as `not shown: diff
  budget` (or by its deny-list / binary / summarize treatment).
- **The diff commands run with `--no-color --no-ext-diff --no-textconv`**, so a diff driver
  or textconv filter configured in the repository never runs and the reviewer sees the bytes
  on disk.
- **The file list is bounded too**: it gets at most half of `max_diff_bytes`; files beyond
  that (and beyond 500 untracked) are counted in the heading as `... and N more files,
  neither listed nor shown` (`ReviewDiff.Unlisted`) and make the diff `truncated`.
- **The summary lives on `domain.ReviewResult.Diff`** (`ReviewDiffSummary`), so `run.json`
  and the artifact carry the same value and `Restore`'s artifact comparison still holds. A
  `diff` field the model writes is discarded before validation, so it cannot fail the review;
  `ReviewResult.Validate` checks the counts (non-negative, shown + summarized + withheld ≤
  files) on anything read back.
- **`Repository.Diff` is removed**: review was its only caller. Its unit tests became
  `TestReviewDiff_GitFailure` and `TestReviewDiff_ExcludesPathsAndExternalDrivers`.
- `projectctx.DenyMatcher(extra)` exposes the Context Manager's own matcher (including its
  `.git` / `.orchestrator` directory rule); `repository` takes it as a function and imports
  nothing new. The default summarize list is `action.DefaultDiffSummarize`.
- Integration scenario 4 (`TestDecision_ContextFailureContinuesLimited`) failed every
  `git ls-files`; it now fails only the Context Manager's listing (`--cached`), since review
  lists untracked files with `ls-files` too.

# DL09 — `review --mr` and `--comment`

**Size** M (2 d) · **Depends on** DL03, DL08 · **Blocks** DL10, DL11

## Goal

Review a GitLab merge request by iid or URL, and optionally post `report.md` as one note
(`SPEC.md` §12.4).

## Files

- `internal/gitlab/ref.go`: `ParseMRRef(arg, baseURL, project) (iid int, err)` — a number, `!12`, or
  a URL on `base_url`'s host for the remote's project; anything else a usage error.
- `internal/app/review.go`: fail-fast (`GitLab: true`, `Project`), `MR(iid)` (fork and non-open
  refused), the operator fingerprint, `Repository.FetchMR` (explicit refspecs into
  `refs/orchestrator/review/<run-id>/{head,base}`, `--no-tags`), `head == diff_refs.head_sha`, base =
  `diff_refs.base_sha`; the task from the title and description (bounded 32 KiB); `review_target.mr_iid`.
- `--comment`: after a terminal status with at least one step, the intent, `CreateNote(report.md)`,
  `mr_note`; a failure is a warning.

## Acceptance

- Fake GitLab + a bare "remote" repository with `refs/merge-requests/7/head`: the review runs on
  that head; a moved head → the error; a fork → refused; a URL for another project → usage error,
  no request.
- `--comment`: exactly one note with the escaped report; a timeout after sending → no second POST.
- Live check step 4 (operator).

## Gates

`scripts/check.sh`; the live check (operator).

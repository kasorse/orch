# T01 — `domain.Policy` and the fixed action→policy table

**Phase** 1 · **Size** S–M · **Depends on** nothing · **Blocks** T02, T03, T06, T14

## Goal

A provider-neutral description of "what one AI call is allowed to do", plus the pure
function that derives it from `(action, mode, repoRoot)`. No provider knowledge, no config
import, no I/O — this is the same kind of fixed table as `workflow.Build`, and it must be
testable with nothing but a struct literal.

## Files

- `internal/domain/policy.go` — **new**
- `internal/domain/policy_test.go` — **new**

## What to do

1. Define `Approval` (`ApprovalDeny`, `ApprovalAsk`) and `Policy` exactly as
   `SPEC.md` §8.1 specifies. Every field gets a comment saying whether it is mandatory or
   advisory, because that distinction is the whole contract with T02's `Supports`.
2. `Policy.Validate() error` rejects incoherent combinations:
   - `WriteFiles` without `ReadFiles`
   - `ExecAllowlist` non-empty with `Exec == false`
   - `WriteFiles` with empty `WriteRoots`
   - `Bypass` together with any other restriction or with `ApprovalAsk`
   - `WriteRoots` entries that are not absolute paths
3. `PolicyFor(action ActionType, mode RunMode, repoRoot string) Policy` implements §8.2's
   table and §8.3's mode mapping. Drive it from a package-level table value, not a `switch`
   — T14's `PolicyTableMarkdown` must render from the same data, and a `switch` cannot be
   walked.
   - `classify`: nothing. `analyze`/`plan`/`review`/`answer`: read only.
   - `implement`: read + write, `WriteRoots = [repoRoot]`, no exec.
   - `InheritAgentConfig: false` for every action.
   - `ModeManual` → `ApprovalAsk`; `ModeAcceptEdits`/`ModeAuto` → `ApprovalDeny`.
   - `ModePlan` never executes, so it must not silently produce a permissive policy —
     return the same policy as `accept-edits` and let `app.go` keep short-circuiting.
4. `classify` is not in `ActionType` (it is not a workflow action). Add it as a distinct
   constant used only for policy lookup, or give `PolicyFor` a sibling
   `ClassifyPolicy()` — pick one and say why in a comment. Do **not** let a typo'd or
   unknown action fall through to a permissive default: an unrecognized action returns the
   most restrictive policy (nothing) so a future action added without a table row fails
   closed rather than inheriting write access.
5. `ExecAllowlist` is populated from config, which `domain` must not import. `PolicyFor`
   therefore returns `implement` with an empty allowlist, and T05/T06 apply
   `security.implement_exec_allowlist` on top via a small explicit method
   (`Policy.WithExecAllowlist([]string) Policy`) that sets `Exec = true` only when the
   list is non-empty.

## Acceptance

- Table test over all seven actions × four modes asserting every field, so the table is
  pinned against accidental widening in review.
- `Validate` rejects each incoherent case above, each as its own named subtest.
- An unknown `ActionType` yields a policy with no capabilities.
- `PolicyFor` is deterministic and free of side effects: calling it twice with the same
  arguments returns an identical value (guards against a shared slice being mutated by a
  caller — `WriteRoots` and `ExecAllowlist` must be freshly allocated per call).
- `internal/domain` still imports nothing from `config`, `provider`, or `workflow`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

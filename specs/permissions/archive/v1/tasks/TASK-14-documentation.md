# T14 — Documentation: MVP.md §36, README security model, CLAUDE.md invariants, drift test

**Phase** 5 (but see the landing schedule) · **Size** M · **Depends on** T01–T12 ·
**Blocks** nothing

Per `SPEC.md` §10 this is a **required deliverable, not a follow-up**. Per the Q9 resolution
MVP.md §36 is normative and this spec folder is the design record.

## Landing schedule

Documentation must not describe authority the code does not yet enforce, so it lands in
pieces along the phases rather than all at the end:

| fragment | lands with |
|---|---|
| MVP.md §36 (table, mode mapping, fail-closed rule, ordering), §22 domain types | phase 1 (T01–T06, T12) |
| `PolicyTableMarkdown` + `TestDocsPolicyTableCurrent` | phase 1 |
| README "Security model" §, `orchestrator.example.yaml` `security:` block | phase 2 (after T07) |
| CLAUDE.md invariants | phase 2 |
| MVP.md §25 (approval-wait deadline), §27 (authority block) | phases 3–4 (T10, T13) |
| §12.1 matrix updates | continuously, per verification |

## What to do

1. **MVP.md §36 "Execution authority and permission policy"** — normative: `domain.Policy`
   and the mandatory/advisory split; the action→policy table between
   `<!-- BEGIN policy-table -->` / `<!-- END policy-table -->` markers; the mode→approval
   mapping; the fail-closed rule and its position in the fail-fast ordering, cross-referenced
   from §7; the escape hatch and its preconditions; the audit record. Update §13–15 (adapters
   declare capabilities and translate policy), §22 (new domain types), §25 (approval waits are
   excluded from `execution.timeout`), §27 (authority block in output), §30 (policy DSL and
   OS-level sandboxing are out of scope; add the deferred worktree-isolation option from
   `DECISIONS.md` Q8).
2. **README "## Security model"**, after "## Configuration", before "## Usage", written for an
   operator deciding whether to run this on a given repository. Per §10.2: the one-paragraph
   model; the authority table (same markers); what each mode actually permits, **explicitly
   correcting** the prior behavior where mode did not affect authority; the
   `--dangerously-bypass-permissions` precondition; the environment allowlist and how to
   extend it; where to look afterwards (`policy.json`, `run.json` step policies,
   `logs/approvals.log`); the "repository you do not fully trust" checklist.
3. **"What is not protected"** — the section that does the most good, so do not soften it:
   - Prompt injection is mitigated, not eliminated (fencing is not a boundary).
   - No OS-level sandbox. Isolation is detectable and requestable; providing it is the
     operator's job.
   - `test.command` is shell-executed trusted config, with T09's provenance rules as the only
     check.
   - **`HOME` is forwarded**, so on-disk credentials (`~/.aws/credentials`, `~/.ssh`,
     `~/.config/gh`) are reachable by any step that can read files. The env allowlist removes
     the easy path, not every path; what makes it inert is no-exec/no-network. This is the
     limit an operator is most likely to get wrong (see `DECISIONS.md` Q2).
   - A hostile provider CLI binary is out of the threat model.
4. **CLAUDE.md invariants** under "Non-negotiable invariants", per §10.3: no
   `bypassPermissions` outside the §8.4 opt-in; authority comes from `domain.PolicyFor`, a
   fixed table, never the model / never a provider default / never configurable per action;
   translation lives only in `internal/provider/<name>/buildArgs`; an adapter that cannot
   enforce a mandatory field must refuse — silent downgrade is the one unacceptable failure
   mode. Add `policy.json` and `logs/approvals.log` to the "Run artifacts" list.
5. **`orchestrator.example.yaml`** — the `security:` block with §9's comments,
   `implement_exec_allowlist` shown **empty** with the opt-in example commented out, so copying
   the example never widens authority. (T04 already re-pointed the action routes at `claude`.)
6. **`domain.PolicyTableMarkdown()`** renders the table from the same data `PolicyFor` uses
   (T01 step 3 made this possible), and `TestDocsPolicyTableCurrent` reads `README.md` and
   `MVP.md`, extracts the marked region, and fails on any difference. A stale security table
   is a real defect; this makes it a failing test rather than a discovery months later.

## Acceptance

- `TestDocsPolicyTableCurrent` passes, **and fails** when the code table is edited without the
  docs (assert the failure mode too — a drift test that cannot fail is decoration).
- An operator reading only `README.md` can state correctly what `implement` is allowed to do.
  This is §10's stated acceptance bar; have someone who did not write the code try it.
- MVP.md §36 exists and §7 cross-references the policy check's position in the ordering.
- CLAUDE.md carries all four invariants and both new artifacts.
- Copying `orchestrator.example.yaml` yields a config that runs and grants no exec.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

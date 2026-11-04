---
id: EB04
repo: orche
depends_on: [EB03]
jira: none
---
# EB04 — `examples/embed/`, MVP.md §53, the summaries

**Size** S

## Goal

SPEC.md §5 and §9: the example service the root module compiles, the README an operator follows
on the platform, and the documentation that needs every task done.

## Files

- `examples/embed/main.go`: `Embedded` first, `signal.NotifyContext`, `Listen`; the two
  platform lines in a comment. No import outside the module and the standard library.
- `examples/embed/README.md`: the service repository's files as SPEC §5 lists them (`main.go` on
  `bootstrap`, the `Dockerfile` on the Go build image plus `bubblewrap`, the `.gitlab-ci.yml`
  with the platform includes, the deployment values with `secretKeys`, one replica, the
  persistent volume, resources and the stop timeout, `listener.yaml`, `default.yaml`,
  `/etc/orchestrator/org.yaml`); corporate names as placeholders. The host's duties of §4.3
  repeated as a checklist.
- `examples/embed/embed_test.go` (or `examples/ci/ci_test.go`'s pattern): the README's YAML
  blocks parse; `main.go` compiles (`go vet`).
- MVP.md §53 completed: the package, `Embedded`, the host's duties, the example, what is not
  covered (§10); §30 (out of scope) names the library's limits.
- CLAUDE.md "Out of scope" line; TODO.md P14 status; this spec's README `Status:` with the
  operator's live check recorded when it has run.

## Acceptance

- `examples/embed/main.go` compiles under `go vet ./...`, imports nothing outside the module and the standard library, and calls `Embedded` before `Listen` under a `signal.NotifyContext`.
- `examples/embed/README.md` lists the service repository's files of SPEC §5 with corporate names as placeholders and repeats the host's duties of §4.3 as a checklist; a test asserts its YAML blocks parse.
- MVP.md §53 answers, without this spec, how a service embeds the listener and what it must provide; §30 and CLAUDE.md's "Out of scope" name the library's limits; `TODO.md` P14 is ticked; `scripts/check.sh` passes.

## As built (2026-11-04)

Run `11cf4a7c-a722-4e87-95c2-3cea42c2c29f` in orche: completed, 0 fix iterations, tests passed, no review.
Branch `story/embed/EB04` on `story/embed/EB03.2` @ 7893f89, commit bcb7ffe.
Cost $0.71. Implementation summary: Add examples/embed service, README and test; complete MVP.md §53.
Conflicts with spec/embed: none.

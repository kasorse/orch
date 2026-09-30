# Task from a Jira ticket

Status: **in progress**. The design questions are answered (`SPEC.md` §18 Q1–Q7). The
tasks assume the defaults D1–D7 in `SPEC.md` §18.

J01 is done; J02–J08 remain.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification: findings, principles, CLI, config, fetch, rendering, persistence, security, errors, tests, decisions |
| [`tasks/`](tasks/) | 8 implementation tasks |

When J08 is done, MVP.md §39 is the normative home, and this folder is the design record.

## The problem in one paragraph

Tasks live in Jira, and today the operator copies each one into `orchestrator run "<task>"`
by hand. The fix is `orchestrator run --jira PMC-2789 ["notes"]`, which makes one read-only
GET to the Jira REST API (Cloud or Data Center). The fetch happens after the fail-fast
chain and before the run exists. It renders the issue (summary, type, parent,
description, configured text fields, operator notes) into the task text deterministically,
then the run carries on exactly as if that text had been typed. Resume never contacts
Jira. Credentials come only from the environment, go only to `jira.base_url`, and are
scrubbed, together with the Jev key, from every child process. That closes an existing
gap where the test command could read them.

## Tasks

| task | title | size | depends on |
|---|---|---|---|
| [J01](tasks/TASK-J01-config.md) | `jira` config section, validation, `DependencyOptions` ✓ | S–M | — |
| [J02](tasks/TASK-J02-client.md) | `internal/jira` client: request, retries, validation, `JiraError`, redaction | M | J01, J05 |
| [J03](tasks/TASK-J03-parse-and-render.md) | `ParseRef`, `Render`, golden files | S | J05 |
| [J04](tasks/TASK-J04-scrub-credentials.md) | child processes lose the Jira and Jev credential variables | S | J01 |
| [J05](tasks/TASK-J05-domain-and-persistence.md) | `Ticket`, `TaskSource`, load validation, `ticket.json` | S | — |
| [J06](tasks/TASK-J06-run-flag.md) | `run --jira`: CLI, `taskFromJira`, console output, exit codes, resume line | M | J01–J05 |
| [J07](tasks/TASK-J07-integration.md) | integration scenarios 19a–19h | M | J06 |
| [J08](tasks/TASK-J08-documentation.md) | MVP.md §39 and the amendments, CLAUDE.md, README, example config | S | all |

```
J05 ──┬── J02 ──┐
      └── J03 ──┤
J01 ──┬── J02   ├── J06 ── J07 ── J08
      └── J04 ──┘
```

Estimate: 5.5–6 engineering days. J01, J05 and J03 can start in parallel (J03 needs only
J05's `domain.Ticket`). J04 is independent of the Jira feature itself, so it can ship
first as a fix for `JEV_API_KEY`.

## Ground rules for every task

- `internal/jira` imports only `domain`, `apperr` and the standard library. It does not
  import `config`: `jira.Options` mirrors the transport fields, as `jev.Options` does.
- `internal/workflow`, `internal/policy` and `internal/action` do not change. No stage
  learns where the task came from (J1).
- Nothing makes a network request before the whole fail-fast chain and `ParseRef` have
  passed. `resume` never builds a Jira client.
- A credential value never appears in an error, a log, `run.json`, `ticket.json` or
  stdout.
- Tests use `httptest` on loopback only: no network, no real Jira, no real token.
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...`, and
  `gofmt -l .` empty. Existing integration scenarios 1–18 keep passing unchanged.

## Deferred

See `SPEC.md` §19: an outcome comment or transition, comments and linked issues as
sections, fencing the task block in prompts, the Data Center Epic Link.

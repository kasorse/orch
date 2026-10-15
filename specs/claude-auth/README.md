# Claude credentials: API key or the CLI's login

Status: **done** (2026-10-15): CA01–CA09 done. `scripts/check.sh` (root and `bench/`) passed
over all of them, and the live checks of CA07 passed against a real key through the owner's
gateway (`specs/permissions/DECISIONS.md` D17). The owner answered Q1–Q5 (`SPEC.md` §15). The
author's defaults D1–D9 are in `SPEC.md` §16 and §17.7; D4 changed after CA07 (an auth
`api_retry` stops the call live). What was built differently, and why, is in each task's
"As built" section.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 9 implementation tasks |

## The problem in one paragraph

Every step shells out to `claude --print`, and the CLI picks the credential from whatever
environment it inherits: an `ANTHROPIC_API_KEY` left in a shell silently moves every run to
per-token billing, and nothing can make sure the key *is* used. This spec adds one mode per
process: `api` (a Console key from the variable `providers.claude.api_key_env` names) or
`login` (the CLI's own login), chosen by `--claude-auth`, then `providers.claude.auth`, then
`auto` (key set → `api`). The adapter builds the child's environment for the mode, checks the
`apiKeySource` the CLI reports after every call, and makes an authentication failure fatal.
Argv, authorities, the guard and resume semantics are unchanged; a resume may switch modes.

## Tasks

| task | title | size | depends on | state |
|---|---|---|---|---|
| [CA01](tasks/TASK-CA01-config.md) | `providers.claude` section, key variable as a credential | S | — | done |
| [CA02](tasks/TASK-CA02-resolve-and-flag.md) | `ResolveClaudeAuth`, `--claude-auth`, console, `validate` | S | CA01 | done |
| [CA03](tasks/TASK-CA03-adapter.md) | adapter: environment per mode, `apiKeySource` check, `FailureAuth` | M | CA02 | done |
| [CA04](tasks/TASK-CA04-fatal-auth.md) | translator and decision provider do not swallow `FailureAuth` | S | CA03 | done |
| [CA05](tasks/TASK-CA05-persistence.md) | `run.json`, `resumes[]`, `decision.json`, `summary.md` | S | CA03 | done |
| [CA06](tasks/TASK-CA06-integration.md) | integration scenarios (fake CLI, `FAKE_ENV_DIR`) | M | CA04, CA05 | done |
| [CA07](tasks/TASK-CA07-live-check.md) | live check under `-tags security` (needs a key; Q5) | S | CA03 | done (live, 2026-10-15, D17) |
| [CA08](tasks/TASK-CA08-documentation.md) | MVP.md §46, CLAUDE.md, README, TODO.md | S | all | done |
| [CA09](tasks/TASK-CA09-api-env.md) | `providers.claude.api_env`: the api mode's own environment | M | CA03, CA05 | done |

```
CA01 ── CA02 ── CA03 ──┬── CA04 ──┬── CA06 ── CA08
                       ├── CA05 ──┘
                       ├── CA07
                       └── CA09 (also after CA05)
```

Estimate: about 4 engineering days.

## Ground rules for every task

- Credential logic lives in `internal/provider/claude` (environment, `apiKeySource`) and
  `internal/config` (resolution); `workflow`, `policy`, `action` and `domain.Authority` learn
  nothing about it.
- `buildArgs` and every authority's flag set are unchanged; no `--bare`.
- The key never appears in argv, config, `run.json`, `decision.json`, logs, prompts or an error;
  only the claude adapter's `Env` carries it. Tests build key-shaped values at run time.
- Without `providers` and without the flag, a run with `ANTHROPIC_API_KEY` unset is
  byte-for-byte as before apart from the new `run.json` field; integration scenarios keep
  passing.
- Each task ends green on `scripts/check.sh` (root and `bench/`).

# Spec: How the orchestrator authenticates `claude` (API key or the CLI's login)

Status: **specified** (2026-10-14), not started. The owner's answers are in §15; the author's
defaults are in §16 and stand unless the owner changes them.
Scope: every call to the `claude` CLI (workflow steps, the translator, the LLM decision
provider) runs under one credential mode per process: **`api`** (a Claude Console API key
from an environment variable the config names, billed per token) or **`login`** (whatever
the CLI itself is logged in with: a claude.ai subscription, `CLAUDE_CODE_OAUTH_TOKEN`, a
Console login, a cloud provider). The mode comes from `--claude-auth`, then
`providers.claude.auth`, then `auto` (key set → `api`, otherwise `login`). The CLI, its
flags, authorities, the guard and everything trusted stay the same.
Extends MVP.md §5–§7 (config, fail-fast), §31.1 (static routing), §37 (resume), §38
(permissions), §41 (hardening), §43 (summary). A direct Messages API client in Go is **not**
part of this spec (§14, TODO.md P12).

---

## 1. Problem

Every call shells out to `claude --print`. Which account pays is decided by the CLI, from the
environment the orchestrator happens to inherit:

- `execution.ScrubbedEnv()` passes `ANTHROPIC_API_KEY` through. In `-p` mode the CLI **always**
  uses it when present (official docs, §2), ahead of the subscription login. A key left in a
  shell profile silently moves every run to per-token billing, and nothing in `run.json`
  says so.
- The reverse cannot be asked for: there is no way to say "use the API key" and be sure the
  CLI did. `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_USE_BEDROCK`/`VERTEX`/`FOUNDRY`, a gateway
  session or a managed `forceLoginMethod` all outrank or block the key.
- An operator who hits the subscription's usage window mid-run has no switch to finish the
  run on an API key.

## 2. What the official sources say (checked 2026-10-14, claude 2.1.299)

From `code.claude.com/docs/en/authentication` ("Authentication precedence"):

1. Cloud provider credentials when `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX` or
   `CLAUDE_CODE_USE_FOUNDRY` is set (a signed-in Claude apps gateway session outranks even
   these).
2. `ANTHROPIC_AUTH_TOKEN` (`Authorization: Bearer`, for LLM gateways).
3. `ANTHROPIC_API_KEY` (`X-Api-Key`). "In non-interactive mode (`-p`), the key is always used
   when present."
4. `apiKeyHelper` script output (a settings key; only with user settings loaded here).
5. `CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`, one-year subscription token).
6. Anthropic profiles / federation (`ANTHROPIC_PROFILE`, `ANTHROPIC_FEDERATION_RULE_ID` +
   `ANTHROPIC_ORGANIZATION_ID`).
7. Subscription OAuth from `/login`.

Managed settings with `forceLoginMethod`/`forceLoginOrgUUID` **block** `ANTHROPIC_API_KEY` at
startup.

From `code.claude.com/docs/en/headless`: `--bare` never reads OAuth or the keychain and takes
only `ANTHROPIC_API_KEY` or an `apiKeyHelper`; it also skips CLAUDE.md, hooks, skills, plugins
and MCP auto-discovery, and "will become the default for `-p` in a future release".

From the Agent SDK reference (`SDKSystemMessage`, `ApiKeySource`): the stream-json `system/init`
event carries `apiKeySource`, one of `ANTHROPIC_API_KEY`, `apiKeyHelper`,
`/login managed key`, `none` ("authenticates another way, such as a claude.ai login, a bearer
token, or a cloud provider"). Retryable API failures emit `system/api_retry` with `error` in
`authentication_failed`, `oauth_org_not_allowed`, `account_on_hold`, `billing_error`, … .

The Agent SDK exists only for Python and TypeScript and runs the same CLI underneath, so it
adds a runtime and nothing this spec needs.

## 3. Principles

1. **One mode per process, decided before any call.** Resolution is part of the fail-fast
   chain (MVP.md §7). An explicit `api` without a usable key is a config error before the
   translator, Jev or any CLI runs.
2. **The orchestrator sets the credential; the CLI's own report is the proof.** The adapter
   builds the child's environment for the mode (§7), and the `apiKeySource` the CLI reports is
   checked against the mode after every call (§8). A mismatch fails, never warns: billing the
   wrong account is the one silent failure this spec exists to remove.
3. **Nothing else changes.** Same executable, same argv per authority (`buildArgs` is
   untouched), same version range, guard, sandbox, prompts, routing. No `--bare`
   (§16 D3).
4. **The key is a secret like the Jev key.** Environment only, never in argv, config,
   `run.json`, logs, prompts or a non-claude child.
5. **Static, not adaptive.** The mode is chosen by the operator or by a fixed rule over the
   environment; a failure never switches modes by itself (MVP.md §31.1: no fallback).

## 4. Modes and resolution

`requested` = the `--claude-auth` flag if given, else `providers.claude.auth`, else `auto`.

| requested | key variable (`api_key_env`) | effective mode | console |
|---|---|---|---|
| `api` | set, valid | `api` | `claude auth: api key ($VAR)` |
| `api` | unset, blank or invalid (§12) | — config error, no run | |
| `auto` | set, valid | `api` | `claude auth: api key ($VAR, auto)` |
| `auto` | unset | `login` | `claude auth: CLI login (auto: $VAR not set)` |
| `auto` | set but blank | `login` | warning: `$VAR is set but empty; using the CLI login` |
| `auto` | set, invalid (§12) | — config error, no run | |
| `login` | anything | `login` | if set: `$VAR is set but not used (--claude-auth login)` |

Resolution is one function in `config` over the config, the flag and an env lookup
(`ResolveClaudeAuth`), called inside `ValidateDependencies` so `validate` answers the same
question as `run`. Its result `{requested, mode, decided_by: flag|config|default, key_env}` is
handed to the claude adapter by `app` before the first call (§16 D5) and persisted (§10).

## 5. Configuration

```yaml
providers:              # new, optional; absent = auth: auto, api_key_env: ANTHROPIC_API_KEY
  claude:
    auth: auto          # auto | api | login
    api_key_env: ANTHROPIC_API_KEY   # the variable that holds the key; never the key itself
    api_env:            # optional (§17): set for the child in api mode only, never a secret
      ANTHROPIC_BASE_URL: https://gateway.example
```

- Parsed strictly (version 2). `providers.claude.api_key` (a literal) is declared in the
  schema and rejected with "name an environment variable via providers.claude.api_key_env
  instead", like `decision.jev.api_key`.
- `api_key_env` must match the env-name pattern, must not be a parent-session variable
  (`execution.IsParentSessionVar`) and joins `Config.CredentialEnvNames()`: every child process
  loses it through `ScrubbedEnv()`, `test.env_pass` refuses it, and only the claude adapter
  puts the value back, under the name `ANTHROPIC_API_KEY` (§7).
- `providers.codex`/`providers.gemini` do not exist (they support no authority); the section
  takes only `claude`.
- The org layer (`org.yaml`) gets no key in this spec (§15 Q4, §16 D7).

## 6. CLI

```
orchestrator [--config path] [--mode …] [--claude-auth auto|api|login] run …
orchestrator [--claude-auth …] resume <run-id>
orchestrator [--claude-auth …] validate
```

- Parsed like `--mode`: any position, `--claude-auth X` or `--claude-auth=X`, once. An unknown
  value or a second occurrence is a usage error. On any other command it is a usage error
  ("--claude-auth is only valid with run, resume and validate").
- Combines with `--stage`, `--jira`, `--allow-red-tests` with no interaction.

## 7. The child's environment per mode

Built in the claude adapter only (`internal/provider/claude`), starting from
`execution.ScrubbedEnv()` (which already lacks `api_key_env` and the other credentials):

| variable | `api` | `login` |
|---|---|---|
| `ANTHROPIC_API_KEY` | **set** to the key | removed |
| `ANTHROPIC_AUTH_TOKEN` | removed (outranks the key) | inherited |
| `CLAUDE_CODE_USE_BEDROCK`, `_VERTEX`, `_FOUNDRY` | removed (outrank the key) | inherited |
| `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_PROFILE`, `ANTHROPIC_FEDERATION_RULE_ID` | removed | inherited |
| `ANTHROPIC_BASE_URL` | inherited, warned once (§13 #12) | inherited |
| each name in `providers.claude.api_env` (§17) | **set** to the configured value | removed |
| everything else | inherited, as today | inherited, as today |

`login` means "the CLI's own credentials, never an API key from the orchestrator's
environment": a gateway token or a cloud provider the operator set up keeps working there.
The same environment goes to the `claude --version` probe (harmless, and one code path). The
variables removed in `api` that were set are named once on the console
(`ignored in api mode: ANTHROPIC_AUTH_TOKEN`), never their values.

## 8. Checking what the CLI actually used

`parseStream` (`internal/provider/claude/stream.go`) reads `apiKeySource` from the
`system/init` event. After the call, the adapter compares it with the mode:

| mode | `apiKeySource` | result |
|---|---|---|
| `api` | `ANTHROPIC_API_KEY` | ok |
| `api` | anything else (`none`: a gateway session or `forceLoginMethod`; `apiKeyHelper`; …) | the call fails, `FailureAuth` (§9): `claude did not use the API key (it reported "none"); a gateway sign-in or managed settings override it` |
| `login` | `ANTHROPIC_API_KEY` | the call fails, `FailureAuth` (defensive: §7 removed it) |
| `login` | anything else | ok, recorded |
| any | no init event (not stream-json: a test double, a CLI outside the range) | not checked, recorded as `""` |

The check runs after the process ends, so the one call that reveals a mismatch has already
run (§16 D4). Since a mismatch is fatal (§9), it is at most one call, usually the translator's.

## 9. Authentication failures stop the run

A new `apperr` failure kind `FailureAuth` covers: the mismatch in §8, and a call whose stream
carries a `system/api_retry` event with `error` in `authentication_failed`,
`oauth_org_not_allowed`, `account_on_hold`, `billing_error`, or whose result event is an error
and whose stream had one of those. It is **fatal everywhere**:

- the translator does not fall back to the original text with a warning;
- the LLM decision provider does not lead to the manual choice (`on_failure: ask`);
- a workflow step fails the run (as any provider error already does; no retry, no fix loop).

The message names the mode and how it was chosen, and what to try:
`claude auth (api, from --claude-auth): billing_error. Check the Console account, or run with
--claude-auth login`. `resume` with another `--claude-auth` continues the run (§11).

Rate limits (`rate_limit`, `overloaded`) are not `FailureAuth`: the CLI retries them as today.
A call that fails with an error result `API Error: NNN …` (the API, or a gateway in front of
it, refused the request) carries that status as `ProviderError.APIStatus`; the LLM decision
provider classifies it like a Jev HTTP status (`apperr.DecisionKindForStatus`), so a 400 for
a model the endpoint does not serve is `bad_request`, not `unavailable` (2026-10-15).
A 401 is not final for the CLI: claude 2.1.300 retries it as `system/api_retry` with
`error_status: 401, error: "authentication_failed"`, up to `max_retries: 10`, and the init event
still reports `apiKeySource: ANTHROPIC_API_KEY` (CA07, `specs/permissions/DECISIONS.md` D17).
The adapter watches the live stream (`authRetryWatcher`) and stops the call at the first
auth-category `api_retry`, so a wrong key costs one retry delay and not the call's timeout
(§16 D4, 2026-10-15).

## 10. Persistence and output

- `run.json`: `claude_auth: {requested, mode, decided_by, key_env}` at run start; each
  `resumes[]` entry gets its own. Never the key, its length or a hash of it.
- Each execution's `provider.api_key_source`: what §8 read (claude only). Decision calls get
  the same field in `decision.json`'s `usage` blocks.
- `summary.md` (MVP.md §43): one line, `Claude auth: api key (ANTHROPIC_API_KEY, auto)`, and on
  a resumed run the mode of each part if they differ.
- `outcome.usage.cost_usd` stays the CLI's estimate in both modes; `summary.md` marks it
  `(estimate; billed to the subscription)` in `login`, since no money changes hands per token.
- `validate` prints the resolved mode line from §4.

## 11. Resume

Credentials are the environment's, not the run's. `resume` resolves the mode again from its
own flag, the current config and environment, records it in `resumes[]`, and continues; it
never reads `claude_auth` back to decide anything. The supported story: a run stops on the
subscription's usage window or a `FailureAuth`, and `orchestrator resume --claude-auth api <id>`
finishes it on the key. A resume that changes the mode prints
`claude auth changed since the run started: login → api`.

## 12. The key itself

- Read once, in `app`, from `api_key_env`; trimmed of surrounding whitespace.
- Invalid, a config error before any call: whitespace or a control character inside it; the
  prefix `sk-ant-oat` (an OAuth token from `claude setup-token` put in the key variable:
  "this is a subscription token; set it as CLAUDE_CODE_OAUTH_TOKEN and use --claude-auth
  login"). No other format check: a gateway may issue keys of any shape.
- No HTTP request validates it (no new HTTP integration, MVP.md §30); the first call does,
  and §9 makes its failure clear and fatal.
- Passed only through the adapter's `Env`; never in argv. The value is redacted from every
  error, log and `ProviderError` the way the Jev key is, before anything prints or persists
  it. Tests assemble any key-shaped value at run time (CLAUDE.md: no token-shaped literal).
- Which processes see it: the `claude` process, and what it spawns: the operator's own MCP
  servers (`extensions`, opt-in, as today) and the `orch` verify server. The verify server
  never passes it on: it runs `test.command` only with the `TestEnv` that `action` computed and
  wrote into `verify.json` (`internal/verify/spec.go`). Not the test command, not git, not
  codex/gemini.

## 13. Corner cases

| # | case | behavior |
|---|---|---|
| 1 | `ANTHROPIC_API_KEY` set in the shell, nothing configured | `auto` → `api`: today's behavior, now visible on the console and in `run.json` |
| 2 | Same, plus `CLAUDE_CODE_USE_BEDROCK=1` | today Bedrock wins; now `auto` → `api` and Bedrock is removed, warned. The **one behavior change**; `--claude-auth login` keeps Bedrock |
| 3 | `api_key_env: ORCH_CLAUDE_KEY` unset, `ANTHROPIC_API_KEY` set | `auto` → `login` (only the configured variable counts), `ANTHROPIC_API_KEY` removed, notice names both |
| 4 | Key variable set to `""` or spaces | `auto` → `login` + warning; `api` → config error |
| 5 | A `setup-token` OAuth token in the key variable | config error with the fix (§12) |
| 6 | `api`, managed `forceLoginMethod` on the machine | the CLI refuses the key at startup → the call fails; with stream output it is `FailureAuth`, otherwise a provider exit error naming the mode |
| 7 | `api`, a signed-in Claude apps gateway session | the CLI reports `apiKeySource: none` → `FailureAuth` after the first call (§8) |
| 8 | Invalid or revoked key, out of credit | `FailureAuth` from `api_retry`/result (§9); translator and decision do not swallow it |
| 9 | Subscription window exhausted mid-run (`login`) | the step fails as today; `resume --claude-auth api` continues it (§11) |
| 10 | `extensions.user_settings: true` with an `apiKeyHelper` in user settings | `api`: the key outranks the helper, check passes. `login`: the helper is used, `apiKeySource: apiKeyHelper`, recorded, allowed |
| 11 | `limits.max_cost_usd` in `login` | enforced on the CLI's estimate as today; the summary says it was not billed per token |
| 12 | `ANTHROPIC_BASE_URL` set in `api` | inherited; one warning naming its host only: the key goes there |
| 13 | `--claude-auth` on `decisions report` | usage error |
| 14 | `--claude-auth api` with `--stage` or `--jira` | works; Jira's own credentials are unrelated |
| 15 | `api_key_env` also listed in `test.env_pass` | config error (existing credential rule) |
| 16 | `api_key_env` names `CLAUDECODE` or another parent-session variable | config error |
| 17 | The orchestrator runs inside a Claude Code session (parent-session vars set) | unchanged: they are scrubbed first; the key comes only from `api_key_env` |
| 18 | A test double prints plain text (no stream-json) | no `apiKeySource`, not checked, recorded `""` (§8) |
| 19 | A future CLI makes `--bare` the default for `-p` | `login` would break; the version range (`<2.2.0`) refuses that CLI until the live check (CA07) passes on it |
| 20 | `bench/` (`orchbench`) runs `claude` for its baseline itself | not covered; the operator gives both the same environment (bench README note) |
| 21 | The key's endpoint serves other model names than the login (a Bedrock gateway) | `api_env` maps the aliases (`ANTHROPIC_DEFAULT_OPUS_MODEL` …) in `api` only; routes keep `opus`/`sonnet` (§17) |
| 22 | The gateway does not serve a model (`API Error: 400 … no healthy deployments`) | the step fails with the status in the message; a decision call is `bad_request`, not `unavailable` (§9) |

## 14. Out of scope and the next step

- **The Messages API from Go** (TODO.md P12, ⚠ spec): an HTTP client to `api.anthropic.com`
  with the orchestrator's own agent loop. It needs a change to MVP.md §30, Go implementations
  of `Read`/`Grep`/`Glob`/`Edit`/`Write` with each authority enforced in Go (and a live check
  replaced by Go tests), the deny list and confinement to the working directory in Go, cost
  from `usage` × a price table, retries on 429/529, prompt caching, and `verify` as a Go tool.
  It would make authority enforcement stronger than CLI flags, at the price of maintaining an
  agent. This spec leaves the provider interface ready for it: the mode is the adapter's
  business, not the workflow's.
- `ANTHROPIC_AUTH_TOKEN`/gateway, Bedrock, Vertex, Foundry as first-class modes: they keep
  working under `login`; making them explicit is TODO.md P6.
- An HTTP preflight of the key: no new HTTP integration.
- Choosing the mode per action or per complexity: one mode per process (§15 Q3).

## 15. The owner's answers (2026-10-14)

| # | question | answer |
|---|---|---|
| Q1 | What does "through the API" mean? | The same `claude` CLI on a Console API key now; a TODO for a Messages API client in Go |
| Q2 | Where does the key come from? | An environment variable named in the config (`providers.claude.api_key_env`), the literal rejected |
| Q3 | How is the mode switched? | `auto` by default (key set → api), `providers.claude.auth` in config, `--claude-auth` flag over it; `login` removes the key so nothing is billed silently |
| Q4 | Which calls does it cover? | All claude calls: steps, translator, LLM decision provider |
| Q5 | May a live check spend real API calls (CA07, about $0.05)? Which key? | Yes (2026-10-15). The owner's key is a gateway (LiteLLM → Bedrock) key, set in the environment by the owner |

## 16. Defaults (the author's, stand unless changed)

- **D1** Mode names `auto | api | login`. `login` rather than `subscription`: it also covers a
  Console login, a cloud provider and a gateway the CLI is set up for.
- **D2** `api` removes the variables that outrank or replace the key (§7); `login` removes only
  the key. A run never mixes them.
- **D3** No `--bare` in `api`: it would also drop CLAUDE.md and `extensions.user_settings`, so
  the same task would get a different context depending on who pays. §7 + §8 already guarantee
  the key is used.
- **D4** The `apiKeySource` check runs after the call, not live on the init line: one call
  can run on the wrong credential before the run stops. An auth-category `api_retry` is acted
  on live. The adapter's stdout sink cancels the call at the first one, because the CLI
  retries a refused key for minutes (CA07, `specs/permissions/DECISIONS.md` D17; changed
  2026-10-15).
- **D5** The resolved mode reaches the adapter through a setter on `claude.Client` called by
  `app` after the fail-fast chain (the registry is built before the config is loaded, as for
  `execution.ScrubCredentials`), not through `provider.Request`: the workflow never learns it.
- **D6** `resume` re-resolves; the persisted mode is a record, never an input.
- **D7** The org layer gets no `providers.claude.auth` floor: whether an org may forbid
  subscription use is a policy question for TODO.md P6 (legal).

## 17. The api mode's own environment (`providers.claude.api_env`)

Added 2026-10-15, task CA09. Built the same day (`scripts/check.sh` pending). The secret scan
of §17.3 runs in `Validate`: `internal/secretscan` is a standard-library leaf `config` imports.

### 17.1 Problem

A key and a login can reach different endpoints that serve different model names. The
owner's key goes to an internal gateway (LiteLLM in front of Bedrock): it needs
`ANTHROPIC_BASE_URL`, `AWS_PROFILE`, `AWS_REGION` and the aliases
`ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU,FABLE}_MODEL` mapped to `bedrock/eu.anthropic.…` names.
Without the aliases the CLI sends `claude-opus-5-5` and the gateway answers
`API Error: 400 … no healthy deployments for this model` (run 7250e1ac). Exporting the
variables in the shell does not work: `login` inherits them (§7), so the subscription call
goes to the gateway with a gateway model name. The operator would have to `unset` them
before every switch of mode, and a resume that changes mode (§11) would need another shell.

### 17.2 Configuration

```yaml
providers:
  claude:
    auth: auto
    api_key_env: ANTHROPIC_API_KEY
    api_env:
      ANTHROPIC_BASE_URL: https://ai-gateway.example.internal
      AWS_PROFILE: claude-bedrock
      AWS_REGION: eu-west-1
      ANTHROPIC_DEFAULT_OPUS_MODEL: bedrock/eu.anthropic.claude-opus-5-5
      ANTHROPIC_DEFAULT_SONNET_MODEL: bedrock/eu.anthropic.claude-sonnet-5-5
      ANTHROPIC_DEFAULT_HAIKU_MODEL: bedrock/eu.anthropic.claude-haiku-4-5-20251001-v1:0
      ANTHROPIC_DEFAULT_FABLE_MODEL: bedrock/eu.anthropic.claude-fable-5
```

A map of names to values. Absent or empty means today's behavior byte for byte. Routes keep the
aliases (`model: opus`), so `by_complexity`, `escalate` and `run --stage` need no change and
the same config works in both modes. The CLI resolves the alias.

### 17.3 Validation (config errors, in `Validate`, before any call)

- A name must match `envNameRE` and must not be:
  - `ANTHROPIC_API_KEY` or the effective `api_key_env`, because the key comes only from the
    environment (§12);
  - one of the variables `api` removes (§7: `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_USE_*`,
    `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_PROFILE`, `ANTHROPIC_FEDERATION_RULE_ID`), since
    setting one would undo the mode;
  - a parent-session variable (`execution.IsParentSessionVar`);
  - a name in `Config.CredentialEnvNames()`;
  - a credential-like name (`credentialLike`, the `test.env_pass` rule). This rules out
    `AWS_SECRET_ACCESS_KEY` or `AWS_SESSION_TOKEN` by name; a cloud credential belongs in
    the profile `AWS_PROFILE` names, not in a committed config.
- A value must be non-empty and free of control characters (a newline included). A value
  that parses as a URL with userinfo (`https://user:pass@host`) is refused. The value is
  also run through `internal/secretscan`, and a finding is refused with the variable's name,
  never the value. The config file is committed, so it holds no secret.
- With an org config that has `allowed_models` (MVP.md §45), any value of
  `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `ANTHROPIC_DEFAULT_*_MODEL` or
  `ANTHROPIC_CUSTOM_MODEL_OPTION` in `api_env` must also be in `allowed_models`. These
  variables change the model a route reaches. A value outside the list is the same error as
  a route outside it (D8).
- The org layer gets no `api_env` of its own (D7 still applies).

### 17.4 The child's environment

Built in `buildEnv` (`internal/provider/claude/auth.go`), after the §7 table:

- `api`: each `api_env` entry is set, replacing an inherited value of the same name.
- `login`: each name in `api_env` is removed, so an export left in the shell cannot pull the
  subscription onto the gateway. The removed names that were set join the one notice
  (`ignored in login mode: ANTHROPIC_BASE_URL`), names only.
- The version probe gets the same environment (one code path, §7).
- No other process gets `api_env`: not the test command (`TestEnv`), not git, not the `orch`
  verify server's test processes (they run with `verify.json`'s `TestEnv`), not codex or
  gemini. The values are not secret, but they are claude's.
- When `api_env` sets `ANTHROPIC_BASE_URL`, the §13 #12 warning names its host and says
  `(providers.claude.api_env)`.

### 17.5 Persistence and output

- `run.json` gets `claude_auth.api_env`, the sorted names that were set (api) or removed
  (login), never the values. Each `resumes[]` entry gets its own (§11: a resume re-resolves).
- `validate` and the start of a run print one line in `api`:
  `claude env (api): ANTHROPIC_BASE_URL (ai-gateway.example.internal), AWS_PROFILE, AWS_REGION, ANTHROPIC_DEFAULT_OPUS_MODEL, …`.
  Only the host of `ANTHROPIC_BASE_URL` is shown; other values are not printed.
- The real model of each call is already in `call.models` (the CLI reports
  `bedrock/eu.anthropic.claude-opus-5-5`), so the audit shows what the alias became.
- `summary.md`'s `Claude auth` line adds `, env: N variables` in `api`.
- `decision.json`'s `models` keeps the routed alias. It records the route, not the endpoint.

### 17.6 Out of scope

- A `login_env` for the other direction. Nobody has asked for it; add it the same way if a
  login setup ever needs variables the key's setup must not see.
- Per-action or per-complexity environments: one mode and one environment per process (§15 Q3).
- `bench/`: the operator gives `orchbench` the same environment (§13 #20).

### 17.7 Defaults

- **D8** The model variables in `api_env` are checked against the org's `allowed_models`. A
  stricter alternative (forbid them whenever the org config exists) would block exactly the
  gateway setup this section is for.
- **D9** `login` removes the `api_env` names rather than leaving them alone. The section
  exists because a shell export leaks across modes, and keeping them would bring that leak
  back.


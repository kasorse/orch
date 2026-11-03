# The API transport: claude and codex over HTTP, without their CLIs

Status: **draft** (2026-11-01). The owner's answers are §0; everything else follows from them
and from the invariants in CLAUDE.md. Normative where it says "must".

## 0. Decisions (the owner, 2026-11-01)

| # | question | answer |
|---|---|---|
| O1 | which calls go over HTTP | all of them, in three phases: no tools (§10 phase 1), read and web (phase 2), edit, MCP and skills (phase 3) |
| O2 | how it is switched on | a new key `providers.<p>.transport: cli \| api`; the CLI with an API key stays as it is |
| O3 | HTTP client | the standard library (`net/http`), unexported wire types, as `decision/jev` and `jira`; no SDK |
| O4 | OpenAI API | the Responses API (`POST /v1/responses`) |
| O5 | model names | full IDs only under `transport: api` (an alias like `sonnet` is a config error) |
| O6 | cost | the tokens the answer reports × the provider's published price; a gateway's own figure when it gives one (§7) |
| O7 | MCP | the adapter is the MCP client: it starts the servers of `Extensions.MCPConfig` itself |
| O8 | OpenAI state | `store: false`, the history resent each turn with `reasoning.encrypted_content` |
| O9 | `user_settings` | gives the model every skill in `~/.claude` (and the enabled plugins') through a `skill` tool; the orchestrator returns the skill's text, the model decides; hooks do not exist over HTTP |
| O10 | Bedrock | through the existing LiteLLM gateway (`base_url`); no SigV4 or AWS credentials in the orchestrator |
| O11 | research | an ordinary model call; web tools only where the endpoint has them (§6) |
| O12 | codex endpoint | `api.openai.com` now, the LiteLLM gateway later: nothing may assume the first-party endpoint |

## 1. The problem

Every model call starts a CLI process (`claude --print`, `codex exec`): a cold start per call,
three of them before the first step (translate, context depth, classify), a stream format the
adapter has to reverse-engineer, a CLI version range to re-verify, and no control over the
request (caching, effort, retries). With an API key the orchestrator can call the Messages API
and the Responses API directly. For a call without tools that is one HTTP request. For a step
with tools the orchestrator must run the tool loop itself, and then the boundary that
`--restricted`, `--tools` and `dontAsk` enforce today becomes Go code that this spec defines
(§5) and tests (§11).

The operator's setup is already an Anthropic-compatible gateway: `providers.claude.api_env`
sets `ANTHROPIC_BASE_URL` to an internal LiteLLM, whose model names (`bedrock/eu.anthropic.
claude-opus-5-5`) reach Bedrock; LiteLLM signs the Bedrock requests. The API transport talks to
the same endpoint with the same key.

## 2. Configuration

```yaml
providers:
  claude:
    auth: api                    # transport: api needs api (auto resolving to api is enough)
    api_key_env: ANTHROPIC_API_KEY
    transport: api               # cli (default) | api
    base_url: https://ai-gateway.example.internal   # default https://api.anthropic.com
    web_tools: false             # default: true without base_url, false with it (§6)
    prices:                      # USD per million tokens; overrides the built-in table (§7)
      bedrock/eu.anthropic.claude-opus-5-5: {input: 4.4, output: 22, cache_read: 0.44, cache_write: 5.5}
  codex:
    auth: api
    api_key_env: OPENAI_API_KEY
    transport: api
    # base_url: https://api.openai.com   # default
```

`config.Validate` (strict schema, fail fast, before any call):

1. `transport` is `cli`, `api` or absent (`cli`). Gemini has no `transport`.
2. `transport: api` needs the resolved auth mode `api` (`--claude-auth`/`--codex-auth` included):
   `login` with `api` is a config error naming both keys. The key variable must be set
   (`ValidateDependencies`), and no executable is required for that provider.
3. `base_url` is `https://` (plain `http://` only for `localhost`/`127.0.0.1`, for tests), has no
   user, password, query or fragment, and is never a secret (the `api_env` rules of
   specs/claude-auth §17.3 apply).
4. Every route of an api provider (actions, `by_complexity`, `escalate`, the translator,
   clarify, an LLM decision provider, branch naming, story survey/specify) names a full model ID:
   one of the CLI aliases (`sonnet`, `opus`, `haiku`, `fable`, `opusplan`, `default`, and an
   alias with `[1m]`) is refused, naming the route.
5. `providers.claude.api_env` with `transport: api` is a config error naming `base_url`: the
   variables only configure the CLI.
6. `prices` keys are model IDs, each value has `input` and `output` (`cache_read`,
   `cache_write` optional, default 0.1× and 1.25× input), all finite and ≥ 0.
7. `limits.max_cost_usd` with an api route whose model has no price (built-in or `prices`) is a
   config error, unless that provider's `base_url` is a gateway known to report cost — no
   gateway is assumed to: the operator adds `prices`. This is the existing budget check of the
   fail-fast chain (`config.CostChecker`) made model-aware (AT05), not a `Validate` rule: the
   built-in table lives in `httpapi`.
8. `extensions.user_settings` is allowed (O9). A hook in the operator's settings is not an
   error: the run prints once that hooks do not run over the API transport.

The org layer (`/etc/orchestrator/org.yaml`) has no providers section and gets none here;
`allowed_models` applies to the full IDs like any route's model. With `transport: cli` each of
`base_url`, `web_tools` and `prices` is an error: the CLI would silently ignore it.

## 3. Packages

```
internal/provider/httpapi     shared: SSE reader, retries, redaction, no redirects, cost, the model check
internal/provider/anthropic   the Messages API wire (unexported types), the claude Client for transport api
internal/provider/openai      the Responses API wire (unexported types), the codex Client for transport api
internal/provider/tools       the Go tools and their confinement (§5), skills
internal/provider/mcp         the MCP stdio client (§5.3): it runs commands, so it is not in tools
```

Each new package imports only `domain`, `apperr`, `provider` (the interfaces), `projectctx`
(the deny-list matcher, as `verify` does) and the standard library — never `config`, `action` or
`app`. The provider IDs stay `claude` and `codex`: `app` registers, per provider, the CLI client
or the API client from the resolved transport, so routing, `decision.json: models` and every
caller are unchanged. Per-provider wire quirks stay in their package, as `buildArgs` does today.

## 4. Requests

Both adapters stream (`stream: true`) every request: a step can run for many minutes, and
streaming gives `Request.Stream`/`Activity` their live output and a partial usage on a timeout.

### 4.1 Anthropic (`POST {base_url}/v1/messages`)

- Headers: `x-api-key`, `anthropic-version: 2023-06-01`, `content-type`. **No `anthropic-beta`
  header, ever**: a gateway to Bedrock may refuse one, and every beta in use today (compaction,
  context editing, fallbacks, fast mode, task budgets) is out of scope (§12).
- `system`: one fixed text per authority (§6), frozen in Go, never interpolated (the cache prefix).
  `messages[0]`: the prompt as built today (`Request.Prompt`), as a user text block.
- `max_tokens: 64000`. `thinking` omitted (the model's default; adaptive on current models).
  `Request.Effort` → `output_config.effort`; empty omits it. No sampling parameters.
- `tools`: sorted by name, the same list on every turn of a call (changing it invalidates the cache).
- Prompt caching, explicit breakpoints (`cache_control: {type: ephemeral}`), at most three: the last
  block of `messages[0]`, the last block of the previous turn, the last block of the current turn.
  Explicit because a Bedrock integration may refuse the top-level form; three because the cache
  looks back only 20 blocks and one turn of parallel tool results can exceed that.
- Stop reasons: `end_turn` ends the call (the answer is the text blocks of the last message,
  joined); `tool_use` runs the tools (§5) and sends **all** results in one user message, every
  `tool_use_id` answered, failures as `is_error: true`; `pause_turn` resends the conversation as it
  is (no added message, at most 5 in a row); `max_tokens` fails the call ("output cut at
  max_tokens"); `refusal` fails the call with `stop_details.category`. No server-side fallback:
  another model answering is the automatic fallback §31.1 forbids.
- The assistant content is appended verbatim, thinking blocks included (they must be passed back
  unchanged).

### 4.2 OpenAI (`POST {base_url}/v1/responses`)

- `Authorization: Bearer <key>`. `store: false`, `include: ["reasoning.encrypted_content"]`.
- `instructions`: the fixed text per authority; `input`: the prompt as a user message, then every
  output item of each response appended verbatim (reasoning items carry the encrypted content
  back), then the `function_call_output`/`apply_patch_call_output` items.
- `Request.Effort` → `reasoning.effort`; `parallel_tool_calls: true`; function tools with
  `strict: true`, sorted by name; `prompt_cache_key`: sha256 of provider, model and authority, so
  calls with one prefix share a cache and nothing per run or per user is in it.
- `status: incomplete` fails the call with `incomplete_details.reason`; `failed` fails it with the
  error's code (never its free text unredacted).
- Only what LiteLLM proxies on `/v1/responses` (O12): no `previous_response_id`, no
  `conversation`, no `background`, no Conversations or Files API.

## 5. The tool loop and its boundary

A call with tools loops: request → tools → request, until the model stops. `execution.timeout`
(the step's context) bounds the whole loop; a fixed cap of 500 model turns per call is a backstop
and fails the call. Tool calls of one turn run one after another, in order.

The boundary is Go code in `internal/provider/tools`, and it must hold what the CLI flags hold
today (MVP.md §38.2):

1. **No shell, no code execution, no network** from any repository tool. There is no tool that runs
   a command; MCP servers run only the commands the config names (§5.3).
2. **Paths** are relative to `Request.WorkingDir`. An absolute path, a `..` that leaves it, a path
   whose resolved real path (`filepath.EvalSymlinks`) leaves it, or a symlink as a write target is
   refused (`is_error`). In a worktree run this keeps the step out of the operator's tree and the
   run directory, as `--restricted` does (V-wt-1…3).
3. **The deny list** (`Request.DenyRead`) is matched on every path segment with projectctx's
   matcher, case-insensitively, before any read, listing or write: a denied file is neither read,
   listed by grep/glob, nor written.
4. **`.git` and `.claude`**: never written, never read below `.git/` (the guard's fingerprint
   covers them; the CLI always prompts for them, so `dontAsk` denied them).
5. **Bounds**: a file over 1 MiB, a binary file (NUL in the first 8 KiB) or a non-regular file is
   not read; a tool result is cut at 64 KiB with a marker naming what was cut; grep and glob walk
   the directory in Go (no `git`: `git status` and friends run a planted `core.fsmonitor`), skip
   `.git`, never follow a symlink, and stop at 2000 matches.
6. The guard, the secret scan and the protected paths are unchanged: they still run around every
   `implement` and `test` (§38.3). The tools add no exception to them.

### 5.1 Tools by authority

| authority | Anthropic | OpenAI |
|---|---|---|
| `none` | no `tools` | no `tools` |
| `web` | `web_search_20260209`, `web_fetch_20260209` (`max_uses: 10` each) when `web_tools` | `web_search` when `web_tools` |
| `read` | `read_file`, `grep`, `glob` (function tools) | the same three |
| `edit` | `str_replace_based_edit_tool` (`text_editor_20250728`), `grep`, `glob` | `apply_patch`, `read_file`, `grep`, `glob` |

Plus, for the actions the config gives them to (unchanged resolution in `action`): the allowlisted
MCP tools (`Extensions.MCPTools`, §5.3) and, with `user_settings`, the `skill` tool (§5.4). `web`
never gets a repository tool, and no call gets both repository access and the network: a step
with `read`/`edit` never gets `web_search`/`web_fetch`.

The text editor's `view`, `create`, `str_replace` and `insert` are honoured, `undo_edit` refused.
`apply_patch` operations (`create_file`, `update_file`, `delete_file`, V4A diffs) are parsed and
applied in Go; a hunk that does not match the file fails that operation with `status: failed` and a
message the model can act on, and changes nothing. `apply_patch` is OpenAI's built-in tool on the
first-party endpoint only; behind any other `base_url` it is a strict function tool of the same name
and operation (`type`, `path`, `diff`), answered with a `function_call_output`: the LiteLLM gateway
to Bedrock drops the built-in tool, so the model had no way to edit (AT13).

### 5.2 Live output and the call report

`Activity` gets one line per tool call (`Read src/app.ts`, `Edit internal/x.go`, `mcp__orch__run_tests`),
`Stream` the text deltas and those lines. `CallReport.Tools` counts calls by tool name,
`CallReport.Models` the `model` field of the responses, `APIMS` the time spent waiting on HTTP,
`Usage.Turns` the model turns.

### 5.3 MCP (O7)

The adapter starts each server of `Extensions.MCPConfig` (`{"mcpServers": {name: {command, args,
env}}}`) as a child process: argv only, `cwd` the working directory, the environment
`execution.ScrubbedEnv()` plus the server's own `env`. It speaks MCP over stdio (JSON-RPC 2.0:
`initialize`, `notifications/initialized`, `tools/list`, `tools/call`) with the standard library,
offers the model only the tools `Extensions.MCPTools` allowlists, named `mcp__<server>__<tool>`
(a whole-server entry `mcp__<server>` allows all of its tools), and kills every server when the call
ends. A server that fails to start or to list is recorded in `CallReport.MCPFailed` and the call runs
without it, as today. The orchestrator's own `orch` verify server works unchanged: `action` writes
`verify.json` and the config entry; the server enforces its own rules; `action` reads `calls.jsonl`
and `violation` back. Both adapters are `provider.MCPSupporter`.

### 5.4 Skills (O9)

With `user_settings` for the action, the adapter discovers skills in `~/.claude/skills/*/SKILL.md`
and in the skills of plugins enabled in `~/.claude/settings.json` (from
`~/.claude/plugins/installed_plugins.json`), reads each one's front matter (`name`, `description`)
and offers one `skill` tool: `name` (an enum of the discovered names) and an optional `path` (a file
inside that skill's directory). The result is the file's text, under the bounds of §5 item 5 and the
deny list; the path rules of §5 item 2 apply with the skill's directory as the root. A skill is
instructions: if it tells the model to run a command, there is still no tool that runs one. Skill
text is the operator's, but it reaches the model as a tool result, not as a system prompt. Hooks are
the CLI's own events and do not run (§2 item 8). The tool is offered to `read` and `edit` calls
only: a `web` call has a way out, a `none` call no tools.

## 6. Web tools (O11)

`web_tools` defaults to `true` when `base_url` is absent (the first-party API has them) and to
`false` when it is set (Bedrock behind a gateway has no `web_search`/`web_fetch`). Without them a
`web` call is a plain model call: the model answers from what it knows. This is fewer tools than the
authority allows, never more, so it is not a downgrade of a boundary; the call report records which
web tools were offered (`call.web_tools`). The orchestrator never fetches a URL for the model.

## 7. Usage and cost (O6)

| `domain.Usage` | Anthropic `usage` | OpenAI `usage` |
|---|---|---|
| `InputTokens` | `input_tokens` | `input_tokens − input_tokens_details.cached_tokens − …cache_write_tokens` |
| `CacheReadTokens` | `cache_read_input_tokens` | `input_tokens_details.cached_tokens` |
| `CacheCreationTokens` | `cache_creation_input_tokens` | `input_tokens_details.cache_write_tokens` |
| `OutputTokens` | `output_tokens` | `output_tokens` |
| `ReasoningTokens` | — | `output_tokens_details.reasoning_tokens` |

Summed over the turns of a call. `CostUSD` is, per response, the gateway's own figure when the
response carries a valid `x-litellm-response-cost` header (a finite number ≥ 0), else the tokens ×
the price: `prices` from the config first, then the built-in table in `httpapi` (the first-party
models of the providers' pricing pages, with the date they were taken). A model with neither leaves
the cost 0 and `ReportsCost` false for that route, which is why §2 item 7 refuses a budget for it.
LiteLLM sends no `x-litellm-response-cost` on a streamed response, as a header or a trailer (AT13,
checked on both wires): every request streams, so a gateway route is priced from `prices` alone.
`provider.CostReporter` becomes model-aware (`ReportsCost(model string) bool`) and
`config.CostChecker` follows. A call cut off (timeout, cancel) reports the completed responses with
`Partial: true`.

## 8. Errors and retries

- No redirect is followed (`CheckRedirect` returns an error), as for Jira.
- 401, 403 and a billing/permission error type → `FailureAuth` (fatal in the translator, the LLM
  decision provider and every step, MVP.md §46).
- 429: wait `retry-after` (seconds) and retry, up to 3 times, while the step's context has room. A
  429 that remains, or one whose `retry-after` (or `anthropic-ratelimit-*-reset`,
  `x-ratelimit-reset-*`) is past the context's deadline, is `FailureRateLimit` with that reset time,
  so the shared tail waits as §25.2 describes. OpenAI's `insufficient_quota` is `FailureAuth`, not a
  rate limit: waiting does not refill an account.
- 500, 502, 503, 504, 529 and `overloaded_error`, and a connection error before the first byte:
  exponential backoff with jitter (1 s, 2 s, 4 s), 3 retries, then `ProviderError`. A stream broken
  after the first event is not retried (tools may have run): the call fails, with the partial usage.
- Every error message and log line has the key replaced by `[REDACTED]` (`httpapi` redacts, as
  `jev` does); the key is never in a URL, an argv, `run.json` or a log.
- An event that is not valid JSON, or a known event with an unexpected shape, is `InvalidResponse`;
  unknown event types are skipped (the APIs add them).

## 9. Startup, persistence, resume

- The fail-fast chain: `ValidateDependencies` checks the key variable instead of an executable;
  `ValidateAuthorities` passes (the API clients support every authority, enforced in Go); instead of
  the CLI version check, `httpapi.CheckModels` makes one `GET {base_url}/v1/models` per api provider
  (following the pages) and requires every routed model ID in `data[].id` — the first-party APIs and
  LiteLLM all answer in that shape. A failure exits before any run, like `jev.CheckModel`.
- `run.json: provider_cli` gets, for an api provider, `{provider, transport: "api", endpoint}`
  (`endpoint` is the host of `base_url`, never a path or query) and no version; each `resumes[]`
  entry has its own. `claude_auth` is recorded as today with `mode: api`; `api_key_source` is empty
  (the adapter sends the key itself, there is nothing to check).
- `resume` re-resolves the transport from the current config, as it re-resolves the auth mode, and
  records it; the decision and workflow are not re-made.

## 10. Phases

1. **No tools** (AT01–AT05): the translator, clarify, the LLM decision provider, branch naming and
   every `none` call run over HTTP. Read, web and edit routes of an api provider are refused by
   `ValidateAuthorities` until phase 2/3 (`Supports` is false for them), so phase 1 is usable on
   its own.
2. **Read and web** (AT06–AT09): the tool loop, the confined read tools, the web tools.
3. **Edit, MCP, skills** (AT10–AT12), then the live checks (AT13) and the closing documentation
   (AT14).

## 11. Testing

Unit tests against `httptest` servers that replay recorded event streams for both wires (no network,
no key). The confinement of §5 gets its own table-driven tests: absolute paths, `..`, symlinks in and
out, a symlinked parent directory, the deny list at every depth, `.git`, oversize and binary files,
the result cut. Integration scenarios (`test/integration`) run whole workflows against a fake API
server routed by the `ACTION:` line of the prompt, as `fakeexec` does for the CLIs. The live suite
(`go test -tags security ./test/security/...`) gets an api section: the wire against the first-party
endpoints and against the operator's gateway (caching hit on the second turn, the cost header,
`apply_patch`, MCP, a path escape refused); it spends real calls and is the operator's gate.

## 12. Out of scope

Bedrock or Vertex without a gateway (SigV4, GCP credentials); Chat Completions; any `anthropic-beta`
feature (compaction, context editing, server-side fallbacks, fast mode, task budgets, mid-conversation
system messages); the Batches, Files, Conversations and Agent Skills APIs; Managed Agents; a shell or
code-execution tool; client-side web fetching; hooks; LiteLLM as a component of the orchestrator
(it is only an endpoint behind `base_url`); automatic choice between the transports.

## 13. Documentation it changes

MVP.md: §30 (HTTP provider APIs: claude and codex under `transport: api`), §31.1 (no server-side
fallback), §38.2 (enforcement in Go for the API transport), §46 (auth `api` with `transport`), a new
§52 for this spec; CLAUDE.md (architecture: the new packages and their imports; the invariants:
least authority under the API transport); README; `orchestrator.example.yaml`; TODO.md (P13 becomes
this spec). Each task updates the parts its change makes out of date (CLAUDE.md, "Tasks from
specs/*/tasks").

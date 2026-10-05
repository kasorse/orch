# Spec: Hardening — reliable prompts and safe defaults (phase 0)

Status: **implemented** (2026-10-06). The design questions are answered (§13), the defaults
in §14 were kept, and MVP.md §41 is now the normative home; this file is the design record.
What was built differently is in each task's "As built" section.
Scope: TODO.md phase 0 (S1–S6). How a prompt reaches the provider CLI and how much
repository data goes into it; what the test command's environment is and whether it is
sandboxed by default; which provider CLI versions the orchestrator trusts; a test run before
the first step; a secret scan of what the run changed. Touches MVP.md §7 (fail-fast), §18–§19
(test, review), §25 (limits), §37 (resume), §38 (permissions).

---

## 1. Problem

Five defects, each verified against the code on master at `15d856a`:

1. **The prompt is one argv element and has no size bound.** `claude.args` puts the prompt
   last on the command line. Linux limits one argv string to `MAX_ARG_STRLEN` (128 KiB);
   a 140 000-byte argument fails with `fork/exec ...: argument list too long` (checked).
   The review prompt embeds the whole `git diff HEAD` (`Repository.Diff`), the implement
   retry prompt the whole test stdout and stderr (`BuildImplementPrompt`), both unbounded.
   A lock-file change or a long failing test log ends the run with an execution error and,
   below the limit, costs tokens for nothing.
2. **Review does not see new files.** `Repository.Diff` appends only the *names* of
   untracked files. A file `implement` created is reviewed by name, not by content, although
   MVP.md §19 says review inspects the actual change.
3. **The test command inherits almost the whole environment.** `ScrubbedEnv` removes the
   parent-session variables and the credential names the config declares, nothing else.
   `ANTHROPIC_API_KEY`, `AWS_*`, `GITHUB_TOKEN`, `SSH_AUTH_SOCK` reach code a model wrote.
   `test.sandbox` defaults to none, so that code also has the network and `$HOME`.
   (Decision D7 in `specs/permissions/DECISIONS.md` — no allowlist — was argued for provider
   CLIs, which have no shell; it does not hold for the test command, which *is* a shell.)
4. **The provider CLI version is never checked.** Every authority's flag set was verified
   against claude 2.1.283/2.1.287 (`specs/permissions/SPEC.md` §11). If a later CLI changes
   what `--restricted`, `--tools` or `dontAsk` mean, the orchestrator silently runs with a
   different authority — the one failure CLAUDE.md calls unacceptable.
5. **Tests are never run before the change.** If they already fail, `implement` is sent to
   fix failures it did not cause, spends `max_fix_iterations`, and the run fails with a
   misleading reason.

And one gap that becomes a leak with TODO W1/W2 (commit, push): nothing stops a secret that
`implement` or a test wrote into a repository file from reaching the review prompt today and a
commit tomorrow.

## 2. What already works

- `execution.Spec.Stdin` exists and `OSProcessExecutor` wires it (`internal/execution/process.go`).
- `claude --print` reads the prompt from stdin when no positional prompt is given (checked
  on 2.1.289 with the `AuthorityNone` flag set: answer received, `stop_reason: end_turn`).
- Every prompt builder starts with a literal `ACTION: <name>` line, so a prompt never
  starts with `/` and is never taken for a slash command.
- `testexec.Sandbox` (bwrap) hides `$HOME`, cuts the network and re-binds git directories
  read-only; `testexec.Probe` is already in the fail-fast chain.
- `action.Guard` snapshots and compares protected paths and the git fingerprint around
  `implement` and `test`; `projectctx.DenyPatterns` is the one secret deny list.
- `run.json` is validated strictly on resume (`persistence`), so new fields get the same
  treatment by construction.

## 3. Principles

1. **Fail closed, before any AI call.** A CLI version the orchestrator has not verified, a
   host that cannot sandbox when sandboxing is on, and an invalid new key all stop the run in
   the fail-fast chain (MVP.md §7).
2. **Bounding is deterministic.** What is cut from a prompt depends only on the input bytes
   and the configured limits — a resume rebuilds the same prompt from the same artifacts.
   Artifacts and logs always keep the full, uncut data.
3. **Secrets never leave the machine** — now also through the review diff and the test
   environment. A secret's value is never printed, logged or persisted; only file, line and
   rule.
4. **Only exit codes decide.** The pre-run test, like the test step, is pass/fail by exit
   code only. The secret scan is a guard, not a judgment by a model.
5. **No provider-specific behavior outside the adapter.** Prompt delivery and the verified
   version range live in `internal/provider/claude`.
6. **Nothing here widens an authority.** Every change narrows or bounds.

---

## 4. Prompt delivery on stdin (H01)

- The claude adapter passes the prompt as `execution.Spec.Stdin`, never in argv. `args`
  loses its `prompt` parameter; `--input-format text` is added explicitly. The rule that
  `--model` comes right before the prompt (variadic `--tools` would swallow it) no longer
  applies and its comment goes.
- `redactedArgs` is the argv as run (no prompt in it any more). `apperr.ProviderError`
  states the prompt length separately (`prompt: N bytes on stdin`).
- This covers every claude call: workflow steps, the translator, the LLM decision provider.
- codex and gemini refuse every request (`Supports` is false); their argv is unchanged.
  Their own specs (TODO P8) must adopt stdin delivery.
- The live permission suite (`go test -tags security ./test/security/...`) is re-run on the
  installed CLI after the change; the result goes into `specs/permissions/DECISIONS.md`.

## 5. Bounded prompt content (H02, H03)

### 5.1 Configuration

```yaml
limits:
  prompt:
    max_diff_bytes: 262144        # 256 KiB; 16 KiB..4 MiB
    max_test_output_bytes: 32768  # 32 KiB;  4 KiB..1 MiB
    diff_summarize: []            # extra globs shown as a stat line only
```

All three keys are optional; out-of-range values are a validation error (exit 2).

### 5.2 Test output (H02)

`BoundTestOutput(stdout, stderr string, budget int) (string, string, Omitted)` — a pure function
in `internal/action`, used by `BuildImplementPrompt` for `LastTestResult`:

- The budget is shared: each stream gets half; the unused share of the shorter stream goes
  to the other.
- A stream within its share is passed unchanged.
- Otherwise, on line boundaries: the first 20% of the share; then, from the omitted middle, the
  lines matching the failure pattern (§5.2.1) up to 30% of the share; then the last lines up
  to the remaining share. A single line longer than its part is cut at a UTF-8 boundary.
- Each cut is marked in place: `[... 1234 lines, 98765 bytes omitted ...]`.
- The prompt says that the output was shortened and where the full log is
  (`logs/NNN-test.stdout.log`).

#### 5.2.1 Failure pattern

A line matches when, after leading whitespace, it starts with one of `--- FAIL`, `FAIL`,
`panic:`, `Error`, `ERROR`, `error:`, `E   ` (pytest), `●` (jest), `✕`, `AssertionError`,
`Traceback`, or contains `_test.go:` followed by a line number. The pattern only selects what
to show, never pass/fail.

### 5.3 Review diff (H03)

`Repository.Diff` is replaced, for the review prompt, by `Repository.ReviewDiff(ctx, opts)`
returning a structured result; the old `Diff` stays for any other caller.

**Files.** The change set is:
- tracked changes against HEAD: `git diff HEAD --numstat -z` for the list, `git diff HEAD`
  split per file at `diff --git` headers for the content;
- untracked, not ignored files: `git ls-files --others --exclude-standard -z`;
- both with the runs directory excluded, as today.

**Per file**, in this order of precedence:

| file | shown as |
|---|---|
| matches the secret deny list (`projectctx.DenyPatterns`, every path segment, case-insensitive) | name + `(content withheld: secret deny list)` — tracked or untracked |
| symlink (untracked) | name + `(symlink -> target)` |
| binary (NUL byte in the first 8 KiB, or git reports `-` in numstat) | name + `(binary)` |
| matches a summarize glob (§5.3.1) | stat line `path  +A -D (summarized)` |
| anything else | full diff; untracked as `git diff --no-index -- /dev/null <path>` (exit 1 means "differs" and is success; any other non-zero is an error) |

**Budget.** The diff always starts with a header listing *every* file with its
`+A -D` and its treatment. Then full diffs follow in git order while they fit in
`max_diff_bytes`; a file that does not fit becomes a stat line marked
`(not shown: diff budget)`, and the next smaller files still get their chance. More than 500
untracked files: the first 500 by path, then `... and N more untracked files`.

**Artifact.** The review artifact gains `diff: {bytes, files, shown, summarized, withheld,
truncated}` so the reader of `run.json` knows what the reviewer actually saw. The prompt tells
the reviewer which files it did not see in full and that it may Read them (review has
`AuthorityRead`; deny-listed files are denied to it anyway).

#### 5.3.1 Summarize globs

Default list (always applied): `go.sum`, `package-lock.json`, `npm-shrinkwrap.json`,
`yarn.lock`, `pnpm-lock.yaml`, `bun.lockb`, `Cargo.lock`, `poetry.lock`, `uv.lock`,
`Pipfile.lock`, `composer.lock`, `Gemfile.lock`, `*.min.js`, `*.min.css`, `*.map`.
`limits.prompt.diff_summarize` adds to it (never removes). A glob matches the base name when
it has no `/`, otherwise the repository-relative path (`path.Match`). Manifests (`go.mod`,
`package.json`, `Cargo.toml`, `pyproject.toml`) are never summarized by default: a dependency
change stays visible to the reviewer.

---

## 6. Test environment and sandbox default (H04, H05)

### 6.1 Allowlist (H04)

`execution.TestEnv()` replaces `ScrubbedEnv()` for every test-command process (the test step,
the pre-run test §8, the bwrap probe). Provider CLIs keep `ScrubbedEnv()` (D7 unchanged for
them). A variable passes only when its name is exactly one of:

- **base:** `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `LANG`, `LANGUAGE`, `LC_ALL`,
  `LC_CTYPE`, `LC_COLLATE`, `LC_MESSAGES`, `LC_MONETARY`, `LC_NUMERIC`, `LC_TIME`, `TERM`,
  `TZ`, `TMPDIR`, `CI`;
- **toolchain:** `GOPATH`, `GOROOT`, `GOFLAGS`, `GOCACHE`, `GOMODCACHE`, `GOTOOLCHAIN`,
  `CGO_ENABLED`, `CARGO_HOME`, `RUSTUP_HOME`, `JAVA_HOME`, `VIRTUAL_ENV`, `PYTHONPATH`,
  `NODE_OPTIONS`, `XDG_CACHE_HOME`;
- **operator:** `test.env_pass` (§6.2).

Proxies (`GOPROXY`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `GONOSUMDB`, `GOPRIVATE`) are not in
the default: a proxy URL can carry credentials. An operator who needs one names it.

Credential names the config declares (`Config.CredentialEnvNames`) and the parent-session
variables are removed even if listed. Inside bwrap `TMPDIR` is still forced to `/tmp`.

### 6.2 `test.env_pass`

```yaml
test:
  env_pass: [GOPROXY, DATABASE_URL]
```

- Exact names, `^[A-Za-z_][A-Za-z0-9_]*$`; no wildcards. Duplicates are ignored.
- A name whose upper-case form contains `TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`,
  `CREDENTIAL`, `API_KEY`, `APIKEY`, `PRIVATE_KEY`, `ACCESS_KEY` or `AUTH` is a validation
  error naming the rule. No override: a test that needs a credential does not belong in a run
  that executes model-written code. (`DATABASE_URL` with an embedded password passes the name
  check — the operator's responsibility, documented.)
- A name that is a declared credential or a parent-session variable is a validation error.

### 6.3 Sandbox on by default (H05)

- `test.sandbox` absent or empty means `bwrap`. Valid values: `bwrap`, `none`.
- With `bwrap`, a host without `bwrap` on `PATH` or failing the probe fails fast with the
  existing remedy text plus: `or set test.sandbox: none to run tests unsandboxed`.
- With an explicit `none`, the authority line printed before approval says
  `test: <command> — NOT sandboxed: network, $HOME and your files are reachable`, and every
  `TestResult` keeps `sandboxed: false` (already persisted).
- `orchestrator.example.yaml` shows `sandbox: bwrap` uncommented, with the binds explained.
- macOS: there is no bwrap; the remedy names `test.sandbox: none` until TODO P4.

---

## 7. Provider CLI version (H06, H13)

### 7.1 Verified range

Each adapter that `Supports` any authority declares the CLI versions its flag sets were
verified on. For claude: `>= 2.1.283, < 2.2.0` — a minimum and the next minor as the upper
bound. Raising the minimum or crossing a minor requires a green live permission suite on that
version and an entry in `specs/permissions/DECISIONS.md`. The range lives in
`internal/provider/claude` (provider-specific); `provider` gains an optional interface:

```go
type VersionVerifier interface {
    // CLIVersion runs the CLI's version command and returns the version string and whether
    // it is inside the verified range. A version that cannot be parsed is not verified.
    CLIVersion(ctx context.Context) (version string, verified bool, err error)
}
```

### 7.2 Check

- Runs in the fail-fast chain right after `ValidateAuthorities`, once per distinct provider
  routed by the config (actions, translator, LLM decision provider) whose adapter implements
  `VersionVerifier`. Timeout 10 s per CLI.
- claude: `claude --version`; the first line, trimmed, must match
  `^(\d+)\.(\d+)\.(\d+)(\s|$)` — anything else (pre-release suffix, empty output, non-zero
  exit) is "cannot be parsed".
- Not verified → `DependencyError` (exit 2):
  `claude 2.2.1 has not been verified ... verified: >=2.1.283 <2.2.0. Run go test -tags
  security ./test/security/... on this version and extend the range, or set
  security.allow_unverified_cli: true to run anyway.` Nothing is called before it.
- `security.allow_unverified_cli: true` turns the refusal into a warning printed in the run
  header (red, every run) and recorded (§7.3). Default false. It does not cover a CLI that
  cannot be executed at all.
- Runs for `run`, `run --stage`, `run --jira`, `resume` and `validate` (which prints the
  version and range).

### 7.3 Record

`run.json` gains `provider_cli: {"claude": {"version": "2.1.289", "verified": true}}`, set at
run start. A resume records what it saw in its `Resume` entry (`provider_cli`, same shape):
the CLI may have been updated between the two.

### 7.4 Weekly live check (H13, optional)

A scheduled CI job installs the latest claude CLI and runs the live permission suite with an
API key from CI secrets; on failure it opens an issue. This is the early warning that the
range must not be extended. Optional because it spends real calls and needs a key in CI.

---

## 8. Pre-run test (H07)

### 8.1 When

All of: the workflow contains both `implement` and `test`; `test.baseline` is not `off`;
the run has no executions (fresh run, or a resume of a run that never executed a step);
the mode is not `plan`. A `--stage` run follows the same rule.

### 8.2 Where

In `executeWorkflow`, after approval, the restore point, the resume baseline and pinning,
after the test sandbox and the step guard are set up, inside the engine context (it counts
toward `execution.timeout`), immediately before `engine.Run`. To make that possible the
sandbox and guard setup move above it; nothing else in the tail moves.

### 8.3 How

Exactly like the test step: `testexec.Runner` (sandbox and `TestEnv`), guard snapshot before
and comparison after; a guard violation stops the run as in the test step. It is **not** an
execution: `run.Executions`, `max_steps` and `max_fix_iterations` are untouched.

### 8.4 Outcome

- Exit 0 → `run.json: pre_run_test {exit_code: 0, duration, sandboxed}`, a console line
  `pre-run test: passed (12.3s)`, and the engine starts.
- Non-zero → the run fails before the engine starts: status `failed`, reason
  `tests fail before any change (pre-run test exited N; output in <log>)`, naming
  `--allow-red-tests` and `test.baseline: off` as the ways out, exit code 4. Its stdout and
  stderr go to `logs/pre-run-test.{stdout,stderr}.log`.
- Non-zero with `run --allow-red-tests` (Q12) → the run goes on: the task is to make the
  failing tests pass. `run.json` records `allow_red_tests: true` and the red `pre_run_test`;
  the first implement prompt gets its bounded output ("already FAILS before any change"),
  until the first test step runs. The run still ends green or not by the test step's exit
  code. The flag is valid with `run` only; a resume continues what the run recorded.
- Such a run is resumable (no executions): a resume re-runs the pre-run test, which
  overwrites `pre_run_test`.

### 8.5 Configuration

```yaml
test:
  baseline: require_green   # require_green (default) | off
```

### 8.6 Known limitation

Files the tests create that are not ignored (coverage reports, snapshots) are part of the
working tree after the pre-run test and appear in the review diff. The same is true of the
test step today. Documented; fixed only by worktree isolation (TODO Q4).

---

## 9. Secret scan (H08, H09)

### 9.1 Configuration

```yaml
security:
  secret_scan:
    mode: stop          # stop (default) | warn | off
    allow_paths: []     # globs whose findings are ignored, e.g. ["testdata/**"]
```

### 9.2 Scanner (H08)

A new package `internal/secretscan` (standard library only):
`Scan(path string, content []byte) []Finding`, `Finding{Rule, Line int, Fingerprint string}`.
The matched text is never part of a `Finding`; `Fingerprint` is
`sha256(rule + "\x00" + path + "\x00" + sha256(match))` in hex — stable across line moves.

Rules (each with an ID used in messages):

| ID | matches |
|---|---|
| `aws-access-key-id` | `\b(AKIA\|ASIA)[0-9A-Z]{16}\b` |
| `aws-secret-key` | `aws.{0,20}(secret\|sk).{0,20}['"][0-9a-zA-Z/+]{40}['"]` (case-insensitive) |
| `private-key` | `-----BEGIN [A-Z ]*PRIVATE KEY-----` |
| `github-token` | `\bgh[pousr]_[A-Za-z0-9]{36,}\b`, `\bgithub_pat_[A-Za-z0-9_]{80,}\b` |
| `gitlab-token` | `\bglpat-[A-Za-z0-9_-]{20,}\b` |
| `slack-token` | `\bxox[baprs]-[A-Za-z0-9-]{10,}\b` |
| `anthropic-key` | `\bsk-ant-[A-Za-z0-9_-]{20,}\b` |
| `openai-key` | `\bsk-(proj-)?[A-Za-z0-9_-]{32,}\b` |
| `stripe-key` | `\b(sk\|rk)_live_[A-Za-z0-9]{20,}\b` |
| `jwt` | `\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b` |
| `url-credentials` | `[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s:@]{3,}@` |
| `generic-assignment` | a name containing `password`, `passwd`, `secret`, `token`, `api_key`, `apikey`, `private_key` assigned (`=`, `:`, `:=`) a quoted literal of ≥ 16 characters with Shannon entropy ≥ 3.5 bits/char |

Not findings: the AWS documentation examples (`AKIAIOSFODNN7EXAMPLE`,
`wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`); a `generic-assignment` value that is a
placeholder (`${...}`, `<...>`, `{{...}}`, all one character repeated, containing
`example`, `changeme`, `dummy`, `fake`, `placeholder`, `xxxx` case-insensitive) or an env
lookup (`os.Getenv(`, `process.env.`, `os.environ`). Files over 1 MiB and binaries are skipped
(and listed as skipped in the result).

### 9.3 What is scanned (H09)

The *added* content of the working tree against HEAD, uncut (independent of §5's budget):
the `+` lines of `git diff HEAD` for tracked files (with their new-file line numbers) and the
whole content of untracked, not ignored files — deny-listed files included (the scan is local;
nothing is sent anywhere). Ignored files are not scanned: they cannot be committed. The runs
directory is excluded. Findings under `allow_paths` are dropped.

### 9.4 Baseline

At run start — after the pre-run test (§8) if it runs, otherwise right after the resume
baseline — the scan runs once and the fingerprints are stored in
`run.json: secret_scan_baseline: ["<hex>", ...]`. Only findings not in the baseline count
later. This covers a dirty start (`require_clean_worktree: false`) and test fixtures the
pre-run test generated. A resume uses the stored baseline and never recomputes it.

### 9.5 When and what happens

After every `implement` (after its guard check) and every `test` step (after its guard
check):

- `stop` → the step fails with a `WorkflowError` like a guard violation (exit 4):
  `implement wrote what looks like a secret; the run stops before anything sends it on:
  internal/foo/config.go:12 (aws-access-key-id) ...` plus the restore point. The changes are
  left in place for inspection.
- `warn` → the run goes on; each new finding is printed and appended to
  `run.json: secret_findings: [{step_index, file, line, rule}]` (deduplicated by fingerprint).
- `off` → nothing runs and nothing is recorded; the run header says `secret scan: off`.

With `stop` and `warn`, the review diff (§5.3) always goes through the scanner's redaction
before it is embedded: every matched value — a new finding in `warn`, or a baseline finding
already in the tree at start — becomes `[REDACTED:<rule>]` and is not sent to the review model.

The value of a finding never appears in output, logs, artifacts or `run.json`.

---

## 10. Configuration summary

```yaml
limits:
  prompt:
    max_diff_bytes: 262144
    max_test_output_bytes: 32768
    diff_summarize: []
test:
  sandbox: bwrap              # default; none must be explicit
  env_pass: []
  baseline: require_green     # require_green | off
security:
  allow_unverified_cli: false
  secret_scan:
    mode: stop                # stop | warn | off
    allow_paths: []
```

All keys are new to the version-2 schema (strict parsing). No config version bump: every key is
optional; the one behavior change for an existing config is the sandbox default (§6.3).

## 11. Persistence

`run.json` additions, each validated strictly on resume like the rest (`persistence`):

| field | type | validation |
|---|---|---|
| `provider_cli` | map provider → `{version, verified}` | known provider names; version ≤ 64 printable bytes |
| `pre_run_test` | `{exit_code, duration, sandboxed}` | present only if §8.1 held |
| `secret_scan_baseline` | list of hex strings | each 64 hex chars; ≤ 10 000 entries |
| `secret_findings` | list of `{step_index, file, line, rule}` | known rule IDs; `step_index` within executions; `file` relative, cleaned, no `..` |
| `resumes[].provider_cli` | as `provider_cli` | as above |

The review artifact gains `diff` (§5.3). Logs gain `logs/pre-run-test.{stdout,stderr}.log`.

## 12. Errors and exit codes

| situation | error | exit |
|---|---|---|
| invalid `limits.prompt.*`, `test.env_pass`, `test.baseline`, `security.secret_scan.*` | config validation | 2 |
| sandbox on (default) and bwrap missing or probe fails | `DependencyError` | 2 |
| CLI version not verified / unparseable / `--version` fails, no override | `DependencyError` | 2 |
| pre-run test exits non-zero | `WorkflowError` | 4 |
| guard violation during the pre-run test | `WorkflowError` | 4 |
| secret found, `mode: stop` | `WorkflowError` | 4 |

## 13. Answered questions (2026-10-05)

| # | question | answer |
|---|---|---|
| Q1 | `test.sandbox` absent | **bwrap by default**; host cannot sandbox → fail fast; explicit `none` warns every run |
| Q2 | default test env | **base + toolchain** allowlist, proxies excluded, operator adds via `test.env_pass` |
| Q3 | verified CLI versions | **minimum + same minor** (`>=2.1.283 <2.2.0`) |
| Q4 | CLI outside the range / unparseable | **refuse, with explicit override** `security.allow_unverified_cli` |
| Q5 | red pre-run test | **refuse by default**, `test.baseline: off` to disable |
| Q6 | new files' content in the review diff | **yes, in phase 0**, deny list and binaries by name only |
| Q7 | lock files and generated files | **stat line only** (default list + config extension) |
| Q8 | secret found | **configurable `stop \| warn \| off`, default stop** |
| Q9 | scanner engine | **built-in rules**; gitleaks later, separately |
| Q10 | merged branches | **delete locally and on origin** (H12) |
| Q11 | language of the spec | English, like the other specs |
| Q12 | a task that is about failing tests, under `require_green` (asked during H07) | **`run --allow-red-tests`** for that one run; the red output goes to the first implement |

## 14. Defaults chosen by the author

| # | default | why |
|---|---|---|
| D1 | Without a sandbox the test keeps the real `$HOME` (only the env is cut) | Toolchains derive caches from `$HOME` (`~/go/pkg/mod`); a fake `$HOME` without a sandbox would re-download everything and protects nothing the filesystem still exposes |
| D2 | `test.env_pass` secret-like names are a hard error, no override | A credential in a model-code run is the thing this phase removes |
| D3 | The pre-run test counts toward `execution.timeout` and is not retried | Deterministic; a flaky suite is the operator's to fix or to `off` |
| D4 | The pre-run test result is not shown to `implement` | It is green whenever implement runs |
| D5 | No hard cap on the total prompt | Each embedded part is bounded; the task text is bounded by its source (Jira `max_bytes`, argv) |
| D6 | `diff_summarize` extends, never replaces, the default list | A config cannot make lock files fully visible again by accident; it is cheap to add a pattern |
| D7′ | Deny-listed files are withheld from the review diff whether tracked or untracked | "Secrets never leave the machine" did not cover a tracked `secrets.yaml` modified by the run |
| D8 | `test.env` (literal values) is not added | Not needed for phase 0; `env_pass` covers real cases |
| D9 | The secret scan also runs after `test`, not only `implement` | Tests run model-written code that can write files |

## 15. Not covered

- Secrets in model prose (artifacts, logs of a provider's stdout), in ignored files, split
  across lines, or encoded (base64, hex).
- `PATH` entries inside the repository (a model could shadow a tool the test command calls);
  the sandbox limits what such a tool can reach.
- macOS sandboxing (TODO P4); codex/gemini prompt delivery (TODO P8).
- Comparing failing test *names* between the pre-run test and the test step (TODO Q7).

## 16. Tests

- **Unit:** `BoundTestOutput` (table + fuzz: output ≤ budget + markers, deterministic, valid
  UTF-8); `ReviewDiff` against real temp repos (untracked text/binary/symlink/deny-listed,
  summarize globs, budget overflow, > 500 untracked); `TestEnv` (allowlist, env_pass,
  credentials removed even if listed); config validation of every new key; version parsing
  and range (`2.1.282`, `2.1.283`, `2.1.999`, `2.2.0`, `2.1.290-beta`, garbage, empty);
  `secretscan` golden positives and negatives per rule, placeholders, AWS examples,
  entropy threshold, fingerprint stability across line moves.
- **Integration** (fake CLI reads the prompt from stdin and answers `--version` from
  `FAKE_CLI_VERSION`): see H10 for the scenario list.
- **Live** (`-tags security`): every authority still holds with the prompt on stdin; the
  test command under bwrap cannot read `~/.ssh` and sees no `ANTHROPIC_API_KEY`.

## 17. Documentation changes (H11)

MVP.md: §7 (version check in the fail-fast chain), §18 (pre-run test, test env), §19 (review
diff, bounds), §25 (`limits.prompt`), §37 (resume re-runs the pre-run test for a run without
executions; records `provider_cli`), §38.3 (sandbox default, allowlist, secret scan), §30
(unchanged). CLAUDE.md invariants: sandbox default and test-env allowlist, version check,
secret scan, prompt on stdin. README, `orchestrator.example.yaml`, TODO.md statuses.

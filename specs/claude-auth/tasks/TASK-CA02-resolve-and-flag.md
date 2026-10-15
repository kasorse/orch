# CA02 — `ResolveClaudeAuth`, `--claude-auth`, console, `validate`

**Size** S (0.5–1 d) · **Depends on** CA01 · **Blocks** CA03

## Goal

One function turns the flag, the config and the environment into the effective mode, inside
the fail-fast chain (`SPEC.md` §4, §6, §12).

## Files

- `internal/config/providers.go` (+ test): `ResolveClaudeAuth(cfg, flag string, lookup func(string) (string, bool)) (ClaudeAuth, []string /*notices*/, error)`
- `internal/domain`: `ClaudeAuth{Requested, Mode, DecidedBy, KeyEnv}` (JSON, no key)
- `internal/app/app.go`: flag parsing next to `--mode`; the call inside the dependency step;
  usage text; `validate` output

## Steps

1. Implement the table of §4 exactly, including the blank-key warning and the
   "configured variable unset but `ANTHROPIC_API_KEY` set" notice (§13 #3).
2. Key checks of §12: trim; reject inner whitespace/control characters and the `sk-ant-oat`
   prefix with the given messages. The error text never contains the value.
3. `--claude-auth X` / `--claude-auth=X`, any position, once; valid only with `run`, `resume`,
   `validate`.
4. Run it from `config.ValidateDependencies` (or right after it, before `ValidateAuthorities`),
   so a bad `api` stops everything before any call; return the key value only to `app`.
5. Print the mode line and notices; `validate` prints the mode line.

## Acceptance

- Table test over every row of §4 and cases 3–5, 13, 16 of §13.
- An app-level test: `--claude-auth api` with the variable unset fails before the fake
  `claude` is called once (no `FAKE_ARGS_DIR` entry).
- `--claude-auth` twice, with a bad value, or on `decisions report` is a usage error.

## Gates

`scripts/check.sh`

## As built

- `ResolveClaudeAuth` is in `internal/config/providers.go`; `domain.ClaudeAuth` (with `Line()`
  for the console text) in `internal/domain/claudeauth.go`. It does not return the key: `app`
  will read the variable itself in CA03, only in `api` mode.
- It runs in `app.loadAndValidateConfig` right after `ValidateDependencies` (whose signature is
  unchanged), before the sandbox probe and `ValidateAuthorities`. The mode line and notices are
  printed by `ConsoleReporter.ClaudeAuth` after "configuration valid", for `run`, `resume` and
  `validate`. `loadAndValidateConfig` now returns the `domain.ClaudeAuth`; callers ignore it
  until CA03/CA05.
- Status: implemented; `scripts/check.sh` is left to the operator.

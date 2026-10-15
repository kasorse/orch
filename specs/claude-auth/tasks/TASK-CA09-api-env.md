# CA09 — `providers.claude.api_env`: the api mode's own environment

**Size** M (1 d) · **Depends on** CA03, CA05 · **Blocks** —

## Goal

In `api` mode the claude child gets the variables `providers.claude.api_env` names (a gateway's
`ANTHROPIC_BASE_URL`, `AWS_PROFILE`, the `ANTHROPIC_DEFAULT_*_MODEL` aliases). In `login` mode
it loses them, so one config works in both modes and a shell export cannot leak across them
(`SPEC.md` §17).

## Files

- `internal/config/providers.go` (+ test): `ClaudeProviderConfig.APIEnv map[string]string`,
  `providersProblems` for the §17.3 rules
- the strict schema of a version-2 file: `providers.claude.api_env`
- `internal/config` governance check (+ test): the model variables against `allowed_models`
  (§17.3, D8)
- `internal/provider/claude/auth.go` (+ test): `SetAuth` takes the map; `buildEnv` sets it in
  `api` and removes its names in `login` (§17.4); the removed names join `IgnoredEnv`
- `internal/app`: pass `APIEnv` to `SetAuth`; the `claude env (api): …` line; the
  `ANTHROPIC_BASE_URL` warning names `api_env` as the source
- `internal/domain`: `ClaudeAuth.APIEnv []string` (names), in `run.json` and `resumes[]`
- `internal/report` / `summary.md`: `, env: N variables`
- `orchestrator.example.yaml`: a commented example with placeholder values
- MVP.md §46, CLAUDE.md (the claude-auth invariant), README

## Steps

1. Config: parse the map. Validate every name and value per §17.3 and report each problem
   with the variable's name and never its value. `internal/secretscan` runs over the values;
   check that `config` may import it (stdlib-only leaf), otherwise call it from
   `ValidateDependencies`.
2. Org layer: with `allowed_models`, every model variable's value must be in the list. Use the
   message shape of a route outside it.
3. Adapter: `buildEnv(environ, mode, key, apiEnv)`. In `api`, set after the §7 table. In
   `login`, remove the names. Sort names for the notice.
4. `app.applyClaudeAuth` hands the map over before `checkProviderCLIs`, so the version probe
   sees the same environment.
5. Persist the names. Print the line from §17.5, with only the host of a URL value shown.

## Acceptance

- Table tests for every §17.3 problem: each forbidden name class, a control character, a URL
  with userinfo, a secretscan finding (the value assembled at run time, no token-shaped
  literal in the repository), a model value outside `allowed_models`.
- `buildEnv` over a fixed environment: `api` sets and overrides; `login` removes a name that
  was exported in the shell and leaves the rest; an empty map changes nothing.
- An integration scenario with `FAKE_ENV_DIR`: in `api` the claude CLI sees every `api_env`
  variable and the test command sees none of them. Under `--claude-auth login` with the same
  config and the variables exported, the claude CLI sees none of them.
- `run.json` holds the names, never a value. `summary.md` has the count.
- Without `api_env`, existing scenarios and golden files are unchanged.

## Gates

`scripts/check.sh`

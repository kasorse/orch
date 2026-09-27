# T04 — `codex`/`gemini`: unverified capabilities, and an example config that runs

**Phase** 1 · **Size** S · **Depends on** T02 · **Blocks** T14

## Goal

Make the two providers that cannot be tested here refuse loudly instead of running with
unknown authority — and stop shipping an example config that this change makes unusable.

Neither `codex` nor `gemini` is installed on the development machine (`which` finds only
`claude`), so per `SPEC.md` §12 rule 1 every capability is `false`.

## Files

- `internal/provider/codex/client.go`, `internal/provider/gemini/client.go`
- their tests
- `orchestrator.example.yaml`
- `README.md` / release note fragment (coordinate with T14)

## What to do

1. Accept `domain.Policy` in `buildArgs` for signature consistency, and write the intended
   translation **as commented-out code with a `TODO(T15)` and the reason**, so whoever
   installs the CLI has the starting point:
   - `codex`: `exec --sandbox read-only|workspace-write --ask-for-approval never|on-request --cd <root>`
   - `gemini`: `--approval-mode default|auto_edit` plus its sandbox flag
2. `Capabilities()` returns the zero value for both. Comment must state *why* (`SPEC.md`
   §12: unverified translation must not ship as verified) rather than reading like an
   oversight — the next person's instinct will be to "fix" it by filling the struct in.
3. `Execute` re-checks `Supports` and refuses, same as T03 step 6. In practice this means
   both adapters currently refuse every policy including `classify`'s, which is correct and
   must be tested rather than special-cased.
4. **Re-point `orchestrator.example.yaml` to `claude` for all actions.** Today it routes
   `review` → codex and `answer` → gemini; after this change that config fails
   `ValidatePolicies` with exit 2 before any AI call. An example that cannot run is a bug
   report generator. Keep the codex/gemini routes present but commented out, with a note
   pointing at §12.
5. Write the release-note paragraph: anyone whose config routes an action at codex or gemini
   will now fail fast with a `PolicyError` and the remedy text from T02. This is intended
   behavior, but it is a breaking change for those users and must be stated plainly, not
   discovered.

## Acceptance

- Both adapters return zero-value `Capabilities`.
- `Execute` on either, with any policy, returns `*apperr.PolicyError` and spawns no process.
- `orchestrator.example.yaml` passes `ValidatePolicies` in every mode (add a test that loads
  the example file and validates it — it is the config most likely to be copied, so it
  should be the config most likely to be tested).
- Release note states the breaking change and both remedies.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

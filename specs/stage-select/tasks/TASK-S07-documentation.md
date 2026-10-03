# S07 — Documentation

**Size** S (0.5 d) · **Depends on** all

## Steps

1. `MVP.md`: new section after §39 (CLI, the decision for a manual selection, gap warnings,
   review on an empty diff); amend §8–§9 (decision flow) and §27 (CLI output).
2. `CLAUDE.md`: add `[--stage]` to the decision-flow diagram; extend the "Decision providers
   return probabilities; Go decides" invariant (a manual selection is the one case where the
   operator supplies the stage set — never an LLM, never the order); note `source: manual`
   in the run artifacts.
3. README and the example config comment: usage line.
4. `specs/stage-select/README.md`: status, task table and graph (as `specs/jira/README.md`);
   mark every task `As built` with what differed.
5. Set `SPEC.md` status to **implemented** and record the §14 answers.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03. MVP.md §40 and amendments to §4, §8 and §9; CLAUDE.md; README; the example
config comment; this folder's status. §27 needed no change, since the console line is
described in §40.3. `TestLoad_ExampleConfigIsValid` still passes.

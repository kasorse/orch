# QL15 — Benchmark configurations E (verify) and F (by_complexity)

**Size** S (1 d + the run) · **TODO** Q2, Q3 (their "done when") · **Depends on** QL14, BM16 ·
**Blocks** —

## Goal

The benchmark measures whether verify raises the pass rate over configuration D
(implement + test) without more guard violations, and whether `by_complexity` lowers cost at
an equal pass rate (`specs/benchmark/SPEC.md`, TODO.md B2 item E).

## Files

- `bench/configs/E.yaml`, `bench/configs/F.yaml`: two new templates next to `A.yaml`,
  `B.yaml`, `C.yaml`, `D.yaml`, `Bmock.yaml` and `common.yaml` (BM09), plus wherever BM09
  registers the configuration names. E = D + `implement.verify: true` (+ the repository's
  checks). F = B + `by_complexity` on implement (simple: a cheaper model, low effort).
- `bench/README.md`: the two configurations
- `bench/results/<date>/summary.md`: the comparison (only `summary.md` is committed)

## Steps

1. Add E and F as templates in the same form as BM09. `bench/` reads only what the
   orchestrator writes; the new `verify_calls`, `outcome.verify_calls` and
   `provider.model_source` fields are already in `run.json` for the operator's sake.
2. Run E, D, F and B on the BM15 task set with the BM16 procedure (3 repeats).
3. Summarise: pass rate, cost, time, guard violations, and verify calls per task for E vs D;
   pass rate and cost for F vs B.
4. Write the result into TODO.md under Q2 and Q3 and tick them (or record why not).

## Acceptance

`summary.md` for the run contains E vs D and F vs B. TODO.md Q2 and Q3 carry the verdict.

## Gates

The four gates in `bench/` (`cd bench && go build ./... && go vet ./... && go test ./... &&
gofmt -l .`) and in the root (`scripts/check.sh`).

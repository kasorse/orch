# CL07 — Scenarios and documentation

**Size** M (2 d) · **Depends on** CL01–CL06

## Goal

The change is covered end to end and documented where an operator and the next developer look.
MVP.md becomes the normative home; this spec stays the design record.

## Files

- `test/integration`: scenarios — a clear task runs without clarify; an unclear task at a
  (simulated) terminal is clarified by the operator and its investigate prompt holds the answer;
  `--mode auto` with instructions that answer the question goes on, without them stops with exit
  4; `run --stage development` on an unclear task runs clarify; a story task never calls
  clarify; an old config with `analysis` fails before any call
- `internal/report/calibrate.go`: `decisions calibrate` shows the `P(clear)` distribution per
  `questions_hash` and the share of runs that stopped `needs input` (no proposal yet, A1)
- MVP.md: new §49 "Clarify" (from this spec's §2–§7, §9–§11); §8 flow, §9 asked stages and
  fallback, §10/§17/§20 actions and prompts, §21/§43 persistence and summary fields, §37 resume
  refusals, §38.1 the clarify call's authority, §40 stage names, §44.4 criteria source, §48 story
  task
- CLAUDE.md: the decision flow, the stage → action table, the invariants (clarify `none`, the
  decision provider sees text only), the run artifacts
- README.md, `orchestrator.example.yaml` (`clarify:` section, `policy.clarify`, the new template)
- `bench/`: configs and README; `bench/configs/*.yaml` gain `clarify: {enabled: false}` (D14: a
  mined task never stops `needs input`, and `P(clear)` is still recorded for calibration); four
  gates in `bench/`
- TODO.md: the entry for this spec ticked, with what was built

## Acceptance

- `scripts/check.sh` passes; the four bwrap-skipped `bench/` tests are run outside the sandbox
  once and recorded in the README's `Status:` line.
- `grep -rn "analysis\|planning" MVP.md CLAUDE.md README.md orchestrator.example.yaml` finds
  only historical mentions (calibration data, design records), each saying so.

## Gates

`scripts/check.sh` (root and `bench/`)

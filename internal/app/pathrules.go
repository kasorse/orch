package app

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"orchestrator/internal/domain"
	"orchestrator/internal/logging"
)

// pathRuleWarnings are the start warnings of specs/governance/SPEC.md §4.2: one per rule
// whose required stages a workflow with implement does not fully contain. They save nothing,
// since the files are not known before implement; they tell the operator while stopping the
// run is still cheap. A workflow without implement changes no file, so it gets none.
func pathRuleWarnings(rules []domain.PathRule, wf domain.Workflow) []string {
	if !wf.Contains(domain.ActionImplement) {
		return nil
	}
	var warnings []string
	for _, rule := range rules {
		missing := domain.MissingStages(rule, wf)
		if len(missing) == 0 {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"policy.paths %q requires %s; this workflow has no %s.\n"+
				"A change there will stop the run. Add the stage with --stage, or start a new run.",
			rule.Match, stageList(rule.RequireStages), stageList(missing)))
	}
	return warnings
}

func stageList(stages []domain.Stage) string {
	names := make([]string, len(stages))
	for i, st := range stages {
		names[i] = string(st)
	}
	return strings.Join(names, ", ")
}

// warnPathRules prints the start warnings for wf.
func warnPathRules(stderr io.Writer, rules []domain.PathRule, wf domain.Workflow) {
	for _, w := range pathRuleWarnings(rules, wf) {
		logging.GapWarning(stderr, w)
	}
}

// pathRulesChanged reports whether the config's rules differ from the ones a run recorded at
// its start; no rules and an empty list are the same.
func pathRulesChanged(recorded, current []domain.PathRule) bool {
	return !slices.EqualFunc(recorded, current, func(a, b domain.PathRule) bool {
		return a.Match == b.Match && a.Approval == b.Approval && slices.Equal(a.RequireStages, b.RequireStages)
	})
}

// pathRulesChangedWarning is the resume warning for a config whose policy.paths moved on.
const pathRulesChangedWarning = "policy.paths changed since the run started; this run keeps the rules it started with"

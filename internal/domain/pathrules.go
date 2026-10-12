package domain

import (
	"fmt"
	"slices"
	"strings"
)

// PathApprovalManual is the one value of a path rule's approval: the commit offer needs a
// person's yes (specs/governance/SPEC.md §4.5).
const PathApprovalManual = "manual"

// PathRule is one policy.paths rule: a change that matches Match needs the stages in
// RequireStages in the workflow, and, with Approval, a person's yes before it is committed
// (specs/governance/SPEC.md §4.1). It is copied into the run when the run is created.
type PathRule struct {
	Match         string  `json:"match"`
	RequireStages []Stage `json:"require_stages,omitempty"`
	Approval      string  `json:"approval,omitempty"`
}

// Validate checks a rule as config.Validate does: a usable pattern, known stages without
// duplicates, a known approval, and at least a stage or an approval.
func (r PathRule) Validate() error {
	if err := ValidateProtectedPattern(r.Match); err != nil {
		return fmt.Errorf("match %q: %w", r.Match, err)
	}
	if err := validateStages(r.RequireStages, true); err != nil {
		return fmt.Errorf("require_stages: %w", err)
	}
	if err := validatePathApproval(r.Approval); err != nil {
		return err
	}
	if len(r.RequireStages) == 0 && r.Approval == "" {
		return fmt.Errorf("rule %q needs require_stages, approval, or both", r.Match)
	}
	return nil
}

func validateStages(stages []Stage, noDuplicates bool) error {
	seen := map[Stage]bool{}
	for _, st := range stages {
		if _, err := ParseStage(string(st)); err != nil {
			return err
		}
		if noDuplicates && seen[st] {
			return fmt.Errorf("duplicate stage %q", st)
		}
		seen[st] = true
	}
	return nil
}

func validatePathApproval(a string) error {
	if a != "" && a != PathApprovalManual {
		return fmt.Errorf("approval %q: want %q or nothing", a, PathApprovalManual)
	}
	return nil
}

// PathRuleMatch is a rule that matched the files of a run's net diff, recorded on the
// implement execution (specs/governance/SPEC.md §4.3). Missing is never nil, so it is
// written as [] when the workflow has every required stage.
type PathRuleMatch struct {
	Match string `json:"match"`
	// Files are the changed files the rule matched, in git's order, at most MaxDiffPaths.
	Files         []string `json:"files"`
	RequireStages []Stage  `json:"require_stages,omitempty"`
	Missing       []Stage  `json:"missing"`
	Approval      string   `json:"approval,omitempty"`
}

// Validate checks a recorded match.
func (m PathRuleMatch) Validate() error {
	if err := ValidateProtectedPattern(m.Match); err != nil {
		return fmt.Errorf("match %q: %w", m.Match, err)
	}
	if len(m.Files) > MaxDiffPaths {
		return fmt.Errorf("files: %d paths, at most %d", len(m.Files), MaxDiffPaths)
	}
	for _, f := range m.Files {
		if !CleanRelPath(f) {
			return fmt.Errorf("files: %q is not a clean repository-relative path", f)
		}
	}
	if err := validateStages(m.RequireStages, false); err != nil {
		return fmt.Errorf("require_stages: %w", err)
	}
	if err := validateStages(m.Missing, false); err != nil {
		return fmt.Errorf("missing: %w", err)
	}
	for _, st := range m.Missing {
		if !slices.Contains(m.RequireStages, st) {
			return fmt.Errorf("missing: %q is not in require_stages", st)
		}
	}
	return validatePathApproval(m.Approval)
}

// MissingStages are the stages of rule that wf does not run, in the rule's order. A stage
// counts as run when wf contains its action, whatever the step's stage name says.
func MissingStages(rule PathRule, wf Workflow) []Stage {
	missing := []Stage{}
	for _, st := range rule.RequireStages {
		if action, ok := st.Action(); !ok || !wf.Contains(action) {
			missing = append(missing, st)
		}
	}
	return missing
}

// MatchPathRules applies rules, in order, to the repository-relative files of a run's net
// diff. Every rule that matches at least one file gives one entry; requirements add up, so
// a file can be in several. Files keeps git's order up to MaxDiffPaths. It is pure: the
// workflow only decides Missing.
func MatchPathRules(rules []PathRule, files []string, wf Workflow) []PathRuleMatch {
	var matches []PathRuleMatch
	for _, rule := range rules {
		var hit []string
		for _, f := range files {
			if MatchProtected(rule.Match, f) {
				hit = append(hit, f)
			}
		}
		if len(hit) == 0 {
			continue
		}
		if len(hit) > MaxDiffPaths {
			hit = hit[:MaxDiffPaths]
		}
		matches = append(matches, PathRuleMatch{
			Match:         rule.Match,
			Files:         hit,
			RequireStages: slices.Clone(rule.RequireStages),
			Missing:       MissingStages(rule, wf),
			Approval:      rule.Approval,
		})
	}
	return matches
}

// PathRuleFailure is the failure_reason of a step whose change matched a rule the workflow
// cannot satisfy (specs/governance/SPEC.md §4.2).
func PathRuleFailure(m PathRuleMatch) string {
	return fmt.Sprintf("policy.paths %q requires %s, which this workflow does not run (changed: %s)",
		m.Match, joinValues(m.Missing), strings.Join(m.Files, ", "))
}

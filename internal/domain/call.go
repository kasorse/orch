package domain

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// CallReport is what a step's provider call was and did, besides what it cost (Usage):
// the prompt the orchestrator sent, and what the CLI reported about the models and tools
// it used. It is recorded in run.json and the run summary (MVP.md §43); nothing decides
// anything from it.
type CallReport struct {
	// PromptBytes is the size of the prompt sent on stdin.
	PromptBytes int `json:"prompt_bytes"`
	// Truncated names the parts of the prompt cut to fit limits.prompt, in prompt order;
	// empty when nothing was.
	Truncated []PromptPart `json:"truncated,omitempty"`
	// Models are the models that actually answered (the configured name is an alias, e.g.
	// "sonnet"), sorted by name; empty when the CLI does not say.
	Models []ModelUse `json:"models,omitempty"`
	// APIMS is the time the call spent waiting on the model API, as the CLI reported it;
	// the rest of the step's time went to tools and the CLI itself. 0 when not reported.
	APIMS int64 `json:"api_ms,omitempty"`
	// Tools counts the model's tool calls by tool name; empty when it made none or the
	// CLI does not say.
	Tools map[string]int `json:"tools,omitempty"`
}

// ModelUse is one model a call used and what it cost; CostUSD is 0 when not reported
// (a call that ended before its final report).
type ModelUse struct {
	Model   string  `json:"model"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// PromptPart names a part of a prompt that limits.prompt can cut.
type PromptPart string

const (
	// PromptTestOutput: a failing test's output in the implement prompt
	// (limits.prompt.max_test_output_bytes).
	PromptTestOutput PromptPart = "test_output"
	// PromptDiff: the review diff, where a file's diff did not fit or a file was not
	// listed (limits.prompt.max_diff_bytes).
	PromptDiff PromptPart = "diff"
)

// Bounds on what a call report may hold, so a run.json read back stays small.
const (
	maxReportNames  = 64
	maxReportString = 200
)

// Validate checks a recorded call report read back from disk.
func (c CallReport) Validate() error {
	if c.PromptBytes < 0 || c.APIMS < 0 {
		return fmt.Errorf("negative counter")
	}
	for _, p := range c.Truncated {
		if p != PromptTestOutput && p != PromptDiff {
			return fmt.Errorf("unknown prompt part %q", p)
		}
	}
	if len(c.Models) > maxReportNames || len(c.Tools) > maxReportNames {
		return fmt.Errorf("more than %d models or tools", maxReportNames)
	}
	for _, m := range c.Models {
		if err := reportName(m.Model); err != nil {
			return fmt.Errorf("model: %w", err)
		}
		if err := (Usage{CostUSD: m.CostUSD}).Validate(); err != nil {
			return fmt.Errorf("model %s: %w", m.Model, err)
		}
	}
	for name, n := range c.Tools {
		if err := reportName(name); err != nil {
			return fmt.Errorf("tool: %w", err)
		}
		if n < 0 {
			return fmt.Errorf("tool %s: negative count", name)
		}
	}
	return nil
}

// reportName checks a model or tool name: non-empty, bounded, printable.
func reportName(s string) error {
	if s == "" || len(s) > maxReportString {
		return fmt.Errorf("name of %d bytes", len(s))
	}
	if strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return fmt.Errorf("name with a non-printable character")
	}
	return nil
}

// DiffStat counts a change between two trees as git sees it. Binary files are counted in
// Files and Binary, never in the line counts.
type DiffStat struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
	Binary     int `json:"binary,omitempty"`
}

// Validate checks a recorded diff stat.
func (d DiffStat) Validate() error {
	if d.Files < 0 || d.Insertions < 0 || d.Deletions < 0 || d.Binary < 0 || d.Binary > d.Files {
		return fmt.Errorf("inconsistent counts %+v", d)
	}
	return nil
}

// MaxDiffPaths bounds each path list of a StepDiff.
const MaxDiffPaths = 50

// StepDiff is what an implement step changed, from git - never from the model's own
// account of it (MVP.md §31.4) - and how that account compares.
type StepDiff struct {
	// Step is the working tree after the step against the working tree before it.
	Step DiffStat `json:"step"`
	// Run is the working tree after the step against the run's restore point
	// (pre_run_ref): everything the run has changed so far. Nil without a restore point.
	Run *DiffStat `json:"run,omitempty"`
	// NotReported are files the step changed that its artifact's changed_files and
	// tests_added leave out; ReportedUnchanged are files those name that the step did not
	// change. Both are repository-relative, at most MaxDiffPaths each, and both are empty
	// when the artifact has no changed_files at all (the model did not answer in JSON).
	NotReported       []string `json:"not_reported,omitempty"`
	ReportedUnchanged []string `json:"reported_unchanged,omitempty"`
}

// Validate checks a recorded step diff.
func (d StepDiff) Validate() error {
	if err := d.Step.Validate(); err != nil {
		return fmt.Errorf("step: %w", err)
	}
	if d.Run != nil {
		if err := d.Run.Validate(); err != nil {
			return fmt.Errorf("run: %w", err)
		}
	}
	for name, list := range map[string][]string{"not_reported": d.NotReported, "reported_unchanged": d.ReportedUnchanged} {
		if len(list) > MaxDiffPaths {
			return fmt.Errorf("%s: %d paths, at most %d", name, len(list), MaxDiffPaths)
		}
		for _, p := range list {
			if !CleanRelPath(p) {
				return fmt.Errorf("%s: %q is not a clean repository-relative path", name, p)
			}
		}
	}
	return nil
}

// CleanRelPath reports whether p is a clean, printable, slash-separated path inside the
// repository, at most 512 bytes.
func CleanRelPath(p string) bool {
	if p == "" || len(p) > 512 || path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return strings.IndexFunc(p, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}

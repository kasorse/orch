package domain

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunStopped   RunStatus = "stopped"
)

// Valid reports whether s is one of the defined run statuses.
func (s RunStatus) Valid() bool {
	switch s {
	case RunPending, RunRunning, RunCompleted, RunFailed, RunStopped:
		return true
	}
	return false
}

// ProviderRef identifies the provider call a step made: which CLI, which model, and the
// authority it ran with (specs/permissions/SPEC.md §5), so run.json answers "what was this
// step allowed to do" after the fact.
type ProviderRef struct {
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Effort    string    `json:"effort,omitempty"`
	Authority Authority `json:"authority,omitempty"`
	// ModelSource says where the model and effort came from: ModelSourceBase,
	// ModelSourceByComplexity + the decided complexity (specs/quality/SPEC.md §9.3), or
	// ModelSourceEscalated + the level above it for an implement that fixes a failed test or
	// review (specs/decision-shape/SPEC.md §8), or ModelSourceByComplexity + the decided
	// complexity + ModelSourceEscalateSuffix when that level's entry names its own escalation
	// (§8.1). Empty in a run written before the field.
	ModelSource string `json:"model_source,omitempty"`
}

// Values of ProviderRef.ModelSource.
const (
	ModelSourceBase         = "base"
	ModelSourceByComplexity = "by_complexity."
	ModelSourceEscalated    = "escalated."
	// ModelSourceEscalateSuffix ends the source of a fix routed by its level's own escalate.
	ModelSourceEscalateSuffix = ".escalate"
)

// ValidModelSource reports whether s is a model source a run records: base, by_complexity
// with one of the complexities (with or without ModelSourceEscalateSuffix), or escalated to one
// above simple.
func ValidModelSource(s string) bool {
	if s == ModelSourceBase {
		return true
	}
	if level, ok := strings.CutPrefix(s, ModelSourceByComplexity); ok {
		_, err := ParseComplexity(strings.TrimSuffix(level, ModelSourceEscalateSuffix))
		return err == nil
	}
	if level, ok := strings.CutPrefix(s, ModelSourceEscalated); ok {
		c, err := ParseComplexity(level)
		return err == nil && c != ComplexitySimple
	}
	return false
}

type StepExecStatus string

const (
	StepPending   StepExecStatus = "pending"
	StepRunning   StepExecStatus = "running"
	StepCompleted StepExecStatus = "completed"
	StepFailed    StepExecStatus = "failed"
)

// Valid reports whether s is one of the defined step statuses.
func (s StepExecStatus) Valid() bool {
	switch s {
	case StepPending, StepRunning, StepCompleted, StepFailed:
		return true
	}
	return false
}

// StepExecution is one recorded attempt at an action. Run.Executions is append-only -
// retries produce new entries rather than overwriting previous ones.
type StepExecution struct {
	Index        int            `json:"index"`
	Action       ActionType     `json:"action"`
	Provider     *ProviderRef   `json:"provider,omitempty"`
	Status       StepExecStatus `json:"status"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
	ArtifactPath string         `json:"artifact_path,omitempty"`
	TestResult   *TestResult    `json:"test_result,omitempty"`
	ReviewResult *ReviewResult  `json:"review_result,omitempty"`
	// Usage is what the step's provider call cost, as the CLI reported it; nil for a step
	// without a call (test) or a CLI that reports nothing. A call that failed after the
	// CLI reported usage still records it.
	Usage *Usage `json:"usage,omitempty"`
	// Call is the step's provider call beyond its cost: prompt size and cuts, the models
	// and tools it used. Nil for a step without a call (test).
	Call *CallReport `json:"call,omitempty"`
	// Diff is what an implement step changed, from git. Nil for every other step, and for
	// an implement whose call failed or whose changes the guard refused: git runs on the
	// working tree only after the guard has passed it.
	Diff *StepDiff `json:"diff,omitempty"`
	// TreeAfter is the git tree id of the working tree after a completed implement, which the
	// no-progress check compares between implements (specs/quality/SPEC.md §8.1). Empty for
	// every other step, when git could not compute it, and in a run written before the field
	// existed.
	TreeAfter string `json:"tree_after,omitempty"`
	// VerifyCalls are the calls the implement session made to the verify tools, in order, also
	// when the step failed (specs/quality/SPEC.md §12). Empty for every other step.
	VerifyCalls []VerifyCall `json:"verify_calls,omitempty"`
	// PathRules are the policy.paths rules the run's net diff matched after this implement,
	// also when the step failed because of them (specs/governance/SPEC.md §4.3). Empty for
	// every other step.
	PathRules []PathRuleMatch `json:"path_rules,omitempty"`
	Error     string          `json:"error,omitempty"`
	// Attempt is 0 for a step the original process ran and n for one run by the n-th
	// resume (specs/resume/SPEC.md §12).
	Attempt int `json:"attempt,omitempty"`
}

// Resume records one `orchestrator resume` of a run: what state it was found in and
// where it continued. Run.Resumes is append-only, like Run.Executions.
type Resume struct {
	At time.Time `json:"at"`
	// FromStatus is the status the run was found in; "running" means the previous process
	// died mid-run (interrupted).
	FromStatus RunStatus  `json:"from_status"`
	FromReason string     `json:"from_reason,omitempty"`
	Entry      ActionType `json:"entry"`
	Why        string     `json:"why"`
	Mode       RunMode    `json:"mode"`
	// ProviderCLI is what the fail-fast version check saw in this process: the CLI may
	// have been updated since the run started (specs/hardening/SPEC.md §7.3).
	ProviderCLI map[string]ProviderCLI `json:"provider_cli,omitempty"`
	// OrgConfig is the org file this resume ran under; nil when there was none
	// (specs/governance/SPEC.md §6.3).
	OrgConfig *OrgConfigRef `json:"org_config,omitempty"`
	// ClaudeAuth is the credential mode this resume ran under; a record, never an input
	// (specs/claude-auth/SPEC.md §11). Nil in a run written before the field.
	ClaudeAuth *ClaudeAuth `json:"claude_auth,omitempty"`
}

// OrgConfigRef names the org config file a run or a resume ran under and the sha256 of its
// bytes, which is the evidence of which policy applied (specs/governance/SPEC.md §6.3).
type OrgConfigRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Validate checks a recorded reference: an absolute, clean path and a 64-digit lowercase hex
// sha256.
func (o OrgConfigRef) Validate() error {
	if !filepath.IsAbs(o.Path) || filepath.Clean(o.Path) != o.Path {
		return fmt.Errorf("path %q is not an absolute, clean path", o.Path)
	}
	if len(o.SHA256) != 64 {
		return fmt.Errorf("sha256 %q is not 64 hex digits", o.SHA256)
	}
	for _, c := range o.SHA256 {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("sha256 %q is not lowercase hex", o.SHA256)
		}
	}
	return nil
}

// ProviderCLI is one provider CLI as the version check found it: the version string the
// CLI reported and whether it lies in the range its permission flags were verified on.
type ProviderCLI struct {
	Version  string `json:"version"`
	Verified bool   `json:"verified"`
}

type Run struct {
	ID   string `json:"id"`
	Task Task   `json:"task"`
	// Decision is the policy's verdict the workflow was built from; decision.json holds
	// the full trace behind it (TASKS.md T11).
	Decision *Decision `json:"decision,omitempty"`
	Workflow *Workflow `json:"workflow,omitempty"`
	// Mode is the execution mode this run actually ran under (MVP.md §1/§27).
	Mode RunMode `json:"mode,omitempty"`
	// PreRunRef is the restore point recorded before the first step: HEAD on a clean
	// tree, or a snapshot commit of the uncommitted changes (untracked files included)
	// the run started from (specs/permissions/SPEC.md §7.2).
	PreRunRef string `json:"pre_run_ref,omitempty"`
	// HeadRef is HEAD when the first step started. A resume refuses when HEAD has moved,
	// since review diffs against HEAD (specs/resume/SPEC.md §9.2).
	HeadRef string `json:"head_ref,omitempty"`
	// ResumeBaseline is the repository guard's state when the first step started: the
	// git fingerprint and the protected paths. A resume compares against it before
	// anything runs (specs/resume/SPEC.md §9.1). Its shape belongs to the guard, not to
	// domain.
	ResumeBaseline json.RawMessage `json:"resume_baseline,omitempty"`
	// ProviderCLI is the CLI version of each routed provider at run start.
	ProviderCLI map[string]ProviderCLI `json:"provider_cli,omitempty"`
	// OrgConfig is the org file the run started under; nil when there was none
	// (specs/governance/SPEC.md §6.3).
	OrgConfig *OrgConfigRef `json:"org_config,omitempty"`
	// ClaudeAuth is the credential mode the run started under (specs/claude-auth/SPEC.md §10);
	// a record, never read back to decide anything. Nil in a run written before the field.
	ClaudeAuth *ClaudeAuth `json:"claude_auth,omitempty"`
	// Resumes is append-only: one entry per `orchestrator resume` of this run.
	Resumes []Resume `json:"resumes,omitempty"`
	// AllowRedTests is run --allow-red-tests: a red pre-run test does not stop the run, its
	// output goes to the first implement instead (specs/hardening/SPEC.md §8.4).
	AllowRedTests bool `json:"allow_red_tests,omitempty"`
	// Options are the config rules the run follows, recorded once when it is created and never
	// changed by a resume (specs/quality/SPEC.md §7.3). Omitted while they are all off.
	Options RunOptions `json:"options,omitzero"`
	// PreRunTest is the test command's result before the first step, when the workflow
	// implements and tests (specs/hardening/SPEC.md §8). It is not an execution.
	// SecretScanBaseline are the fingerprints of what looked like secrets in the working
	// tree when the run started; only findings not among them count (specs/hardening/SPEC.md
	// §9.4).
	SecretScanBaseline []string `json:"secret_scan_baseline,omitempty"`
	// SecretFindings are the new findings a run with security.secret_scan.mode: warn went on
	// with. Never the value.
	SecretFindings []SecretFinding `json:"secret_findings,omitempty"`
	PreRunTest     *TestResult     `json:"pre_run_test,omitempty"`
	Executions     []StepExecution `json:"executions"`
	Status         RunStatus       `json:"status"`
	FailureReason  string          `json:"failure_reason,omitempty"`
	TotalSteps     int             `json:"total_steps"`
	FixIterations  int             `json:"fix_iterations"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	// Outcome is how the run ended and what it cost, written when it reaches a terminal
	// status and recomputed over the whole history by a resume (ComputeOutcome).
	Outcome *Outcome `json:"outcome,omitempty"`
	// Commit is the commit the operator approved at the end of a completed run
	// (MVP.md §42); empty when nothing was committed.
	Commit string `json:"commit,omitempty"`
}

// RunOptions are the config choices that change how a run moves between steps. They are
// copied into the run when it is created, so a resume follows the rule the run started with
// whatever the config says now.
type RunOptions struct {
	// ReviewFixMedium is review.fix_medium: a passing review with a medium issue sends the run
	// back to implement once.
	ReviewFixMedium bool `json:"review_fix_medium,omitempty"`
	// PathRules is a copy of policy.paths (specs/governance/SPEC.md §4.4). Nil, never empty,
	// when there are none, so a run without rules writes no options.
	PathRules []PathRule `json:"path_rules,omitempty"`
}

// SecretFinding is one likely secret a step added: where, which rule, and the fingerprint
// that identifies it across steps and resumes (secretscan.Finding). Never the value.
type SecretFinding struct {
	StepIndex   int    `json:"step_index"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Rule        string `json:"rule"`
	Fingerprint string `json:"fingerprint"`
}

package domain

import (
	"encoding/json"
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
	Error        string         `json:"error,omitempty"`
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
	// Resumes is append-only: one entry per `orchestrator resume` of this run.
	Resumes       []Resume        `json:"resumes,omitempty"`
	Executions    []StepExecution `json:"executions"`
	Status        RunStatus       `json:"status"`
	FailureReason string          `json:"failure_reason,omitempty"`
	TotalSteps    int             `json:"total_steps"`
	FixIterations int             `json:"fix_iterations"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

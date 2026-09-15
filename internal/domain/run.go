package domain

import "time"

type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunStopped   RunStatus = "stopped"
)

type ProviderRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type StepExecStatus string

const (
	StepPending   StepExecStatus = "pending"
	StepRunning   StepExecStatus = "running"
	StepCompleted StepExecStatus = "completed"
	StepFailed    StepExecStatus = "failed"
)

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
}

type Run struct {
	ID             string          `json:"id"`
	Task           Task            `json:"task"`
	Classification *Classification `json:"classification,omitempty"`
	// OriginalClassification preserves the classifier's raw output for audit, and is
	// only set once the user edits Classification during the approve/edit loop or a
	// manual-mode step gate.
	OriginalClassification *Classification `json:"original_classification,omitempty"`
	Workflow               *Workflow       `json:"workflow,omitempty"`
	// Mode is the execution mode this run actually ran under (MVP.md §1/§27).
	Mode          RunMode         `json:"mode,omitempty"`
	Executions    []StepExecution `json:"executions"`
	Status        RunStatus       `json:"status"`
	FailureReason string          `json:"failure_reason,omitempty"`
	TotalSteps    int             `json:"total_steps"`
	FixIterations int             `json:"fix_iterations"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

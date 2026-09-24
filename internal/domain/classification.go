package domain

import "fmt"

type TaskType string

const (
	TaskQuestion TaskType = "question"
	TaskBugfix   TaskType = "bugfix"
	TaskFeature  TaskType = "feature"
	TaskRefactor TaskType = "refactor"
	TaskTest     TaskType = "test"
	TaskReview   TaskType = "review"
)

var validTaskTypes = map[TaskType]bool{
	TaskQuestion: true,
	TaskBugfix:   true,
	TaskFeature:  true,
	TaskRefactor: true,
	TaskTest:     true,
	TaskReview:   true,
}

// LegacyComplexity is the pre-Jev classifier's complexity scale. It is kept only
// until the old classifier flow is removed (specs/JEV integration/TASKS.md T12);
// new code uses Complexity (simple/medium/complex).
type LegacyComplexity string

const (
	LegacyComplexityLow    LegacyComplexity = "low"
	LegacyComplexityMedium LegacyComplexity = "medium"
	LegacyComplexityHigh   LegacyComplexity = "high"
)

var validLegacyComplexities = map[LegacyComplexity]bool{
	LegacyComplexityLow:    true,
	LegacyComplexityMedium: true,
	LegacyComplexityHigh:   true,
}

type Classification struct {
	Type               TaskType         `json:"type"`
	Complexity         LegacyComplexity `json:"complexity"`
	RequiresCodeChange bool             `json:"requires_code_change"`
	RequiresTests      bool             `json:"requires_tests"`
	RequiresReview     bool             `json:"requires_review"`
}

func (c Classification) Validate() error {
	if !validTaskTypes[c.Type] {
		return fmt.Errorf("unsupported task type %q", c.Type)
	}
	if !validLegacyComplexities[c.Complexity] {
		return fmt.Errorf("unsupported complexity %q", c.Complexity)
	}
	return nil
}

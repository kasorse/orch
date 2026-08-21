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

type Complexity string

const (
	ComplexityLow    Complexity = "low"
	ComplexityMedium Complexity = "medium"
	ComplexityHigh   Complexity = "high"
)

var validComplexities = map[Complexity]bool{
	ComplexityLow:    true,
	ComplexityMedium: true,
	ComplexityHigh:   true,
}

type Classification struct {
	Type               TaskType   `json:"type"`
	Complexity         Complexity `json:"complexity"`
	RequiresCodeChange bool       `json:"requires_code_change"`
	RequiresTests      bool       `json:"requires_tests"`
	RequiresReview     bool       `json:"requires_review"`
}

func (c Classification) Validate() error {
	if !validTaskTypes[c.Type] {
		return fmt.Errorf("unsupported task type %q", c.Type)
	}
	if !validComplexities[c.Complexity] {
		return fmt.Errorf("unsupported complexity %q", c.Complexity)
	}
	return nil
}

package action

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// The apply* methods are the only places the Dispatcher's per-run context changes. The
// live execute* paths and Restore both go through them, so a resumed run gets exactly the
// context the original process had (specs/resume/SPEC.md §7.3).

func (d *Dispatcher) applyResearch(a domain.ResearchArtifact) { d.lastResearch = &a }

func (d *Dispatcher) applyPlan(a domain.PlanArtifact) { d.lastPlan = &a }

func (d *Dispatcher) applyImplementation(a domain.ImplementationArtifact) {
	d.lastImpl = &a
	if d.commitImpl == nil && strings.TrimSpace(a.SummaryShort) != "" {
		d.commitImpl = &a
	}
	// Consumed - do not resend stale retry context to a later, unrelated implement call.
	d.pendingTestFailure = nil
	d.pendingReviewIssues = nil
	d.pendingUnmetCriteria = nil
	d.pendingMediumIssues = nil
}

func (d *Dispatcher) applyTest(r domain.TestResult) {
	d.lastTest = &r
	if r.Success {
		d.pendingTestFailure = nil
	} else {
		d.pendingTestFailure = &r
	}
}

// applyPreRunTest remembers a red pre-run test for implement; a green one clears it.
func (d *Dispatcher) applyPreRunTest(r domain.TestResult) {
	if r.Success {
		d.preRunFailure = nil
	} else {
		d.preRunFailure = &r
	}
}

func (d *Dispatcher) applyReview(r domain.ReviewResult) {
	if r.Decide() == domain.ReviewFail {
		d.pendingReviewIssues = r.Issues
		d.pendingUnmetCriteria = r.UnmetCriteria()
		d.pendingMediumIssues = nil
	} else {
		d.pendingReviewIssues = nil
		d.pendingUnmetCriteria = nil
		d.pendingMediumIssues = r.MediumIssues()
	}
}

// ArtifactReader reads back the artifact a step saved. Implementations derive the file
// from the index and action - never from a path stored in run.json - and return an error
// matching fs.ErrNotExist when there is none.
type ArtifactReader interface {
	ReadArtifact(runID string, stepIndex int, action domain.ActionType) ([]byte, error)
}

// Restore rebuilds the Dispatcher's per-run context from a persisted run, for a resume.
// It replays the state changes of every completed execution in order, without calling
// anything; failed executions changed nothing live and are skipped here too (a failed
// implement did not consume the pending retry context, so its retry gets the same one).
//
// research/plan/implement context comes from their artifacts, decoded strictly.
// Test and review results come from run.json, where the engine recorded them; when their
// artifact exists it must agree. Anything missing or inconsistent refuses the resume
// rather than continuing with a thinner prompt.
func (d *Dispatcher) Restore(run *domain.Run, r ArtifactReader) error {
	d.lastResearch, d.lastPlan, d.lastImpl, d.lastTest = nil, nil, nil, nil
	d.commitImpl = nil
	d.pendingTestFailure, d.pendingReviewIssues, d.pendingUnmetCriteria, d.preRunFailure = nil, nil, nil, nil
	d.pendingMediumIssues = nil
	if run.PreRunTest != nil {
		d.applyPreRunTest(*run.PreRunTest)
	}

	for _, exec := range run.Executions {
		if exec.Status != domain.StepCompleted {
			continue
		}
		if err := d.restoreOne(run.ID, exec, r); err != nil {
			return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID,
				Msg: fmt.Sprintf("cannot restore step %03d (%s)", exec.Index, exec.Action), Err: err}
		}
	}
	return nil
}

func (d *Dispatcher) restoreOne(runID string, exec domain.StepExecution, r ArtifactReader) error {
	switch exec.Action {
	case domain.ActionResearch:
		var a domain.ResearchArtifact
		if err := readArtifact(r, runID, exec, &a); err != nil {
			return err
		}
		d.applyResearch(a)
	case domain.ActionPlan:
		var a domain.PlanArtifact
		if err := readArtifact(r, runID, exec, &a); err != nil {
			return err
		}
		d.applyPlan(a)
	case domain.ActionImplement:
		var a domain.ImplementationArtifact
		if err := readArtifact(r, runID, exec, &a); err != nil {
			return err
		}
		d.applyImplementation(a)
	case domain.ActionTest:
		if exec.TestResult == nil {
			return errors.New("run.json has no test result for it")
		}
		if err := matchOptionalArtifact(r, runID, exec, *exec.TestResult); err != nil {
			return err
		}
		d.applyTest(*exec.TestResult)
	case domain.ActionReview:
		if exec.ReviewResult == nil {
			return errors.New("run.json has no review result for it")
		}
		if err := exec.ReviewResult.Validate(); err != nil {
			return err
		}
		if err := matchOptionalArtifact(r, runID, exec, *exec.ReviewResult); err != nil {
			return err
		}
		d.applyReview(*exec.ReviewResult)
	default:
		return fmt.Errorf("no restore for action %q", exec.Action)
	}
	return nil
}

// readArtifact decodes a step's required artifact strictly into v.
func readArtifact(r ArtifactReader, runID string, exec domain.StepExecution, v any) error {
	data, err := r.ReadArtifact(runID, exec.Index, exec.Action)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("its artifact is missing")
	}
	if err != nil {
		return err
	}
	return decodeStrict(data, v)
}

// matchOptionalArtifact checks that a test or review artifact, when there is one, says
// the same as run.json. It is optional because artifact writes are best-effort.
func matchOptionalArtifact[T any](r ArtifactReader, runID string, exec domain.StepExecution, want T) error {
	data, err := r.ReadArtifact(runID, exec.Index, exec.Action)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var got T
	if err := decodeStrict(data, &got); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return errors.New("its artifact does not match run.json")
	}
	return nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid artifact: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid artifact: data after the object")
	}
	return nil
}

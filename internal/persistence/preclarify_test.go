package persistence

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
)

const preClarifyRunID = "3f2a9c1e-8d4b-4c6a-9e21-5b7f0a3c9d10"

// A run whose workflow, executions or decision name analysis is refused with the Approach §6
// message, not with a complaint about an unknown value.
func TestDecodeRun_RefusesARunStartedBeforeClarify(t *testing.T) {
	head := `{"id":"` + preClarifyRunID + `","status":"stopped",`
	for name, body := range map[string]string{
		"workflow step": head + `"workflow":{"steps":[{"action":"analyze","stage":"analysis"},{"action":"implement","stage":"development"}]}}`,
		"execution":     head + `"executions":[{"index":0,"action":"analyze","status":"completed"}]}`,
		"decision":      head + `"decision":{"stages":{"stages":[{"stage":"analysis","forced":false,"enabled":true,"reason":"p"}]}}}`,
		"resume entry":  head + `"resumes":[{"entry":"analyze"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeRun([]byte(body), preClarifyRunID)
			var rerr *apperr.RunStateError
			if !errors.As(err, &rerr) || rerr.Kind != apperr.RunNotResumable {
				t.Fatalf("err = %v, want a not-resumable run state error", err)
			}
			if !strings.Contains(err.Error(), "this run was started before specs/clarify; it cannot be resumed by this version") {
				t.Errorf("err = %v, want the Approach §6 message", err)
			}
		})
	}
}

// decision.json of such a run is refused the same way, by a stage or by the workflow list.
func TestDecodeDecision_RefusesATraceWithAnalysis(t *testing.T) {
	for name, body := range map[string]string{
		"stage":    `{"stages":[{"stage":"analysis","forced":false,"enabled":true,"reason":"p"}],"workflow":["implement"]}`,
		"workflow": `{"stages":[],"workflow":["analyze","implement"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeDecision([]byte(body), preClarifyRunID)
			var rerr *apperr.RunStateError
			if !errors.As(err, &rerr) || rerr.Kind != apperr.RunNotResumable ||
				!strings.Contains(err.Error(), "started before specs/clarify") {
				t.Errorf("err = %v, want the Approach §6 refusal", err)
			}
		})
	}
}

// A file that names no removed action or stage goes on to the strict decoder as before.
func TestDecodeRun_OtherFilesAreNotRefusedAsOld(t *testing.T) {
	_, err := decodeRun([]byte(`{"id":"`+preClarifyRunID+`","status":"stopped","bogus":1}`), preClarifyRunID)
	var rerr *apperr.RunStateError
	if !errors.As(err, &rerr) || rerr.Kind != apperr.RunCorrupt {
		t.Errorf("err = %v, want the strict decoder's corrupt error", err)
	}
}

package persistence

import "encoding/json"

// startedBeforeClarifyMsg is the refusal for a run whose workflow or decision still names the
// analysis or planning stage or the analyze or plan action, which specs/clarify removed or
// renamed (Approach §6).
const startedBeforeClarifyMsg = "this run was started before specs/clarify; it cannot be resumed by this version"

// removedActions and removedStages are the names specs/clarify took out or renamed. A file
// naming one is refused whole, with startedBeforeClarifyMsg, instead of failing on the unknown
// value or reading plan as investigate.
var (
	removedActions = map[string]bool{"analyze": true, "plan": true}
	removedStages  = map[string]bool{"analysis": true, "planning": true}
)

// probe lists the action and stage names a file holds. It reads loosely, only the fields it
// looks at, so the check never fails on a field it does not know; ok is false for a file that
// does not decode, which the strict decoder then reports.
type probe func(data []byte) (actions, stages []string, ok bool)

// runProbe reads run.json: the workflow's steps, the executions, the resumes' entries and the
// decision's stages.
func runProbe(data []byte) (actions, stages []string, ok bool) {
	var p struct {
		Workflow *struct {
			Steps []struct {
				Action string `json:"action"`
				Stage  string `json:"stage"`
			} `json:"steps"`
		} `json:"workflow"`
		Executions []struct {
			Action string `json:"action"`
		} `json:"executions"`
		Resumes []struct {
			Entry string `json:"entry"`
		} `json:"resumes"`
		Decision *struct {
			Stages struct {
				Stages []struct {
					Stage string `json:"stage"`
				} `json:"stages"`
			} `json:"stages"`
		} `json:"decision"`
	}
	if json.Unmarshal(data, &p) != nil {
		return nil, nil, false
	}
	if p.Workflow != nil {
		for _, s := range p.Workflow.Steps {
			actions, stages = append(actions, s.Action), append(stages, s.Stage)
		}
	}
	for _, e := range p.Executions {
		actions = append(actions, e.Action)
	}
	for _, r := range p.Resumes {
		actions = append(actions, r.Entry)
	}
	if p.Decision != nil {
		for _, s := range p.Decision.Stages.Stages {
			stages = append(stages, s.Stage)
		}
	}
	return actions, stages, true
}

// traceProbe reads decision.json: the stages' outcomes and the workflow's list of actions.
func traceProbe(data []byte) (actions, stages []string, ok bool) {
	var p struct {
		Stages []struct {
			Stage string `json:"stage"`
		} `json:"stages"`
		Workflow []string `json:"workflow"`
	}
	if json.Unmarshal(data, &p) != nil {
		return nil, nil, false
	}
	for _, s := range p.Stages {
		stages = append(stages, s.Stage)
	}
	return p.Workflow, stages, true
}

// startedBeforeClarify reports whether the file p reads names a removed action or stage.
func startedBeforeClarify(data []byte, p probe) bool {
	actions, stages, ok := p(data)
	if !ok {
		return false
	}
	for _, a := range actions {
		if removedActions[a] {
			return true
		}
	}
	for _, s := range stages {
		if removedStages[s] {
			return true
		}
	}
	return false
}

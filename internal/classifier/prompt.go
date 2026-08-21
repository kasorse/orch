package classifier

import "fmt"

// BuildClassifyPrompt builds the classifier prompt. The literal "ACTION: classify"
// first line doubles as the routing marker fake executables grep on in integration
// tests, matching every other Build*Prompt function in internal/action.
func BuildClassifyPrompt(task string) string {
	return fmt.Sprintf(`ACTION: classify

You are classifying a single software engineering task for a deterministic workflow
orchestrator. Respond with ONLY a single JSON object, no markdown fences, no prose
before or after it, matching exactly this shape:

{
  "type": "bugfix",
  "complexity": "medium",
  "requires_code_change": true,
  "requires_tests": true,
  "requires_review": true
}

"type" must be exactly one of: question, bugfix, feature, refactor, test, review.
"complexity" must be exactly one of: low, medium, high.
Do not invent any other fields or values.

Task:
%s
`, task)
}

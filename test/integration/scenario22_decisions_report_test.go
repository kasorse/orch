package integration

import (
	"regexp"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 22 (TASKS.md T18, reduced): decisions report compares the two complexity
// questions over the decision.json files runs already wrote, and calls nothing.
func TestScenario22_DecisionsReport(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, "/nonexistent/orchestrator.yaml", "decisions", "report")
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "no decision traces yet") {
		t.Errorf("before any run:\n%s", stdout)
	}

	agree := scriptReadOnly() // choice and score both simple
	disagree := scriptReadOnly()
	disagree.Score = &[3]float64{0.05, 0.15, 0.8} // score: complex
	for _, script := range []jevScript{agree, disagree} {
		j := startJev(t, script)
		stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "explain Add in fixture.go")
		mustSucceed(t, stdout, stderr, err)
	}
	j := startJev(t, agree)
	stdout, stderr, err = runOrchestrator(t, repoDir, jevConfig(t, j), "run", "--stage", "planning", "explain Sub")
	mustSucceed(t, stdout, stderr, err)
	before := invocations(t, invocationsLog)
	requests := j.ModelRequests() + len(j.Bodies())

	// The report needs no configuration: the --config path does not exist.
	stdout, stderr, err = runOrchestrator(t, repoDir, "/nonexistent/orchestrator.yaml", "decisions", "report")
	mustSucceed(t, stdout, stderr, err)
	for _, want := range []string{
		"traces: 3, compared: 2",
		"skipped: 1 manual (--stage)",
		"choice vs score: 1 of 2 agree (50.0%)",
		"disagreements (1):",
		`choice=simple  score=complex "explain Add in fixture.go"`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, stdout)
		}
	}
	// Both runs were decided under one policy: one decider, keyed by the short policy hash.
	if re := regexp.MustCompile(`decided by: jev jev/jev-1\.13\.0 [0-9a-f]{12} \(2\)\n`); !re.MatchString(stdout) {
		t.Errorf("report lacks the decider with its policy hash:\n%s", stdout)
	}
	if got := invocations(t, invocationsLog); len(got) != len(before) {
		t.Errorf("the report called a CLI: %v", got[len(before):])
	}
	if j.ModelRequests()+len(j.Bodies()) != requests {
		t.Error("the report contacted Jev")
	}
}

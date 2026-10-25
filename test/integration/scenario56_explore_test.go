package integration

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 56 (specs/governance/SPEC.md §12, §7.4, GV06): with policy.explore a stage whose
// probability sits just below its threshold is turned on for the runs whose id hashes under
// rate, and recorded as explored; the other runs leave it off.
//
// A run id is random and cannot be injected, so the test does not fix one. It runs fresh
// fixtures until it has seen a run the draw turns the stage on for and one it does not, and
// checks every run against the draw computed from the run's own id.

const (
	exploreRate = 0.5
	// maxExploreRuns bounds the search: each run falls on either side with probability 0.5, so
	// not seeing both in this many runs has a probability of 2^-29.
	maxExploreRuns = 30
)

// exploreDrawsStage is the draw of SPEC §7.4: fnv64a(runID + "/" + stage) % 1000 below rate x 1000.
func exploreDrawsStage(runID, stage string) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(runID + "/" + stage))
	return float64(h.Sum64()%1000) < exploreRate*1000
}

type exploreDecisionJSON struct {
	PolicyHash string `json:"policy_hash"`
	Stages     []struct {
		Stage    string `json:"stage"`
		Enabled  bool   `json:"enabled"`
		Explored bool   `json:"explored"`
		Reason   string `json:"reason"`
	} `json:"stages"`
}

// exploreOnce makes one run with research at P 0.57 (threshold 0.60, band 0.05) and checks it
// against the draw for its run id. It returns whether research was explored.
func exploreOnce(t *testing.T) bool {
	t.Helper()
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "research", readOnlyResponse)
	script := scriptBugfixMedium()
	script.Stages["research"] = 0.57
	cfg := jevConfig(t, startJev(t, script), func(o *configOptions) {
		o.explore = fmt.Sprintf("  explore:\n    band: 0.05\n    rate: %v\n", exploreRate)
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadQL(t, repoDir)
	want := exploreDrawsStage(run.ID, "research")

	raw, ok := runFile(t, repoDir, "decision.json")
	if !ok {
		t.Fatal("decision.json was not written")
	}
	var d exploreDecisionJSON
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.PolicyHash) != 64 || strings.Trim(d.PolicyHash, "0123456789abcdef") != "" {
		t.Errorf("policy_hash = %q, want 64 lowercase hex digits", d.PolicyHash)
	}
	var research bool
	for _, s := range d.Stages {
		if s.Stage != "research" {
			if s.Explored {
				t.Errorf("stage %s is explored, but only research sits in the band", s.Stage)
			}
			continue
		}
		research = true
		if s.Enabled != want || s.Explored != want {
			t.Errorf("run %s: research enabled %v, explored %v; the draw says %v", run.ID, s.Enabled, s.Explored, want)
		}
		if got := strings.HasPrefix(s.Reason, "explored (P 0.57, threshold 0.60)"); got != want {
			t.Errorf("run %s: research reason %q, explored should be %v", run.ID, s.Reason, want)
		}
	}
	if !research {
		t.Error("decision.json has no research stage")
	}

	hasResearch := slices.Contains(run.actions(), "research")
	if hasResearch != want {
		t.Errorf("run %s: executions %v, research should run: %v", run.ID, run.actions(), want)
	}
	if got := slices.Contains(invocations(t, invocationsLog), "research"); got != want {
		t.Errorf("run %s: research invoked %v, want %v", run.ID, got, want)
	}
	return want
}

func TestScenario56_ExploreTurnsOnAStageForTheRunsTheDrawPicks(t *testing.T) {
	seen := map[bool]bool{}
	for i := 1; i <= maxExploreRuns && !(seen[true] && seen[false]); i++ {
		t.Run(fmt.Sprintf("run %d", i), func(t *testing.T) {
			seen[exploreOnce(t)] = true
		})
	}
	if !seen[true] || !seen[false] {
		t.Fatalf("in %d runs: a run explored research %v, a run left it off %v", maxExploreRuns, seen[true], seen[false])
	}
}

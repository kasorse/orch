package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"testing"
)

func hashPolicy() DecisionPolicy {
	return DecisionPolicy{
		ProjectContextThreshold: 0.5,
		ComplexityThreshold:     map[Complexity]float64{ComplexityMedium: 0.6, ComplexityComplex: 0.7},
		StageThreshold:          map[Stage]float64{StageTesting: 0.5, StageReview: 0.4},
		ForceStages:             map[Complexity][]Stage{ComplexityComplex: {StageReview}},
	}
}

func TestPolicyHash(t *testing.T) {
	p := hashPolicy()
	h := PolicyHash(p)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("PolicyHash = %q, want 64 lowercase hex characters", h)
	}
	if again := PolicyHash(hashPolicy()); again != h {
		t.Errorf("the same policy gave %q and %q", h, again)
	}

	p.StageThreshold[StageTesting] = 0.51
	if PolicyHash(p) == h {
		t.Error("changing one stage threshold did not change the hash")
	}
}

func TestPolicyHash_MapOrderDoesNotMatter(t *testing.T) {
	fill := func(stages []Stage, complexities []Complexity) DecisionPolicy {
		p := DecisionPolicy{
			ProjectContextThreshold: 0.5,
			ComplexityThreshold:     map[Complexity]float64{},
			StageThreshold:          map[Stage]float64{},
			ForceStages:             map[Complexity][]Stage{},
		}
		for _, st := range stages {
			p.StageThreshold[st] = 0.1 * float64(len(string(st)))
		}
		for _, c := range complexities {
			p.ComplexityThreshold[c] = 0.5
			p.ForceStages[c] = []Stage{StageReview}
		}
		return p
	}
	stages, complexities := StageOrder(), ComplexityOrder()
	forward := fill(stages, complexities)
	slices.Reverse(stages)
	slices.Reverse(complexities)
	backward := fill(stages, complexities)

	if PolicyHash(forward) != PolicyHash(backward) {
		t.Errorf("map insertion order changed the hash: %s vs %s", PolicyHash(forward), PolicyHash(backward))
	}
}

// The hash covers DecisionPolicy's encoding, so a renamed, retyped or added field changes
// every hash and splits the run history into a new group. This pins the encoding: when it
// fails on purpose, say so in the commit that changes it.
func TestPolicyHash_PinnedEncoding(t *testing.T) {
	const wantJSON = `{"ProjectContextThreshold":0.5,` +
		`"ComplexityThreshold":{"complex":0.7,"medium":0.6},` +
		`"StageThreshold":{"review":0.4,"testing":0.5},` +
		`"ForceStages":{"complex":["review"]}}`
	p := hashPolicy()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != wantJSON {
		t.Fatalf("DecisionPolicy encodes as\n%s\nwant\n%s\nevery recorded policy_hash would change", data, wantJSON)
	}
	sum := sha256.Sum256([]byte(wantJSON))
	if got, want := PolicyHash(p), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("PolicyHash = %s, want the sha256 of the encoding, %s", got, want)
	}
}

// Exploration changes no threshold, so explored runs stay in the group of the thresholds they
// probe.
func TestPolicyHash_IgnoresExplore(t *testing.T) {
	p := hashPolicy()
	want := PolicyHash(p)
	p.Explore = &ExploreSettings{Band: 0.05, Rate: 0.1}
	if got := PolicyHash(p); got != want {
		t.Errorf("setting Explore changed the hash: %s vs %s", got, want)
	}
}

func TestManualTraceHasNoPolicyHash(t *testing.T) {
	d, err := ManualDecision([]Stage{StageAnalysis})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ManualTrace("explain", d, Workflow{}))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["policy_hash"]; ok {
		t.Errorf("a manual trace has a policy_hash: %s", data)
	}
}

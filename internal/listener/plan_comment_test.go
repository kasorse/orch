package listener

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

// golden compares got with testdata/name, or rewrites it with -update.
func golden(t *testing.T, name string, body []string) {
	t.Helper()
	got := strings.Join(body, "\n") + "\n"
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file (run with -update to rewrite it):\n--- got\n%s--- want\n%s", name, got, want)
	}
}

func TestChildLabelsAreTheQueueServiceProjectThenTheConfiguredOnes(t *testing.T) {
	got := ChildLabels("ai-run", "service:", "sku-master", "gitlab:", "g/pim/sku-master", []string{"team-a", "ai-run", "team-a", "pim"})
	want := []string{"ai-run", "service:sku-master", "gitlab:g/pim/sku-master", "team-a", "pim"}
	if !slices.Equal(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
	if got := ChildLabels("ai-run", "", "svc", "", "g/svc", nil); !slices.Equal(got, []string{"ai-run"}) {
		t.Errorf("labels without prefixes = %v", got)
	}
}

func TestPlanCreatedGolden(t *testing.T) {
	golden(t, "plan_created.golden", PlanCreated(testEscape, PlanComment{
		Epic: "PMC-1",
		Services: []PlanService{
			{Name: "sku-master", Why: "the dictionary lives here"},
			{Name: "sku-master-fe", Why: "the editor shows it"},
		},
		Children: []PlanChild{
			{Key: "PMC-2", Service: "sku-master", Title: "The dictionary"},
			{Key: "PMC-3", Service: "sku-master-fe", Title: "The editor"},
			{Key: "PMC-4", Service: "sku-master", Title: "The country attributes", BlockedBy: "PMC-2"},
		},
		References: []string{"g/pim/product-master"},
		Warning:    "The children are in Backlog, which the listener does not poll (To Do): move them there.",
		Cost:       "$0.42",
	}))
}

func TestPlanCreatedWithOneChildAndNothingElseGolden(t *testing.T) {
	golden(t, "plan_created_one.golden", PlanCreated(testEscape, PlanComment{
		Epic:     "PMC-1",
		Children: []PlanChild{{Key: "PMC-2", Service: "sku-master", Title: "The only task"}},
	}))
}

func TestPlanFailedGolden(t *testing.T) {
	golden(t, "plan_failed.golden", PlanFailed(testEscape,
		"creating the issue of PM03 (sku-master) may have reached Jira without an answer: look under PMC-1 and finish it by hand.",
		[]PlanChild{
			{Key: "PMC-2", Service: "sku-master", Title: "The dictionary"},
			{Key: "PMC-3", Service: "sku-master-fe", Title: "The editor"},
		}))
}

func TestPlanFailedWithoutChildrenIsTheReasonOnly(t *testing.T) {
	got := PlanFailed(testEscape, "a description looks like a secret", nil)
	if len(got) != 1 || got[0] != "The plan did not finish: a description looks like a secret" {
		t.Errorf("comment = %q", got)
	}
}

func TestPlanCommentsEscapeWhatAModelWrote(t *testing.T) {
	text := strings.Join(PlanCreated(testEscape, PlanComment{
		Epic:     "PMC-1",
		Services: []PlanService{{Name: "svc", Why: "see {code} and [x]"}},
		Children: []PlanChild{{Key: "PMC-2", Service: "svc", Title: "Title with {macro}"}},
	}), "\n")
	for _, want := range []string{`see \{code\} and \[x\]`, `Title with \{macro\}`} {
		if !strings.Contains(text, want) {
			t.Errorf("comment lacks %q:\n%s", want, text)
		}
	}
}

func TestExistingChildrenNamesTheKeysAndTheRestartLabel(t *testing.T) {
	got := ExistingChildren("PMC-1", []string{"PMC-2", "PMC-3"}, "ai-restart")
	if want := "PMC-1 has child tickets created by the listener already: PMC-2, PMC-3. Add the label ai-restart to plan it again."; got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
	var many []string
	for i := 2; i < 40; i++ {
		many = append(many, "PMC-"+string(rune('0'+i/10))+string(rune('0'+i%10)))
	}
	if got := ExistingChildren("PMC-1", many, ""); !strings.Contains(got, "and 18 more.") || strings.Contains(got, "Add the label") {
		t.Errorf("reason = %q", got)
	}
}

func TestEntryKeepsTheWritesOfAPlanAndNamesWhatIsInDoubt(t *testing.T) {
	e := Entry{
		Phase:    PhaseClaimed,
		Kind:     KindPlan,
		Children: []Child{{Task: "PM01", Key: "PMC-2"}, {Task: "PM02", Intent: true}},
		Links:    []ChildLink{{Blocked: "PM03", Blocker: "PM01", State: WriteIntent}, {Blocked: "PM04", Blocker: "PM02", State: WriteDone}},
	}
	s := NewState()
	s.Tickets["PMC-1"] = e
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseState(data)
	if err != nil {
		t.Fatalf("the state does not load back: %v\n%s", err, data)
	}
	got := back.Tickets["PMC-1"]
	tasks, links := got.InDoubt()
	if !slices.Equal(tasks, []string{"PM02"}) || !slices.Equal(links, []Link{{Blocked: "PM03", Blocker: "PM01"}}) {
		t.Errorf("in doubt = %v, %v", tasks, links)
	}
	if got.ChildKey("PM01") != "PMC-2" || got.ChildKey("PM02") != "" || !slices.Equal(got.CreatedKeys(), []string{"PMC-2"}) {
		t.Errorf("keys = %q %q %v", got.ChildKey("PM01"), got.ChildKey("PM02"), got.CreatedKeys())
	}
}

func TestStateRefusesChildrenThatAreNotAPlansOrNotShapedRight(t *testing.T) {
	for name, e := range map[string]Entry{
		"children of a ticket":  {Phase: PhaseClaimed, Children: []Child{{Task: "PM01", Intent: true}}},
		"a bad task":            {Phase: PhaseClaimed, Kind: KindPlan, Children: []Child{{Task: "pm1", Intent: true}}},
		"a bad key":             {Phase: PhaseClaimed, Kind: KindPlan, Children: []Child{{Task: "PM01", Key: "nope"}}},
		"neither key nor doubt": {Phase: PhaseClaimed, Kind: KindPlan, Children: []Child{{Task: "PM01"}}},
		"a key and an intent":   {Phase: PhaseClaimed, Kind: KindPlan, Children: []Child{{Task: "PM01", Key: "PMC-2", Intent: true}}},
		"a link to itself":      {Phase: PhaseClaimed, Kind: KindPlan, Links: []ChildLink{{Blocked: "PM01", Blocker: "PM01", State: WriteDone}}},
		"a link without state":  {Phase: PhaseClaimed, Kind: KindPlan, Links: []ChildLink{{Blocked: "PM02", Blocker: "PM01"}}},
	} {
		s := NewState()
		s.Tickets["PMC-1"] = e
		if err := s.Validate(); err == nil {
			t.Errorf("%s: the state is accepted", name)
		}
	}
}

func TestPlannedIsACommentWithoutATransition(t *testing.T) {
	d := Planned()
	if d.Comment != CommentPlan || d.Transition != ResultPlanned || !d.Counted || d.Backoff || d.Stop {
		t.Errorf("decision = %+v", d)
	}
	if !ResultPlanned.Valid() {
		t.Error("planned is not a valid result")
	}
}

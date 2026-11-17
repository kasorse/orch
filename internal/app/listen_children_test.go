package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/listener"
)

// The children of an Epic's plan (specs/listener-story/SPEC.md §8, §9), against the fake Jira of
// listen_plan_test.go, which numbers the issues it creates PMC-2, PMC-3, ...

// writes are the POSTs to Jira that create something, in the order they came.
func (j *epicJira) writes() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []string
	for _, p := range j.posts {
		if strings.HasPrefix(p, "POST /rest/api/2/issue") {
			out = append(out, strings.TrimPrefix(p, "POST /rest/api/2/"))
		}
	}
	return out
}

// afterRequirements are the Epic's comments after the first, which must be the requirements comment
// (specs/listener-requirements/SPEC.md §3): the comments of the plan's outcome.
func (e *epicEnv) afterRequirements() []string {
	e.t.Helper()
	c := e.jira.commentBodies()
	if len(c) == 0 || !strings.HasPrefix(c[0], "[orchestrator]\nRequirements of PMC-1, from the Epic") || strings.Contains(c[0], "Open questions") {
		e.t.Fatalf("the first comment is not the requirements without questions: %q", c)
	}
	return c[1:]
}

// planWithSpecify is a plan whose specify call answers out.
func planWithSpecify(t *testing.T, out string, labels ...string) *epicEnv {
	t.Helper()
	env := newEpicEnv(t, "To Do", labels...)
	env.prov.outputs["specify"] = out
	env.plan()
	return env
}

func TestChildren_ThreeChildrenWithTheirLabelsAndParentAndTheLinkTheRightWayRound(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.plan()

	// Each child once, in the story's order, then each link once.
	if got, want := env.jira.writes(), []string{"issue", "issue", "issue", "issueLink"}; !slices.Equal(got, want) {
		t.Fatalf("writes = %v, want %v\n%s", got, want, env.out.String())
	}
	issues := env.jira.createdIssues()
	wantLabels := [][]string{
		{"ai-run", "service:sku-master", "gitlab:g/pim/sku-master", "pim"},
		{"ai-run", "service:sku-master-fe", "gitlab:g/pim/sku-master-fe", "pim"},
		{"ai-run", "service:sku-master", "gitlab:g/pim/sku-master", "pim"},
	}
	wantTitles := []string{"The dictionary", "The editor", "The country attributes"}
	for i, is := range issues {
		f := is.Fields
		if f.Project.Key != "PMC" || f.IssueType.Name != "Task" || f.Parent.Key != "PMC-1" || f.Summary != wantTitles[i] || !slices.Equal(f.Labels, wantLabels[i]) {
			t.Errorf("child %d = %+v, want %q, labels %v under PMC-1", i, f, wantTitles[i], wantLabels[i])
		}
	}
	if c := issues[0].Fields.Components; len(c) != 1 || c[0].Name != "SKU" {
		t.Errorf("components of the first child = %v, want SKU", c)
	}
	if len(issues[1].Fields.Components) != 0 {
		t.Errorf("components of a service without one = %v", issues[1].Fields.Components)
	}
	for _, want := range []string{
		"h2. Goal",
		`Story: PMC-1 — Digital categories migration`,
		`Service: sku\-master (g/pim/sku\-master)`,
		"References:\n* [https://gitlab.example/g/pim/product-master]",
		"h3. From the plan", "h4. Approach", "h4. Contracts",
	} {
		if !strings.Contains(issues[0].Fields.Description, want) {
			t.Errorf("the first child's description lacks %q:\n%s", want, issues[0].Fields.Description)
		}
	}
	// Every child holds all the story's requirements, before the plan (listener-requirements §5).
	reqs := "h3. Requirements of the story\n* sku\\-master keeps the categories of product\\-master, by country.\n" +
		"* sku\\-master\\-fe shows the category of each SKU.\n\nh3. From the plan"
	for i, is := range issues {
		if !strings.Contains(is.Fields.Description, reqs) {
			t.Errorf("child %d's description lacks the requirements:\n%s", i, is.Fields.Description)
		}
	}
	if strings.Contains(issues[0].Fields.Description, "Needs:") {
		t.Errorf("a task that needs nothing of another service has a Needs line:\n%s", issues[0].Fields.Description)
	}

	// The second task of sku-master waits for the first: the later one is the inward side.
	links := env.jira.linkBodies()
	if len(links) != 1 {
		t.Fatalf("links = %q, want one", links)
	}
	var l struct{ Type, InwardIssue, OutwardIssue nameKey }
	if err := json.Unmarshal([]byte(links[0]), &l); err != nil {
		t.Fatal(err)
	}
	if l.Type.Name != "Blocks" || l.InwardIssue.Key != "PMC-4" || l.OutwardIssue.Key != "PMC-2" {
		t.Errorf("link = %+v, want PMC-4 (inward, blocked) by PMC-2 (outward) of type Blocks", l)
	}

	// The Epic's entry has every write done, the Epic stays at start and says what it made.
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultPlanned || e.Phase != listener.PhaseFinished || e.Attempts != 1 || e.Retries != 0 {
		t.Errorf("entry = %+v", e)
	}
	if want := []listener.Child{{Task: "PM01", Key: "PMC-2"}, {Task: "PM02", Key: "PMC-3"}, {Task: "PM03", Key: "PMC-4"}}; !slices.Equal(e.Children, want) {
		t.Errorf("children = %+v, want %+v", e.Children, want)
	}
	if want := []listener.ChildLink{{Blocked: "PM03", Blocker: "PM01", State: listener.WriteDone}}; !slices.Equal(e.Links, want) {
		t.Errorf("links = %+v, want %+v", e.Links, want)
	}
	env.stateIsValid()
	if got := env.jira.statusNow(); got != "In Progress" {
		t.Errorf("the Epic is in %q, want In Progress", got)
	}
	comments := env.afterRequirements()
	if len(comments) != 1 {
		t.Fatalf("comments = %q", comments)
	}
	for _, want := range []string{
		"Planned: 3 child tickets created under PMC-1.",
		`* sku\-master: the dictionary lives here`,
		`|PMC-2|sku\-master|The dictionary|—|`,
		`|PMC-4|sku\-master|The country attributes|PMC-2|`,
		`* g/pim/product\-master`,
	} {
		if !strings.Contains(comments[0], want) {
			t.Errorf("the Epic's comment lacks %q:\n%s", want, comments[0])
		}
	}
	if strings.Contains(comments[0], "does not poll") {
		t.Errorf("a warning about the status although the children are in the queue's status:\n%s", comments[0])
	}
	if rec := env.record(); rec.Outcome != "planned" || !slices.Equal(rec.Children, []string{"PMC-2", "PMC-3", "PMC-4"}) || rec.Tasks[2].Key != "PMC-4" || rec.ChildStatus != "To Do" {
		t.Errorf("plan.json = %+v", rec)
	}

	// The next poll takes a child as any ticket: the account that made it is a reporter, the labels
	// are the queue's. The second of a service waits for the first, as any blocked ticket does.
	for i, is := range issues {
		h := domain.TicketHead{Key: childKey(i), Type: "Task", Status: "To Do", Reporter: "bot-1", Labels: is.Fields.Labels}
		if !env.r.eligible(h) {
			t.Errorf("child %s is not picked by the poll", h.Key)
		}
		if got, want := env.r.serviceOf(h), strings.TrimPrefix(wantLabels[i][1], "service:"); got != want {
			t.Errorf("service of %s = %q, want %q", h.Key, got, want)
		}
		h.BlockedBy = nil
		if i == 2 {
			h.BlockedBy = []domain.TicketRef{{Key: "PMC-2", Status: "To Do", LinkType: "Blocks"}}
		}
		if got := env.r.blocked(h); got != (i == 2) {
			t.Errorf("%s waits = %v", h.Key, got)
		}
	}
}

// With queue_children: false the children lack the queue's label, the poll does not pick them, and the
// Epic's comment says how to queue one.
func TestChildren_NotQueuedChildrenLackTheQueueLabel(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.r.lc.Story.QueueChildren = false
	env.plan()

	issues := env.jira.createdIssues()
	if len(issues) != 3 {
		t.Fatalf("created %d children, want 3\n%s", len(issues), env.out.String())
	}
	for i, is := range issues {
		if slices.Contains(is.Fields.Labels, "ai-run") || !slices.Contains(is.Fields.Labels, "pim") {
			t.Errorf("child %d labels = %v, want no ai-run and the create labels", i, is.Fields.Labels)
		}
		h := domain.TicketHead{Key: childKey(i), Type: "Task", Status: "To Do", Reporter: "bot-1", Labels: is.Fields.Labels}
		if env.r.eligible(h) {
			t.Errorf("child %s is picked by the poll without the queue's label", h.Key)
		}
	}
	comments := env.afterRequirements()
	if len(comments) != 1 || !strings.Contains(comments[0], "not labelled ai") {
		t.Errorf("the Epic's comment does not say how to queue a child:\n%q", comments)
	}
}

func TestChildren_ATaskThatNeedsAnotherServiceNamesItAndIsNotLinked(t *testing.T) {
	out := strings.Replace(epicSpecifyAnswer, `"title":"The editor","size":"M","depends_on":[]`, `"title":"The editor","size":"M","depends_on":["PM01"]`, 1)
	if out == epicSpecifyAnswer {
		t.Fatal("the specify answer was not changed")
	}
	env := planWithSpecify(t, out)

	if got := env.jira.linkBodies(); len(got) != 1 {
		t.Errorf("links = %q, want the one of sku-master's chain only", got)
	}
	d := env.jira.createdIssues()[1].Fields.Description
	if !strings.Contains(d, "Needs:\n* PMC-2 (sku\\-master), not linked") {
		t.Errorf("the editor's description does not name what it needs:\n%s", d)
	}
}

func TestChildren_ACreationThatTimesOutAfterSendingIsNotRepeated(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.jira.timeout = 500 * time.Millisecond
	env.r.jira = env.jira.client()
	env.jira.createMode = func(n int) string {
		if n == 2 {
			return "hang"
		}
		return ""
	}
	env.plan()

	// The second creation was sent once and never answered: no second POST, no third child, no link.
	if got, want := env.jira.writes(), []string{"issue", "issue"}; !slices.Equal(got, want) {
		t.Fatalf("writes = %v, want %v\n%s", got, want, env.out.String())
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || e.Phase != listener.PhaseFinished {
		t.Errorf("entry = %+v", e)
	}
	if want := []listener.Child{{Task: "PM01", Key: "PMC-2"}, {Task: "PM02", Intent: true}}; !slices.Equal(e.Children, want) {
		t.Errorf("children = %+v, want %+v: the one in doubt keeps its intent", e.Children, want)
	}
	env.stateIsValid()
	if got := env.jira.statusNow(); got != "Failed" {
		t.Errorf("status = %q, want Failed", got)
	}
	comments := env.afterRequirements()
	if len(comments) != 1 {
		t.Fatalf("comments = %q", comments)
	}
	for _, want := range []string{"task PM02", "may have reached Jira", "Look under PMC\\-1", "Created before it stopped:", "|PMC-2|sku\\-master|The dictionary|"} {
		if !strings.Contains(comments[0], want) {
			t.Errorf("the comment lacks %q:\n%s", want, comments[0])
		}
	}
}

func TestChildren_ARefusedCreationFailsWithoutADoubt(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.jira.createMode = func(n int) string {
		if n == 2 {
			return "403"
		}
		return ""
	}
	env.plan()

	if got := env.jira.writes(); len(got) != 2 {
		t.Fatalf("writes = %v, want the first creation and the refused one", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 {
		t.Errorf("entry = %+v", e)
	}
	if want := []listener.Child{{Task: "PM01", Key: "PMC-2"}}; !slices.Equal(e.Children, want) {
		t.Errorf("children = %+v, want %+v: a refused creation leaves no intent", e.Children, want)
	}
	if c := env.afterRequirements(); len(c) != 1 || !strings.Contains(c[0], "was refused by Jira") || !strings.Contains(c[0], "|PMC-2|") {
		t.Errorf("comments = %q", c)
	}
}

func TestChildren_ARefusedLinkFailsTheEpicAndListsTheChildren(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.jira.linkMode = func(int) string { return "400" }
	env.plan()

	if got, want := env.jira.writes(), []string{"issue", "issue", "issue", "issueLink"}; !slices.Equal(got, want) {
		t.Fatalf("writes = %v, want %v: the refused link is sent once", got, want)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || len(e.Links) != 0 || len(e.Children) != 3 {
		t.Errorf("entry = %+v", e)
	}
	if got := env.jira.statusNow(); got != "Failed" {
		t.Errorf("status = %q, want Failed", got)
	}
	c := env.afterRequirements()
	if len(c) != 1 {
		t.Fatalf("comments = %q", c)
	}
	for _, want := range []string{"The link of PMC-4 (task PM03)", "was refused by Jira", "|PMC-2|", "|PMC-3|", "|PMC-4|"} {
		if !strings.Contains(strings.ReplaceAll(c[0], `\-`, "-"), want) {
			t.Errorf("the comment lacks %q:\n%s", want, c[0])
		}
	}
}

func TestChildren_AnEpicWithChildrenAlreadyIsBlocked(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.jira.existing = []string{"PMC-9"}
	env.plan()

	if got := env.prov.actions(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
	if got := env.cat.asked(); len(got) != 0 {
		t.Errorf("the catalog was read for %v before the children check", got)
	}
	if got := env.jira.writes(); len(got) != 0 {
		t.Errorf("writes = %v, want none", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultBlocked || e.Attempts != 0 || e.Phase != listener.PhaseFinished {
		t.Errorf("entry = %+v, want blocked, no attempt", e)
	}
	if got := env.jira.statusNow(); got != "Blocked" {
		t.Errorf("status = %q, want Blocked", got)
	}
	c := env.jira.commentBodies()
	if len(c) != 1 || !strings.Contains(c[0], `PMC\-9`) || !strings.Contains(c[0], `ai\-restart`) {
		t.Errorf("comments = %q, want the child's key and the label that plans it again", c)
	}
	env.jira.mu.Lock()
	jqls := slices.Clone(env.jira.jqls)
	env.jira.mu.Unlock()
	if len(jqls) != 1 || !strings.HasPrefix(jqls[0], `parent = PMC-1 AND reporter = "bot-1"`) {
		t.Errorf("searches = %q, want the children of PMC-1 reported by the listener's account", jqls)
	}
}

func TestChildren_TheRestartLabelPlansAgainAndIsRemoved(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ai-restart", "gitlab:g/pim/sku-master", "gitlab:g/pim/sku-master-fe")
	env.jira.existing = []string{"PMC-9"}
	env.plan()

	env.jira.mu.Lock()
	searched := len(env.jira.jqls)
	env.jira.mu.Unlock()
	if searched != 0 {
		t.Errorf("the children were searched %d times with the restart label", searched)
	}
	if slices.Contains(env.jira.labelsNow(), "ai-restart") {
		t.Errorf("labels = %v: the restart label is not removed", env.jira.labelsNow())
	}
	if got := env.jira.writes(); len(got) != 4 {
		t.Errorf("writes = %v, want three new children and a link", got)
	}
	if e := env.r.entryOf("PMC-1"); e.Outcome != listener.ResultPlanned || len(e.Children) != 3 {
		t.Errorf("entry = %+v", e)
	}
}

func TestChildren_ADescriptionWithASecretCreatesNothing(t *testing.T) {
	secret := "-----BEGIN " + "RSA PRIVATE KEY-----" // split: the repository's own secret scan would flag the fixture
	out := strings.Replace(epicSpecifyAnswer, `"title":"The editor","size":"M","depends_on":[],"goal":"g"`,
		`"title":"The editor","size":"M","depends_on":[],"goal":"use `+secret+`"`, 1)
	if out == epicSpecifyAnswer {
		t.Fatal("the specify answer was not changed")
	}
	env := planWithSpecify(t, out)

	if got := env.jira.writes(); len(got) != 0 {
		t.Fatalf("writes = %v, want none: a secret in the third child stops the plan before the first", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || len(e.Children) != 0 {
		t.Errorf("entry = %+v", e)
	}
	c := env.afterRequirements()
	if len(c) != 1 || !strings.Contains(c[0], "PM02") || strings.Contains(c[0], "PRIVATE KEY") {
		t.Errorf("comments = %q, want the task named and not the text", c)
	}
}

// A plan cut off by a stop with a creation in doubt is not made again: the Epic fails, naming it.
func TestChildren_ACutOffPlanWithAnIntentInDoubtFailsNamingIt(t *testing.T) {
	env := newEpicEnv(t, "In Progress", "gitlab:g/pim/sku-master", "gitlab:g/pim/sku-master-fe")
	claimed(t, env.r, "PMC-1", listener.Entry{
		Status:   "In Progress",
		Phase:    listener.PhaseClaimed,
		Kind:     listener.KindPlan,
		Children: []listener.Child{{Task: "PM01", Key: "PMC-2"}, {Task: "PM02", Intent: true}},
	})

	if err := env.r.recover(); err != nil {
		t.Fatal(err)
	}
	env.r.wg.Wait()

	if got := env.prov.actions(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
	if got := env.jira.writes(); len(got) != 0 {
		t.Errorf("writes = %v, want none", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Phase != listener.PhaseFinished || e.Attempts != 1 {
		t.Errorf("entry = %+v", e)
	}
	c := env.jira.commentBodies()
	if len(c) != 1 || !strings.Contains(c[0], "task PM02") || !strings.Contains(c[0], `PMC\-2`) {
		t.Errorf("comments = %q, want the task in doubt and what was created", c)
	}
}

// A plan cut off between creations is made again: the children it made are found, and it is blocked.
func TestChildren_ACutOffPlanThatMadeChildrenFindsThemOnRestart(t *testing.T) {
	env := newEpicEnv(t, "In Progress", "gitlab:g/pim/sku-master", "gitlab:g/pim/sku-master-fe")
	claimed(t, env.r, "PMC-1", listener.Entry{
		Status:   "In Progress",
		Phase:    listener.PhaseClaimed,
		Kind:     listener.KindPlan,
		Children: []listener.Child{{Task: "PM01", Key: "PMC-2"}},
	})

	if err := env.r.recover(); err != nil {
		t.Fatal(err)
	}
	env.r.wg.Wait()

	if got := env.jira.writes(); len(got) != 0 {
		t.Errorf("writes = %v, want none: a duplicate would be made", got)
	}
	if e := env.r.entryOf("PMC-1"); e.Outcome != listener.ResultBlocked || e.Attempts != 0 {
		t.Errorf("entry = %+v, want blocked", e)
	}
}

func TestChildren_TheListenersAccountMustBeAReporterWithListenerStory(t *testing.T) {
	lc := &config.ListenerConfig{Query: config.ListenerQuery{Reporters: []string{"acc-1", "acc-2"}}}
	if err := checkStoryReporter(lc, "acc-3"); err != nil {
		t.Errorf("without listener.story the account is not checked: %v", err)
	}
	lc.Story = &config.ListenerStory{}
	if err := checkStoryReporter(lc, "acc-2"); err != nil {
		t.Errorf("a reporter is refused: %v", err)
	}
	err := checkStoryReporter(lc, "acc-3")
	if err == nil || !strings.Contains(err.Error(), "acc-3") || !strings.Contains(err.Error(), "listener.query.reporters") {
		t.Errorf("an account that is not a reporter = %v, want a configuration error naming it", err)
	}
}

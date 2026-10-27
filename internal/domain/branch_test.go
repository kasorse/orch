package domain

import (
	"strings"
	"testing"
)

func TestRenderBranchTemplate(t *testing.T) {
	vars := BranchVars{ID: "20261023-101500-ab12cd", Key: "PAY-12", Slug: "use-in-add", Date: "20261023"}
	noKey := vars
	noKey.Key = ""
	noSlug := vars
	noSlug.Slug = ""
	tests := []struct {
		name string
		tmpl string
		vars BranchVars
		want string
	}{
		{"with a key", "feature/{key}-{slug}", vars, "feature/PAY-12-use-in-add"},
		{"without a key the separator after it goes", "feature/{key}-{slug}", noKey, "feature/use-in-add"},
		{"an empty placeholder in front takes its slash", "{key}/{slug}", noKey, "use-in-add"},
		{"an empty placeholder at the end takes the separator before it", "fix/{slug}-{key}", noKey, "fix/use-in-add"},
		{"an empty slug at the end", "feature/{key}-{slug}", noSlug, "feature/PAY-12"},
		{"underscore separator", "{key}_{slug}", noKey, "use-in-add"},
		{"id and date", "{date}/{id}", vars, "20261023/20261023-101500-ab12cd"},
		{"a repeated placeholder", "{slug}-{slug}", vars, "use-in-add-use-in-add"},
		{"no placeholder", "work/queue", vars, "work/queue"},
		{"an unknown placeholder is left as written", "a/{type}", vars, "a/{type}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderBranchTemplate(tc.tmpl, tc.vars); got != tc.want {
				t.Errorf("RenderBranchTemplate(%q) = %q, want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

func TestCheckBranchTemplate(t *testing.T) {
	for _, ok := range []string{"feature/{key}-{slug}", "{id}", "{date}/{slug}", "plain", ""} {
		if err := CheckBranchTemplate(ok); err != nil {
			t.Errorf("CheckBranchTemplate(%q) = %v, want nil", ok, err)
		}
	}
	for _, tc := range []struct{ tmpl, want string }{
		{"feature/{type}-{slug}", "{type}"},
		{"{KEY}", "{KEY}"},
		{"{}", "{}"},
		{"a/{key", "brace"},
		{"a/key}", "brace"},
		{"{{id}}", "brace"},
	} {
		if err := CheckBranchTemplate(tc.tmpl); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CheckBranchTemplate(%q) = %v, want an error naming %q", tc.tmpl, err, tc.want)
		}
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"words", "Use + in Add", "use-in-add"},
		{"punctuation runs collapse", "  Fix: the  (nil) map!! ", "fix-the-nil-map"},
		{"digits stay", "Bump go to 1.22", "bump-go-to-1-22"},
		{"Cyrillic", "Исправить панику в Store.Put", "ispravit-paniku-v-store-put"},
		{"the table", "Щётка, чёрный ёж: подъезд", "shchetka-chernyy-ezh-podezd"},
		{"the rest of the table", "ёлка, юла, яма, шаг, хлеб, цирк", "elka-yula-yama-shag-khleb-tsirk"},
		{"Ukrainian", "їжак і ґанок, Європа", "yizhak-i-ganok-yevropa"},
		{"only symbols", "!!! ???", ""},
		{"other scripts drop out", "日本語 title", "title"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Slug(tc.text); got != tc.want {
				t.Errorf("Slug(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestSlug_LongTextIsCutAtADash(t *testing.T) {
	text := "refactor the payment gateway adapter to use the new client library everywhere"
	got := Slug(text)
	if got != "refactor-the-payment-gateway-adapter-to" {
		t.Errorf("Slug() = %q (%d bytes)", got, len(got))
	}
	if len(got) > 40 {
		t.Errorf("slug is %d bytes, at most 40", len(got))
	}

	// A dash exactly at the limit keeps every whole word before it; one before it cuts there.
	exact := strings.Repeat("a", 40) + " tail"
	if got := Slug(exact); got != strings.Repeat("a", 40) {
		t.Errorf("Slug(%q) = %q", exact, got)
	}
	before := strings.Repeat("a", 39) + " tail"
	if got := Slug(before); got != strings.Repeat("a", 39) {
		t.Errorf("Slug(%q) = %q", before, got)
	}
	// One word longer than the limit is cut at the limit.
	if got := Slug(strings.Repeat("b", 60)); got != strings.Repeat("b", 40) {
		t.Errorf("a long word = %q", got)
	}
	if got := Slug(strings.Repeat("я", 30)); len(got) > 40 || strings.HasSuffix(got, "-") {
		t.Errorf("a long Cyrillic word = %q", got)
	}
}

func TestCheckBranchName(t *testing.T) {
	long := strings.Repeat("a", 100)
	tests := []struct {
		name string
		want string // "" = valid, else a part of the reason
	}{
		{"orchestrator/20261023-101500-ab12cd", ""},
		{"feature/PAY-12-use-in-add", ""},
		{"fix_1.2/x", ""},
		{"a", ""},
		{long, ""},
		{long + "a", "at most 100"},
		{"", "empty"},
		{"-x", "starts with -"},
		{"/x", "starts with /"},
		{"x/", "ends with /"},
		{"x.", "ends with ."},
		{"a..b", ".."},
		{"a//b", "//"},
		{"x.lock", ".lock"},
		{"x.lock/y", ".lock"},
		{"a/.hidden", "starts with ."},
		{"has space", "character"},
		{"tab\tx", "character"},
		{"a~b", "character"},
		{"a^b", "character"},
		{"a:b", "character"},
		{"a?b", "character"},
		{"a*b", "character"},
		{"a[b", "character"},
		{"a\\b", "character"},
		{"a@{b", "character"},
		{"ветка", "character"},
		{"HEAD", "HEAD"},
		{"story/refunds/RF02", "story/"},
		{"story/x", "story/"},
		{"stories/x", ""},
		{"mystory/x", ""},
	}
	for _, tc := range tests {
		err := CheckBranchName(tc.name)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("CheckBranchName(%q) = %v, want nil", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("CheckBranchName(%q) = %v, want an error naming %q", tc.name, err, tc.want)
		}
		if got := ValidBranchName(tc.name); got != (tc.want == "") {
			t.Errorf("ValidBranchName(%q) = %v", tc.name, got)
		}
	}
}

func TestRunWorktreeBranch(t *testing.T) {
	run := &Run{ID: "20261023-101500-ab12cd"}
	if got := RunWorktreeBranch(run); got != "orchestrator/20261023-101500-ab12cd" {
		t.Errorf("before a worktree: %q", got)
	}
	run.Worktree = &RunWorktree{Branch: "feature/PAY-12-x"}
	if got := RunWorktreeBranch(run); got != "feature/PAY-12-x" {
		t.Errorf("a recorded name is read, not derived: %q", got)
	}
	run.Task.Source = &TaskSource{Kind: TaskSourceStory, Branch: "story/refunds/RF02"}
	if got := RunWorktreeBranch(run); got != "story/refunds/RF02" {
		t.Errorf("a story task keeps the branch its input gave: %q", got)
	}
}

func TestBranchNaming_Validate(t *testing.T) {
	ok := []BranchNaming{
		{Source: BranchSourceDefault},
		{Source: BranchSourceTemplate, Rejected: "the instructions set no rule", Proposed: "feat/x"},
		{Source: BranchSourceInstructions, Proposed: "feat/x", Usage: &Usage{InputTokens: 10, CostUSD: 0.001}, Call: &CallReport{PromptBytes: 900}},
	}
	for _, b := range ok {
		if err := b.Validate(); err != nil {
			t.Errorf("%+v: %v", b, err)
		}
	}
	bad := []struct {
		name string
		b    BranchNaming
	}{
		{"unknown source", BranchNaming{Source: "model"}},
		{"no source", BranchNaming{}},
		{"instructions without a name", BranchNaming{Source: BranchSourceInstructions}},
		{"proposed too long", BranchNaming{Source: BranchSourceTemplate, Proposed: strings.Repeat("a", 256)}},
		{"proposed not printable", BranchNaming{Source: BranchSourceTemplate, Proposed: "a\x00b"}},
		{"rejected on two lines", BranchNaming{Source: BranchSourceDefault, Rejected: "a\nb"}},
		{"rejected too long", BranchNaming{Source: BranchSourceDefault, Rejected: strings.Repeat("a", 501)}},
		{"negative usage", BranchNaming{Source: BranchSourceDefault, Usage: &Usage{InputTokens: -1}}},
		{"call with a negative counter", BranchNaming{Source: BranchSourceDefault, Call: &CallReport{PromptBytes: -1}}},
	}
	for _, tc := range bad {
		if err := tc.b.Validate(); err == nil {
			t.Errorf("%s: %+v accepted", tc.name, tc.b)
		}
	}
}

// The branch-name call's cost is spent before the workflow runs: it counts in the outcome's usage
// and in the decision overhead.
func TestComputeOutcome_CountsTheBranchNameCall(t *testing.T) {
	run := Run{ID: "r", Status: RunCompleted, BranchNaming: &BranchNaming{
		Source: BranchSourceInstructions, Proposed: "feat/x", Usage: &Usage{InputTokens: 100, OutputTokens: 5, CostUSD: 0.01},
	}}
	out := ComputeOutcome(run, nil)
	if out.Usage.Calls != 1 || out.Usage.InputTokens != 100 || out.Usage.CostUSD != 0.01 {
		t.Errorf("usage = %+v, want the call counted", out.Usage)
	}
	if out.DecisionOverhead.Calls != 1 || out.DecisionOverhead.CostUSD != 0.01 {
		t.Errorf("decision overhead = %+v, want the call counted", out.DecisionOverhead)
	}

	run.BranchNaming = &BranchNaming{Source: BranchSourceTemplate}
	if out := ComputeOutcome(run, nil); out.Usage.Calls != 0 || out.DecisionOverhead.Calls != 0 {
		t.Errorf("a naming without a call changed the outcome: %+v", out)
	}
}

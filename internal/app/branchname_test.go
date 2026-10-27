package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// The branch of a worktree run is named when the worktree is created (specs/delivery/SPEC.md §4.3):
// the repository's instructions, else worktree.branch.template, else orchestrator/<run-id>.

// branchClient is the provider behind the branch-name call: it answers output, or fails with err,
// and keeps the last request.
type branchClient struct {
	output string
	err    error
	usage  *domain.Usage
	calls  int
	req    provider.Request
}

func (c *branchClient) Supports(domain.Authority) bool { return true }

func (c *branchClient) Execute(_ context.Context, req provider.Request) (provider.Result, error) {
	c.calls++
	c.req = req
	return provider.Result{Output: c.output, Usage: c.usage}, c.err
}

const branchRule = "Branches are named feature/<slug>.\n"

// namingSession is a fresh worktree run over a repository whose second commit holds files, with
// worktree.branch.from_instructions routed to client.
func namingSession(t *testing.T, files map[string]string, client *branchClient) (*session, string, *bytes.Buffer) {
	t.Helper()
	s, dir, out := worktreeSession(t, true, implementTest)
	if len(files) > 0 {
		for name, text := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "instructions")
	}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	s.dispatcher.Registry = registry
	s.cfg.Worktree.Branch = config.BranchConfig{FromInstructions: &config.BranchInstructionsConfig{
		ProviderModel: config.ProviderModel{Provider: "claude", Model: "haiku"}, Timeout: time.Minute,
	}}
	return s, dir, out
}

func TestOpenWorktree_BranchFromTheInstructions(t *testing.T) {
	client := &branchClient{
		output: "Sure:\n{\"branch\": \"feature/fix-it\"}",
		usage:  &domain.Usage{InputTokens: 300, OutputTokens: 9, CostUSD: 0.002},
	}
	s, dir, _ := namingSession(t, map[string]string{"CLAUDE.md": branchRule}, client)
	s.cfg.Worktree.Branch.Template = "work/{slug}"
	// An uncommitted edit is not what the run starts from: the file is read from base_ref.
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("LOCAL-EDIT: name them after the moon\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := s.run.Worktree.Branch; got != "feature/fix-it" {
		t.Fatalf("branch = %q, want the instructions' name", got)
	}
	if client.calls != 1 || client.req.Authority != domain.AuthorityNone || client.req.Model != "haiku" {
		t.Errorf("calls = %d, request = %+v, want one tool-less call on haiku", client.calls, client.req)
	}
	prompt := client.req.Prompt
	if !strings.HasPrefix(prompt, "ACTION: branch-name\n") || !strings.Contains(prompt, "Branches are named feature/<slug>.") ||
		strings.Contains(prompt, "LOCAL-EDIT") || !strings.Contains(prompt, "\nfix it\n") || !strings.Contains(prompt, "Run ID: "+s.run.ID+"\n") {
		t.Errorf("prompt:\n%s", prompt)
	}

	bn := s.run.BranchNaming
	if bn == nil || bn.Source != domain.BranchSourceInstructions || bn.Proposed != "feature/fix-it" || bn.Rejected != "" ||
		bn.Usage == nil || bn.Usage.CostUSD != 0.002 || bn.Call == nil || bn.Call.PromptBytes != len(prompt) {
		t.Errorf("branch_naming = %+v", bn)
	}
	if out := gitIn(t, dir, "branch", "--list", "feature/fix-it"); out == "" {
		t.Error("the branch was not created")
	}
	if _, err := os.Stat(s.store.CallLogPath(s.run.ID, "branch-name", "stdout")); err != nil {
		t.Errorf("logs/branch-name.stdout.log: %v", err)
	}
	outcome := domain.ComputeOutcome(*s.run, nil)
	if outcome.Usage.Calls != 1 || outcome.Usage.CostUSD != 0.002 || outcome.DecisionOverhead.Calls != 1 {
		t.Errorf("the call is not in the outcome: %+v", outcome)
	}
	if _, err := verifyRunWorktree(context.Background(), s.repo, s.run); err != nil {
		t.Errorf("the worktree on the instructions' branch does not verify: %v", err)
	}
}

// Without a word about branches in CLAUDE.md, AGENTS.md or CONTRIBUTING.md at base_ref nothing is
// called, whatever the working tree says.
func TestOpenWorktree_NoMentionNoCall(t *testing.T) {
	client := &branchClient{output: `{"branch": "feature/fix-it"}`}
	s, dir, out := namingSession(t, map[string]string{"CLAUDE.md": "Be kind.\n"}, client)
	s.cfg.Worktree.Branch.Template = "work/{slug}"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(branchRule), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Errorf("%d calls, want none: the committed file does not mention branches", client.calls)
	}
	if got := s.run.Worktree.Branch; got != "work/fix-it" || s.run.BranchNaming.Source != domain.BranchSourceTemplate {
		t.Errorf("branch = %q, naming = %+v, want the template's", got, s.run.BranchNaming)
	}
	if strings.Contains(out.String(), "Warning") {
		t.Errorf("a skipped call is not a warning:\n%s", out)
	}
}

func TestOpenWorktree_NoInstructionFilesNoCall(t *testing.T) {
	client := &branchClient{output: `{"branch": "feature/fix-it"}`}
	s, _, _ := namingSession(t, nil, client)
	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := domain.WorktreeBranch(s.run.ID)
	if client.calls != 0 || s.run.Worktree.Branch != want || s.run.BranchNaming.Source != domain.BranchSourceDefault {
		t.Errorf("calls = %d, branch = %q, naming = %+v, want the default %s", client.calls, s.run.Worktree.Branch, s.run.BranchNaming, want)
	}
}

// The instructions are asked only when worktree.branch.from_instructions is set.
func TestOpenWorktree_InstructionsNotConfiguredNotRead(t *testing.T) {
	client := &branchClient{output: `{"branch": "feature/fix-it"}`}
	s, _, _ := namingSession(t, map[string]string{"CLAUDE.md": branchRule}, client)
	s.cfg.Worktree.Branch.FromInstructions = nil
	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 || s.run.Worktree.Branch != domain.WorktreeBranch(s.run.ID) {
		t.Errorf("calls = %d, branch = %q", client.calls, s.run.Worktree.Branch)
	}
}

// What the instructions answer falls through to the template, with a warning that says why.
func TestOpenWorktree_InstructionsNameNotUsed(t *testing.T) {
	timeout := &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureTimeout}
	exit := &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureExit}
	tests := []struct {
		name     string
		client   *branchClient
		existing string // a local branch made first
		why      string // in the warning and the record
		proposed string
	}{
		{"no rule", &branchClient{output: `{"branch": ""}`}, "", "they set no rule for branch names", ""},
		{"a name with a space", &branchClient{output: `{"branch": "bad name"}`}, "", "character", "bad name"},
		{"the base branch", &branchClient{output: `{"branch": "main"}`}, "", "base branch main", "main"},
		{"HEAD", &branchClient{output: `{"branch": "HEAD"}`}, "", "the name is HEAD", "HEAD"},
		{"under story/", &branchClient{output: `{"branch": "story/refunds/RF02"}`}, "", "story/", "story/refunds/RF02"},
		{"two dots", &branchClient{output: `{"branch": "a..b"}`}, "", "..", "a..b"},
		{"a .lock name", &branchClient{output: `{"branch": "x.lock"}`}, "", ".lock", "x.lock"},
		{"an existing branch", &branchClient{output: `{"branch": "feature/taken"}`}, "feature/taken", "feature/taken exists", "feature/taken"},
		{"prose", &branchClient{output: "I would call it feature/x"}, "", "invalid response", ""},
		{"a failed call", &branchClient{err: exit}, "", "the call failed (exit)", ""},
		{"a timeout", &branchClient{err: timeout}, "", "the call timed out after 1m0s", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, dir, out := namingSession(t, map[string]string{"AGENTS.md": branchRule}, tc.client)
			s.cfg.Worktree.Branch.Template = "work/{slug}"
			if tc.existing != "" {
				gitIn(t, dir, "branch", tc.existing)
			}

			if _, err := openWorktree(context.Background(), s); err != nil {
				t.Fatalf("a name that cannot be used must not fail the run: %v", err)
			}
			if got := s.run.Worktree.Branch; got != "work/fix-it" {
				t.Errorf("branch = %q, want the template's", got)
			}
			bn := s.run.BranchNaming
			if bn.Source != domain.BranchSourceTemplate || bn.Proposed != tc.proposed || !strings.Contains(bn.Rejected, tc.why) {
				t.Errorf("branch_naming = %+v, want template, proposed %q, rejected naming %q", bn, tc.proposed, tc.why)
			}
			if want := "Warning: branch name from the instructions not used: "; !strings.Contains(out.String(), want) ||
				!strings.Contains(out.String(), tc.why) {
				t.Errorf("output lacks the warning %q ... %q:\n%s", want, tc.why, out)
			}
			if tc.client.calls != 1 {
				t.Errorf("%d calls, want 1", tc.client.calls)
			}
		})
	}
}

// Without a template the default follows; the run still starts.
func TestOpenWorktree_InstructionsNameNotUsedNoTemplate(t *testing.T) {
	s, _, out := namingSession(t, map[string]string{"CONTRIBUTING.md": branchRule}, &branchClient{output: `{"branch": "-x"}`})
	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	bn := s.run.BranchNaming
	if s.run.Worktree.Branch != domain.WorktreeBranch(s.run.ID) || bn.Source != domain.BranchSourceDefault || !strings.Contains(bn.Rejected, "starts with -") {
		t.Errorf("branch = %q, naming = %+v", s.run.Worktree.Branch, bn)
	}
	if !strings.Contains(out.String(), "Warning: branch name from the instructions not used: the name starts with -") {
		t.Errorf("output:\n%s", out)
	}
}

// A credential problem ends the run wherever it happens.
func TestOpenWorktree_BranchCallAuthFailureIsFatal(t *testing.T) {
	client := &branchClient{err: &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureAuth}}
	s, dir, _ := namingSession(t, map[string]string{"CLAUDE.md": branchRule}, client)
	s.cfg.Worktree.Branch.Template = "work/{slug}"

	_, err := openWorktree(context.Background(), s)
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != apperr.FailureAuth {
		t.Fatalf("err = %v, want the authentication failure", err)
	}
	if s.run.Worktree != nil {
		t.Errorf("worktree recorded: %+v", s.run.Worktree)
	}
	if list := gitIn(t, dir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("a worktree was added:\n%s", list)
	}
}

func TestOpenWorktree_BranchFromTheTemplate(t *testing.T) {
	jira := &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12", URL: "https://x.example/browse/PAY-12", FetchedAt: time.Now()}
	tests := []struct {
		name     string
		template string
		task     string
		source   *domain.TaskSource
		want     string
	}{
		{"no key", "feature/{key}-{slug}", "Fix it", nil, "feature/fix-it"},
		{"a Jira key", "feature/{key}-{slug}", "Fix it", jira, "feature/PAY-12-fix-it"},
		{"Cyrillic", "feature/{slug}", "Исправить панику в Store.Put\nподробности", nil, "feature/ispravit-paniku-v-store-put"},
		{"the first line only", "{slug}", "\n  Add a cache  \nthen more words", nil, "add-a-cache"},
		{"a long title", "{slug}", strings.Repeat("word ", 20), nil, strings.TrimSuffix(strings.Repeat("word-", 8), "-")},
		{"date and id", "{date}/{id}", "x", nil, "20261027/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			branchToday = func() time.Time { return time.Date(2026, 10, 27, 23, 59, 0, 0, time.Local) }
			t.Cleanup(func() { branchToday = time.Now })
			s, _, out := worktreeSession(t, true, implementTest)
			s.cfg.Worktree.Branch.Template = tc.template
			s.run.Task.Description, s.run.Task.Source = tc.task, tc.source

			if _, err := openWorktree(context.Background(), s); err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if strings.HasSuffix(want, "/") {
				want += s.run.ID
			}
			if got := s.run.Worktree.Branch; got != want {
				t.Errorf("branch = %q, want %q", got, want)
			}
			if bn := s.run.BranchNaming; bn.Source != domain.BranchSourceTemplate || bn.Rejected != "" || bn.Usage != nil || bn.Call != nil {
				t.Errorf("branch_naming = %+v", bn)
			}
			if strings.Contains(out.String(), "Warning") {
				t.Errorf("unexpected warning:\n%s", out)
			}
		})
	}
}

// A template name that exists gets -<last 6 of the run ID> once; a name that still cannot be used
// falls to the default, with a warning.
func TestOpenWorktree_TemplateNameTaken(t *testing.T) {
	s, dir, out := worktreeSession(t, true, implementTest)
	s.cfg.Worktree.Branch.Template = "feature/{slug}"
	gitIn(t, dir, "branch", "feature/fix-it")

	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := "feature/fix-it-" + s.run.ID[len(s.run.ID)-6:]
	if got := s.run.Worktree.Branch; got != want || s.run.BranchNaming.Source != domain.BranchSourceTemplate {
		t.Errorf("branch = %q, naming = %+v, want %q", got, s.run.BranchNaming, want)
	}
	if strings.Contains(out.String(), "Warning") {
		t.Errorf("a suffixed name is not a warning:\n%s", out)
	}

	// The suffix is added once: with that taken too, the default follows.
	s2, dir2, out2 := worktreeSession(t, true, implementTest)
	s2.cfg.Worktree.Branch.Template = "feature/{slug}"
	gitIn(t, dir2, "branch", "feature/fix-it")
	gitIn(t, dir2, "branch", "feature/fix-it-"+s2.run.ID[len(s2.run.ID)-6:])
	if _, err := openWorktree(context.Background(), s2); err != nil {
		t.Fatal(err)
	}
	if s2.run.Worktree.Branch != domain.WorktreeBranch(s2.run.ID) || s2.run.BranchNaming.Source != domain.BranchSourceDefault {
		t.Errorf("branch = %q, naming = %+v, want the default", s2.run.Worktree.Branch, s2.run.BranchNaming)
	}
	if !strings.Contains(out2.String(), "Warning: branch name from worktree.branch.template not used: the branch feature/fix-it-") {
		t.Errorf("output:\n%s", out2)
	}
}

func TestOpenWorktree_TemplateNameNotUsed(t *testing.T) {
	tests := []struct {
		name, template, task, why string
	}{
		{"an empty name", "{slug}", "!!!", "the name is empty"},
		{"under story/", "story/{slug}", "Fix it", "story/"},
		{"the base branch", "main", "Fix it", "base branch main"},
		{"a space", "my branch/{slug}", "Fix it", "character"},
		{"too long", strings.Repeat("a", 90) + "/{slug}-{slug}", strings.Repeat("b", 40), "at most 100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _, out := worktreeSession(t, true, implementTest)
			s.cfg.Worktree.Branch.Template = tc.template
			s.run.Task.Description = tc.task

			if _, err := openWorktree(context.Background(), s); err != nil {
				t.Fatal(err)
			}
			bn := s.run.BranchNaming
			if s.run.Worktree.Branch != domain.WorktreeBranch(s.run.ID) || bn.Source != domain.BranchSourceDefault || !strings.Contains(bn.Rejected, tc.why) {
				t.Errorf("branch = %q, naming = %+v, want the default and a rejection naming %q", s.run.Worktree.Branch, bn, tc.why)
			}
			if want := "Warning: branch name from worktree.branch.template not used: "; !strings.Contains(out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		})
	}
}

// A story task keeps the branch its task input gave: nothing is named, nothing is called.
func TestOpenWorktree_StoryTaskIsNotNamed(t *testing.T) {
	client := &branchClient{output: `{"branch": "feature/fix-it"}`}
	s, dir, _ := namingSession(t, map[string]string{"CLAUDE.md": branchRule}, client)
	s.cfg.Worktree.Branch.Template = "work/{slug}"
	s.run.Task.Source = &domain.TaskSource{
		Kind: domain.TaskSourceStory, Story: "refunds", Task: "RF02", Attempt: 1,
		Branch: "story/refunds/RF02", BaseRef: gitIn(t, dir, "rev-parse", "HEAD"), BaseBranch: "main",
	}
	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 || s.run.Worktree.Branch != "story/refunds/RF02" || s.run.BranchNaming != nil {
		t.Errorf("calls = %d, branch = %q, naming = %+v", client.calls, s.run.Worktree.Branch, s.run.BranchNaming)
	}
}

// The naming takes its inputs as parameters, so a story task can be named with its own key, title
// and fallback (specs/delivery/SPEC.md §9.1): the fallback is used as given, even under story/.
func TestNameBranch_TakesItsInputsAsParameters(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	s.cfg.Worktree.Branch.Template = "{key}/{slug}"
	base := gitIn(t, dir, "rev-parse", "HEAD")
	in := branchInput{Key: "RF-7", Task: "Partial refunds", Fallback: "story/refunds/RF02", BaseRef: base, BaseBranch: "main"}

	name, naming, err := nameBranch(context.Background(), s, in)
	if err != nil || name != "RF-7/partial-refunds" || naming.Source != domain.BranchSourceTemplate {
		t.Fatalf("name = %q, naming = %+v, err = %v", name, naming, err)
	}

	s.cfg.Worktree.Branch.Template = ""
	name, naming, err = nameBranch(context.Background(), s, in)
	if err != nil || name != "story/refunds/RF02" || naming.Source != domain.BranchSourceDefault {
		t.Fatalf("name = %q, naming = %+v, err = %v", name, naming, err)
	}

	// A reserved branch, such as the delivery target, is never a rule's name.
	s.cfg.Worktree.Branch.Template = "release"
	in.Reserved = []string{"release"}
	name, naming, err = nameBranch(context.Background(), s, in)
	if err != nil || name != "story/refunds/RF02" || !strings.Contains(naming.Rejected, "target branch release") {
		t.Fatalf("name = %q, naming = %+v, err = %v", name, naming, err)
	}
}

// A resume continues in a worktree on a templated branch, and refuses a branch that was edited.
func TestVerifyRunWorktree_TemplatedBranch(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	s.cfg.Worktree.Branch.Template = "feature/{slug}"
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	operator := s.operator
	w := s.run.Worktree
	if w.Branch != "feature/fix-it" {
		t.Fatalf("branch = %q", w.Branch)
	}

	wt, err := verifyRunWorktree(context.Background(), operator, s.run)
	if err != nil || wt.Dir != w.Path {
		t.Fatalf("a worktree on a templated branch must verify: %v", err)
	}

	// Another worktree's branch: git lists the run's path on the branch it is really on.
	gitIn(t, dir, "worktree", "add", "-q", "-b", "other/branch", filepath.Join(t.TempDir(), "elsewhere"))
	w.Branch = "other/branch"
	_, err = verifyRunWorktree(context.Background(), operator, s.run)
	assertRefused(t, err, apperr.RunNotResumable, "is on refs/heads/feature/fix-it, not refs/heads/other/branch")

	// A name the rules refuse never reaches git.
	for _, bad := range []string{"story/refunds/RF02", "-x", "a b", ""} {
		w.Branch = bad
		_, err = verifyRunWorktree(context.Background(), operator, s.run)
		assertRefused(t, err, apperr.RunCorrupt, "worktree.branch")
	}
}

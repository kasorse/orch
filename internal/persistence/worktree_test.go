package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

const wtRunID = "0b6f3c1e-1111-4222-8333-444455556666"

func wtRun(mutate func(*domain.Run)) *domain.Run {
	run := &domain.Run{
		ID:      wtRunID,
		Status:  domain.RunCompleted,
		Options: domain.RunOptions{Isolation: domain.IsolationWorktree},
		Worktree: &domain.RunWorktree{
			Path:       "/state/orchestrator/worktrees/4f1c/" + wtRunID,
			Branch:     "orchestrator/" + wtRunID,
			BaseRef:    strings.Repeat("ab", 20),
			BaseBranch: "master",
			AdminDir:   "/repo/.git/worktrees/x",
			SetupDone:  true,
		},
	}
	if mutate != nil {
		mutate(run)
	}
	return run
}

func TestValidateWorktree(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*domain.Run)
		want   string // "" = valid
	}{
		{"valid", nil, ""},
		{"no worktree, no isolation", func(r *domain.Run) { r.Worktree = nil; r.Options.Isolation = "" }, ""},
		{"empty base branch", func(r *domain.Run) { r.Worktree.BaseBranch = "" }, ""},
		{"conflicts on completed", func(r *domain.Run) { r.Worktree.Conflicts = []string{"a.go"} }, ""},
		{"removed on completed", func(r *domain.Run) { r.Worktree.Removed = true }, ""},
		{"unknown isolation", func(r *domain.Run) { r.Options.Isolation = "chroot" }, "options.isolation"},
		{"inplace run with worktree", func(r *domain.Run) { r.Options.Isolation = domain.IsolationInplace }, "worktree"},
		{"legacy run with worktree", func(r *domain.Run) { r.Options.Isolation = "" }, "worktree"},
		{"another run's default branch is a valid name", func(r *domain.Run) { r.Worktree.Branch = "orchestrator/other" }, ""},
		{"a templated branch", func(r *domain.Run) {
			r.Worktree.Branch = "feature/PAY-12-use-plus-in-add"
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceTemplate}
		}, ""},
		{"a branch from the instructions", func(r *domain.Run) {
			r.Worktree.Branch = "feat/PAY-12"
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceInstructions, Proposed: "feat/PAY-12"}
		}, ""},
		{"branch under story/", func(r *domain.Run) { r.Worktree.Branch = "story/refunds/RF02" }, "worktree.branch"},
		{"branch that is an option", func(r *domain.Run) { r.Worktree.Branch = "-x" }, "worktree.branch"},
		{"branch with a space", func(r *domain.Run) { r.Worktree.Branch = "feature/a b" }, "worktree.branch"},
		{"empty branch", func(r *domain.Run) { r.Worktree.Branch = "" }, "worktree.branch"},
		{"default naming on another branch", func(r *domain.Run) {
			r.Worktree.Branch = "feature/x"
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceDefault}
		}, "branch_naming.source is default"},
		{"instructions naming on another branch", func(r *domain.Run) {
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceInstructions, Proposed: "feat/x"}
		}, "branch_naming.source is instructions"},
		{"unknown naming source", func(r *domain.Run) { r.BranchNaming = &domain.BranchNaming{Source: "model"} }, "branch_naming"},
		{"naming without a worktree", func(r *domain.Run) {
			r.Worktree = nil
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceDefault, Rejected: "the name is empty"}
		}, ""},
		{"naming on an inplace run", func(r *domain.Run) {
			r.Worktree, r.Options.Isolation = nil, domain.IsolationInplace
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceDefault}
		}, "branch_naming"},
		{"multi-line rejection", func(r *domain.Run) {
			r.BranchNaming = &domain.BranchNaming{Source: domain.BranchSourceDefault, Rejected: "a\nb"}
		}, "branch_naming"},
		{"short base_ref", func(r *domain.Run) { r.Worktree.BaseRef = "abc123" }, "worktree.base_ref"},
		{"base_ref upper case", func(r *domain.Run) { r.Worktree.BaseRef = strings.Repeat("AB", 20) }, "worktree.base_ref"},
		{"bad base_branch", func(r *domain.Run) { r.Worktree.BaseBranch = "-x" }, "worktree.base_branch"},
		{"conflicts on running", func(r *domain.Run) { r.Status = domain.RunRunning; r.Worktree.Conflicts = []string{"a.go"} }, "worktree.conflicts"},
		{"conflicts on failed", func(r *domain.Run) { r.Status = domain.RunFailed; r.Worktree.Conflicts = []string{"a.go"} }, "worktree.conflicts"},
		{"removed on running", func(r *domain.Run) { r.Status = domain.RunRunning; r.Worktree.Removed = true }, "worktree.removed"},
	}
	for _, c := range cases {
		err := validateRun(wtRun(c.mutate), wtRunID)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: validateRun() = %v, want nil", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: validateRun() = %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

// run.mr (specs/delivery/SPEC.md §13): its own rules, and that it belongs to a completed worktree
// run that has the commit it pushed.
func TestValidateMR(t *testing.T) {
	sha := strings.Repeat("cd", 20)
	created := func(r *domain.Run) {
		r.Commit = sha
		r.MR = &domain.RunMR{State: domain.MRCreated, Remote: "origin", Source: r.Worktree.Branch, Target: "master", IID: 7,
			URL: "https://gitlab.example.com/g/p/-/merge_requests/7"}
	}
	cases := []struct {
		name   string
		mutate func(*domain.Run)
		want   string // "" = valid
	}{
		{"no mr", nil, ""},
		{"created", created, ""},
		{"skipped without a commit", func(r *domain.Run) { r.MR = &domain.RunMR{Skipped: "the review did not pass"} }, ""},
		{"intent", func(r *domain.Run) {
			created(r)
			r.MR = &domain.RunMR{State: domain.MRIntent, Remote: "origin", Source: r.Worktree.Branch, Target: "master", Error: "fatal: nope"}
		}, ""},
		{"invalid record", func(r *domain.Run) { created(r); r.MR.URL = "http://x/-/merge_requests/7" }, "mr:"},
		{"on a running run", func(r *domain.Run) { created(r); r.Status = domain.RunRunning }, "only a completed run"},
		{"on a failed run", func(r *domain.Run) { created(r); r.Status = domain.RunFailed }, "only a completed run"},
		{"without a worktree", func(r *domain.Run) { created(r); r.Worktree = nil; r.Options.Isolation = "" }, "without a worktree"},
		{"pushed without a commit", func(r *domain.Run) { created(r); r.Commit = "" }, "without a commit"},
		{"another branch", func(r *domain.Run) { created(r); r.MR.Source = "orchestrator/other" }, "mr.source"},
	}
	for _, c := range cases {
		err := validateRun(wtRun(c.mutate), wtRunID)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: validateRun() = %v, want nil", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: validateRun() = %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

// run.jira_write and options.jira_write (specs/delivery/SPEC.md §13): the record is checked
// against the options, and it needs a merge request this run created for a task with a key.
func TestValidateJiraWrite(t *testing.T) {
	created := func(r *domain.Run) {
		r.Commit = strings.Repeat("cd", 20)
		r.MR = &domain.RunMR{State: domain.MRCreated, Remote: "origin", Source: r.Worktree.Branch, Target: "master", IID: 7,
			URL: "https://gitlab.example.com/g/p/-/merge_requests/7"}
		r.Task.Source = &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12", URL: "https://jira.example.com/browse/PAY-12",
			FetchedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
		r.Options.JiraWrite = domain.JiraWriteOptions{Comment: true, Transition: "In Review"}
	}
	written := func(r *domain.Run) {
		created(r)
		r.JiraWrite = &domain.RunJiraWrite{
			Comment:    &domain.RunJiraComment{State: domain.JiraCommentDone, ID: "10042"},
			Transition: &domain.RunJiraTransition{Name: "In Review", State: domain.JiraTransitionDone},
		}
	}
	cases := []struct {
		name   string
		mutate func(*domain.Run)
		want   string // "" = valid
	}{
		{"nothing written", created, ""},
		{"written", written, ""},
		{"options only", func(r *domain.Run) { r.Options.JiraWrite = domain.JiraWriteOptions{Comment: true} }, ""},
		{"a transition with a line feed", func(r *domain.Run) { r.Options.JiraWrite.Transition = "a\nb" }, "options.jira_write"},
		{"a record for another transition", func(r *domain.Run) { written(r); r.JiraWrite.Transition.Name = "Done" }, "jira_write: transition.name"},
		{"without a merge request", func(r *domain.Run) { written(r); r.MR.State = domain.MRPushed; r.MR.IID, r.MR.URL = 0, "" }, "did not create a merge request"},
		{"without an mr record", func(r *domain.Run) { written(r); r.MR = nil }, "did not create a merge request"},
		{"without a key", func(r *domain.Run) { written(r); r.Task.Source = nil }, "without a Jira key"},
	}
	for _, c := range cases {
		err := validateRun(wtRun(c.mutate), wtRunID)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: validateRun() = %v, want nil", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: validateRun() = %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

func TestDecodeRun_JiraWriteRoundTrip(t *testing.T) {
	run := wtRun(func(r *domain.Run) {
		r.Commit = strings.Repeat("cd", 20)
		r.MR = &domain.RunMR{State: domain.MRCreated, Remote: "origin", Source: r.Worktree.Branch, Target: "master", IID: 7,
			URL: "https://gitlab.example.com/g/p/-/merge_requests/7"}
		r.Task.Source = &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12", URL: "https://jira.example.com/browse/PAY-12",
			FetchedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
		r.Options.JiraWrite = domain.JiraWriteOptions{Comment: true}
		r.JiraWrite = &domain.RunJiraWrite{Comment: &domain.RunJiraComment{State: domain.JiraCommentIntent}}
	})
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRun(data, wtRunID)
	if err != nil || !reflect.DeepEqual(got.JiraWrite, run.JiraWrite) || got.Options.JiraWrite != run.Options.JiraWrite {
		t.Fatalf("round trip: %+v / %+v, %v; want %+v / %+v", got.JiraWrite, got.Options.JiraWrite, err, run.JiraWrite, run.Options.JiraWrite)
	}
	bad := strings.Replace(string(data), `"state":"intent"`, `"state":"intent","extra":1`, 1)
	if _, err := decodeRun([]byte(bad), wtRunID); err == nil {
		t.Error("an unknown jira_write field was accepted")
	}
	if old, _ := json.Marshal(wtRun(nil)); strings.Contains(string(old), "jira_write") {
		t.Errorf("a run that wrote nothing writes %s", old)
	}
}

func TestDecodeRun_MRRoundTrip(t *testing.T) {
	run := wtRun(func(r *domain.Run) {
		r.Commit = strings.Repeat("cd", 20)
		r.MR = &domain.RunMR{State: domain.MRPushed, Remote: "origin", Source: r.Worktree.Branch, Target: "master", Skipped: "declined at merge request"}
	})
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRun(data, wtRunID)
	if err != nil || !reflect.DeepEqual(got.MR, run.MR) {
		t.Fatalf("round trip: %+v, %v; want %+v", got.MR, err, run.MR)
	}
	bad := strings.Replace(string(data), `"declined at merge request"`, `"declined at merge request","extra":1`, 1)
	if _, err := decodeRun([]byte(bad), wtRunID); err == nil {
		t.Error("unknown mr field accepted")
	}
	if old, _ := json.Marshal(wtRun(nil)); strings.Contains(string(old), `"mr"`) {
		t.Errorf("a run without a merge request writes %s", old)
	}
}

func TestDecodeRun_WorktreeRoundTrip(t *testing.T) {
	run := wtRun(func(r *domain.Run) { r.Worktree.Conflicts = []string{"internal/app/app.go"} })
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRun(data, wtRunID)
	if err != nil {
		t.Fatalf("decodeRun: %v", err)
	}
	if !reflect.DeepEqual(got.Worktree, run.Worktree) || got.Options.Isolation != domain.IsolationWorktree {
		t.Errorf("round trip: %+v / %+v, want %+v", got.Worktree, got.Options, run.Worktree)
	}

	// A run written before the field has neither and loads as inplace.
	old, err := json.Marshal(domain.Run{ID: wtRunID, Status: domain.RunFailed})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(old), "worktree") {
		t.Errorf("a run without a worktree writes %s", old)
	}
	got, err = decodeRun(old, wtRunID)
	if err != nil || got.Worktree != nil || got.Options.IsolationMode() != domain.IsolationInplace {
		t.Errorf("legacy run: %+v, %v, want inplace without a worktree", got, err)
	}

	// An unknown field of the worktree is refused.
	bad := strings.Replace(string(data), `"removed"`, `"x"`, 1)
	bad = strings.Replace(bad, `"setup_done":true`, `"setup_done":true,"extra":1`, 1)
	if _, err := decodeRun([]byte(bad), wtRunID); err == nil {
		t.Error("unknown worktree field accepted")
	}
}

func TestWorktreePath(t *testing.T) {
	base := t.TempDir()
	common := filepath.Join(base, "repo", ".git")
	if err := os.MkdirAll(common, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "repo"), link); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state")

	direct := WorktreePath(state, common, wtRunID)
	if via := WorktreePath(state, filepath.Join(link, ".git"), wtRunID); via != direct {
		t.Errorf("through a symlink: %q, want %q", via, direct)
	}
	dir := filepath.Dir(direct)
	if filepath.Base(direct) != wtRunID || filepath.Dir(dir) != filepath.Join(state, "orchestrator", "worktrees") || len(filepath.Base(dir)) != 16 {
		t.Errorf("WorktreePath = %q, want <state>/orchestrator/worktrees/<16 hex>/<run-id>", direct)
	}
	other := filepath.Join(base, "other", ".git")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if WorktreePath(state, other, wtRunID) == direct {
		t.Error("two repositories share a worktree directory")
	}
}

func TestStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if got, err := StateDir(); err != nil || got != "/xdg/state" {
		t.Errorf("StateDir() = %q, %v, want /xdg/state", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, err := StateDir(); err != nil || got != "/home/u/.local/state" {
		t.Errorf("StateDir() = %q, %v, want /home/u/.local/state", got, err)
	}
}

// TestLockWorktrees: a second holder waits until the first lets go, so two processes never run git
// worktree commands of one repository at once (TODO.md S7).
func TestLockWorktrees(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestrator", "worktrees", "0123456789abcdef")
	unlock, err := LockWorktrees(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func(), 1)
	go func() {
		second, err := LockWorktrees(dir)
		if err != nil {
			t.Error(err)
			second = func() {}
		}
		got <- second
	}()
	select {
	case second := <-got:
		second()
		t.Fatal("the second lock was taken while the first was held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case second := <-got:
		second()
	case <-time.After(10 * time.Second):
		t.Fatal("the second lock was not taken after the first was released")
	}
	if _, err := os.Stat(dir + ".lock"); err != nil {
		t.Errorf("the lock file: %v", err)
	}
}

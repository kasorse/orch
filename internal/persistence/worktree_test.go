package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/domain"
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
		{"foreign branch", func(r *domain.Run) { r.Worktree.Branch = "orchestrator/other" }, "worktree.branch"},
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

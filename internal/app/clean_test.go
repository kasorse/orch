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
)

func TestRunsCleanArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    cleanArgs
		wantErr bool
	}{
		{"defaults", nil, cleanArgs{olderThan: 7 * 24 * time.Hour}, false},
		{"days", []string{"--older-than", "2d"}, cleanArgs{olderThan: 48 * time.Hour}, false},
		{"equals and dry run", []string{"--older-than=36h", "--dry-run"}, cleanArgs{olderThan: 36 * time.Hour, dryRun: true}, false},
		{"zero days", []string{"--older-than", "0d"}, cleanArgs{}, true},
		{"negative", []string{"--older-than=-1h"}, cleanArgs{}, true},
		{"junk", []string{"--older-than", "soon"}, cleanArgs{}, true},
		{"twice", []string{"--older-than", "1d", "--older-than", "2d"}, cleanArgs{}, true},
		{"missing value", []string{"--older-than"}, cleanArgs{}, true},
		{"unknown", []string{"--force"}, cleanArgs{}, true},
		{"dry run twice", []string{"--dry-run", "--dry-run"}, cleanArgs{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runsCleanArgs(tc.args)
			if tc.wantErr {
				var ce *apperr.ConfigError
				if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "usage: orchestrator runs clean") {
					t.Fatalf("error = %v, want a ConfigError with the usage", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

// oldWorktreeRun creates the run's worktree in s's repository and ages the run by age.
func oldWorktreeRun(t *testing.T, s *session, age time.Duration) string {
	t.Helper()
	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	s.run.CreatedAt = time.Now().Add(-age)
	if err := s.store.SaveRun(s.run); err != nil {
		t.Fatal(err)
	}
	return s.run.Worktree.Path
}

func TestCleanWorktrees(t *testing.T) {
	week := cleanArgs{olderThan: 7 * 24 * time.Hour}
	clean := func(t *testing.T, s *session, args cleanArgs) string {
		t.Helper()
		var out bytes.Buffer
		if err := cleanWorktrees(context.Background(), &out, s.store, s.repo, args, time.Now()); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	exists := func(path string) bool { _, err := os.Lstat(path); return err == nil }

	t.Run("old run is removed and recorded, branch stays", func(t *testing.T) {
		s, dir, _ := worktreeSession(t, true, implementTest)
		path := oldWorktreeRun(t, s, 10*24*time.Hour)
		branch := s.run.Worktree.Branch
		refs := gitIn(t, dir, "for-each-ref")

		out := clean(t, s, week)
		if exists(path) || !strings.Contains(out, "removed "+path) {
			t.Fatalf("worktree not removed:\n%s", out)
		}
		run, err := s.store.LoadRun(s.run.ID)
		if err != nil || !run.Worktree.Removed {
			t.Fatalf("removed not recorded: %v %+v", err, run.Worktree)
		}
		if got := gitIn(t, dir, "for-each-ref"); got != refs || !strings.Contains(got, "refs/heads/"+branch) {
			t.Errorf("refs changed:\nbefore %s\nafter  %s", refs, got)
		}
	})

	t.Run("recent run is left alone", func(t *testing.T) {
		s, _, _ := worktreeSession(t, true, implementTest)
		path := oldWorktreeRun(t, s, time.Hour)
		clean(t, s, week)
		if !exists(path) {
			t.Fatal("a recent run's worktree was removed")
		}
	})

	t.Run("locked run is skipped", func(t *testing.T) {
		s, _, _ := worktreeSession(t, true, implementTest)
		path := oldWorktreeRun(t, s, 10*24*time.Hour)
		unlock, err := s.store.Lock(s.run.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		out := clean(t, s, week)
		if !exists(path) || !strings.Contains(out, "in use") {
			t.Fatalf("locked run not skipped:\n%s", out)
		}
	})

	t.Run("uncommitted file keeps the worktree", func(t *testing.T) {
		s, _, _ := worktreeSession(t, true, implementTest)
		path := oldWorktreeRun(t, s, 10*24*time.Hour)
		if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := clean(t, s, week)
		if !exists(path) || !strings.Contains(out, "kept "+path) || !strings.Contains(out, "git worktree remove failed") {
			t.Fatalf("worktree not kept with git's reason:\n%s", out)
		}
		run, err := s.store.LoadRun(s.run.ID)
		if err != nil || run.Worktree.Removed {
			t.Fatalf("a kept worktree was recorded as removed: %v", err)
		}
	})

	t.Run("dry run changes nothing", func(t *testing.T) {
		s, dir, _ := worktreeSession(t, true, implementTest)
		path := oldWorktreeRun(t, s, 10*24*time.Hour)
		before, err := os.ReadFile(filepath.Join(s.store.RunDir(s.run.ID), "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		list := gitIn(t, dir, "worktree", "list", "--porcelain")

		out := clean(t, s, cleanArgs{olderThan: week.olderThan, dryRun: true})
		after, _ := os.ReadFile(filepath.Join(s.store.RunDir(s.run.ID), "run.json"))
		if !exists(path) || !bytes.Equal(before, after) || gitIn(t, dir, "worktree", "list", "--porcelain") != list {
			t.Fatal("dry run changed something")
		}
		if !strings.Contains(out, "would remove "+path) {
			t.Fatalf("dry run did not list the worktree:\n%s", out)
		}
	})
}

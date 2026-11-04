package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/story"
)

const twoTaskAnswer = `{"slug": "refunds", "prefix": "RF", "title": "Partial refunds",
 "spec": {"problem": "p", "approach": "a", "contracts": "c", "out_of_scope": "o", "risks": "r"},
 "tasks": [
  {"id": "RF01", "repo": "billing", "title": "Refund an amount", "size": "M", "depends_on": [],
   "goal": "Refund part of a payment.", "acceptance": ["a refund of 5 of 10 leaves 5"], "notes": ""},
  {"id": "RF02", "repo": "gateway", "title": "Сумма в API", "size": "S", "depends_on": ["RF01"],
   "goal": "Accept an amount.", "acceptance": ["the API takes an amount"], "notes": ""}]}`

func parseTwoTasks(t *testing.T) story.SpecifyAnswer {
	t.Helper()
	a, err := story.ParseSpecify([]byte(twoTaskAnswer), []string{"billing", "gateway"}, 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestWriteSpec(t *testing.T) {
	root := filepath.Join(t.TempDir(), "specs")
	answer := parseTwoTasks(t)
	tasks := answer.Tasks()

	dir, err := writeSpec(root, answer, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "refunds") {
		t.Errorf("dir = %s", dir)
	}
	for _, name := range []string{"README.md", "SPEC.md", "tasks/TASK-RF01-refund-an-amount.md", "tasks/TASK-RF02-api.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "tasks/TASK-RF02-api.md" {
			task, err := story.ParseTaskFile(filepath.Base(name), data)
			if err != nil || task.ID != "RF02" || task.Title != "Сумма в API" || !slices.Equal(task.DependsOn, []string{"RF01"}) {
				t.Errorf("%s parses to %+v, %v", name, task, err)
			}
		}
	}

	// The directory exists now: a second spec of the slug is refused, and nothing in it changes.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = writeSpec(root, answer, tasks)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "exists already") {
		t.Fatalf("err = %v, want the existing directory refused", err)
	}
	if data, err := os.ReadFile(readme); err != nil || string(data) != "edited\n" {
		t.Errorf("README.md = %q, %v", data, err)
	}
}

// An empty directory of the slug is refused too: nothing is written into it.
func TestWriteSpec_EmptyDirRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "refunds"), 0o755); err != nil {
		t.Fatal(err)
	}
	answer := parseTwoTasks(t)
	if _, err := writeSpec(root, answer, answer.Tasks()); err == nil {
		t.Fatal("an existing empty directory was written into")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "refunds")); err != nil || len(entries) != 0 {
		t.Errorf("entries = %v, %v", entries, err)
	}
}

// A service without its own orchestrator.yaml stops the plan with a config error naming it.
func TestLoadStoryService_NoConfig(t *testing.T) {
	svc := serviceCandidate{Name: "billing", Path: t.TempDir()}
	_, err := loadStoryService(context.Background(), svc, t.TempDir(), config.Config{}, nil, nil)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "service billing has no") {
		t.Fatalf("err = %v, want a config error naming the service", err)
	}
}

// The story home, when it is a service, is surveyed with its own, already validated config.
func TestLoadStoryService_Home(t *testing.T) {
	home := t.TempDir()
	var cfg config.Config
	cfg.Actions.Investigate = config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "codex", Model: "m"}, Timeout: time.Minute}
	cfg.Context.ExtraDeny = []string{"secrets/**"}

	got, err := loadStoryService(context.Background(), serviceCandidate{Name: "platform", Path: home}, home, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.route.Provider != "codex" || got.timeout != time.Minute || got.survey.Dir != home ||
		!slices.Equal(got.survey.ExtraDeny, []string{"secrets/**"}) || got.survey.RunsDir != runsBaseDir {
		t.Errorf("service = %+v", got)
	}
}

func TestDisplayPath(t *testing.T) {
	cwd := filepath.Join(string(filepath.Separator), "src", "platform")
	for _, tc := range []struct{ p, want string }{
		{filepath.Join(cwd, "specs", "refunds"), filepath.Join("specs", "refunds")},
		{filepath.Join(string(filepath.Separator), "src", "billing"), filepath.Join(string(filepath.Separator), "src", "billing")},
		{filepath.Join(string(filepath.Separator), "src", "platform-x"), filepath.Join(string(filepath.Separator), "src", "platform-x")},
	} {
		if got := displayPath(cwd, tc.p); got != tc.want {
			t.Errorf("displayPath(%s) = %s, want %s", tc.p, got, tc.want)
		}
	}
}

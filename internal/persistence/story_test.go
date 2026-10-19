package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/story"
)

func TestNewStoryPlan_Layout(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".orchestrator", "stories")
	p, err := NewStoryPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(p.Dir())
	id, ok := strings.CutPrefix(name, StoryPlanPrefix)
	if !ok || !isRunID(id) || filepath.Dir(p.Dir()) != base {
		t.Fatalf("Dir() = %s, want %s/.plan-<uuid>", p.Dir(), base)
	}
	for _, sub := range []string{"", "artifacts", "logs"} {
		info, err := os.Stat(filepath.Join(p.Dir(), sub))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
			t.Errorf("%q: %v %v", sub, info, err)
		}
	}

	other, err := NewStoryPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	if other.Dir() == p.Dir() {
		t.Error("two plans share a directory")
	}
}

func TestStoryPlan_FilesAndLogs(t *testing.T) {
	p, err := NewStoryPlan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ticket := domain.Ticket{Key: "PAY-1", URL: "https://j/browse/PAY-1", Type: "Epic", Summary: "S", Components: []string{"billing"}}
	if err := p.SaveTicket(ticket); err != nil {
		t.Fatal(err)
	}
	var got domain.Ticket
	readJSON(t, filepath.Join(p.Dir(), "ticket.json"), &got)
	if !reflect.DeepEqual(got, ticket) {
		t.Errorf("ticket.json = %+v", got)
	}

	art := PlanningArtifact{Output: `{"slug": "x"}`, Error: "tasks[0].repo: no", Prompt: "ACTION: specify\n<<<TASK"}
	if err := p.SaveArtifact("specify", art); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir(), "artifacts", "specify.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"answer": null`) || !strings.Contains(string(data), "<<<TASK") ||
		strings.Index(string(data), `"prompt"`) < strings.Index(string(data), `"output"`) {
		t.Errorf("specify.json:\n%s", data)
	}
	for _, bad := range []string{"../specify", "survey-", "survey-a/b", "plan"} {
		if err := p.SaveArtifact(bad, art); err == nil {
			t.Errorf("SaveArtifact(%q) was accepted", bad)
		}
	}

	if err := p.AppendCallLog("survey-billing", "stdout", "one "); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendCallLog("survey-billing", "stdout", "two"); err != nil {
		t.Fatal(err)
	}
	logPath := p.CallLogPath("survey-billing", "stdout")
	if logPath != filepath.Join(p.Dir(), "logs", "survey-billing.stdout.log") {
		t.Errorf("CallLogPath = %s", logPath)
	}
	if data, err := os.ReadFile(logPath); err != nil || string(data) != "one two" {
		t.Errorf("log = %q, %v", data, err)
	}
	if err := p.AppendCallLog("survey-../x", "stdout", "x"); err == nil {
		t.Error("a call name with a path was accepted")
	}
	if err := p.AppendCallLog("specify", "../x", "x"); err == nil {
		t.Error("a stream with a path was accepted")
	}
}

func validRecord() StoryRecord {
	return StoryRecord{
		Slug:       "refunds",
		SpecDir:    "specs/refunds",
		JiraParent: "PAY-1",
		CreatedAt:  time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC),
		Services:   map[string]string{"billing": "/src/billing", "gateway": "/src/gateway"},
		Status:     story.StatusPlanned,
		Calls: map[string]*domain.Usage{
			"survey-billing": {InputTokens: 10, OutputTokens: 2, CostUSD: 0.01},
			"survey-gateway": nil,
			"specify":        {InputTokens: 30},
		},
	}
}

func TestStoryRecord_RenameSaveLoad(t *testing.T) {
	base := t.TempDir()
	p, err := NewStoryPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	if p.SlugTaken("refunds") {
		t.Fatal("SlugTaken before anything exists")
	}
	if err := p.Rename("Refunds!"); err == nil {
		t.Error("Rename accepted a bad slug")
	}
	if err := p.Rename("refunds"); err != nil {
		t.Fatal(err)
	}
	if p.Dir() != filepath.Join(base, "refunds") || !p.SlugTaken("refunds") {
		t.Fatalf("Dir() = %s", p.Dir())
	}
	want := validRecord()
	if err := p.SaveRecord(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStoryRecord(filepath.Join(p.Dir(), "story.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded\n%+v\nwant\n%+v", got, want)
	}

	// Another plan cannot take the slug: neither by Rename nor over it.
	other, err := NewStoryPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Rename("refunds"); err == nil {
		t.Error("Rename replaced an existing story directory")
	}
	if _, err := os.Stat(other.Dir()); err != nil {
		t.Errorf("the refused plan lost its directory: %v", err)
	}
}

func TestLoadStoryRecord_Refuses(t *testing.T) {
	cases := []struct {
		name   string
		change func(m map[string]any)
		want   string
	}{
		{"unknown key", func(m map[string]any) { m["bases"] = map[string]any{} }, "unknown field"},
		{"bad slug", func(m map[string]any) { m["slug"] = "../x" }, "slug"},
		{"absolute spec dir", func(m map[string]any) { m["spec_dir"] = "/specs/refunds" }, "spec_dir"},
		{"spec dir outside", func(m map[string]any) { m["spec_dir"] = "../refunds" }, "spec_dir"},
		{"spec dir of another slug", func(m map[string]any) { m["spec_dir"] = "specs/other" }, "spec_dir"},
		{"bad jira parent", func(m map[string]any) { m["jira_parent"] = "pay-1" }, "jira_parent"},
		{"no created_at", func(m map[string]any) { delete(m, "created_at") }, "created_at"},
		{"no services", func(m map[string]any) { m["services"] = map[string]any{} }, "services"},
		{"bad service name", func(m map[string]any) { m["services"] = map[string]any{"../x": "/x"} }, "services"},
		{"relative service path", func(m map[string]any) { m["services"] = map[string]any{"billing": "src/billing"} }, "services.billing"},
		{"bad status", func(m map[string]any) { m["status"] = "done" }, "status"},
		{"bad call", func(m map[string]any) { m["calls"] = map[string]any{"translate": nil} }, "calls"},
		{"negative usage", func(m map[string]any) {
			m["calls"] = map[string]any{"specify": map[string]any{"input_tokens": -1, "output_tokens": 0}}
		}, "calls.specify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(validRecord())
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			tc.change(m)
			if data, err = json.Marshal(m); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "story.json")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = LoadStoryRecord(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error about %q", err, tc.want)
			}
		})
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

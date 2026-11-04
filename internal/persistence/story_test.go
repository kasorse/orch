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
	"github.com/kasorse/orch/internal/story"
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
		{"unknown key", func(m map[string]any) { m["extra"] = map[string]any{} }, "unknown field"},
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

const (
	testSHA  = "8d0e4b2a1c9f2e7d3f2a9c1e0b4d6a8c5e7f9a1b"
	testSHA2 = "1c9f2e7d3f2a9c1e0b4d6a8c5e7f9a1b8d0e4b2a"
	testRun  = "3f2a9c1e-0b4d-4a8c-9e7f-9a1b8d0e4b2a"
)

// runningRecord is a record of a story under way: RF01 done, RF02 failed on its first attempt.
func runningRecord() StoryRecord {
	r := validRecord()
	approved := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	r.ApprovedAt = &approved
	r.Status = story.StatusFailed
	r.Bases = map[string]StoryBase{
		"billing": {Branch: "main", SHA: testSHA},
		"gateway": {Branch: "main", SHA: testSHA2},
	}
	hash := strings.Repeat("ab", 32)
	r.Tasks = map[string]StoryTask{
		"RF01": {Status: story.TaskDone, FileSHA256: hash, Attempt: 1, RunID: testRun, Repo: "billing",
			Branch: "story/refunds/RF01", BaseRef: testSHA, Commit: testSHA2, Jira: "PAY-2", CostUSD: 0.42},
		"RF02": {Status: story.TaskFailed, FileSHA256: hash, Attempt: 2, Repo: "gateway",
			Branch: "story/refunds/RF02.2", BaseRef: testSHA2, CostUSD: 0.1, AbandonedCostUSD: 0.3},
		"RF03": {Status: story.TaskPending},
	}
	return r
}

// A plan-time record (no approved_at, bases or tasks) still loads, and a full one round-trips.
func TestStoryRecord_SaveLoadFull(t *testing.T) {
	dir := t.TempDir()
	plain := validRecord()
	if err := SaveStoryRecord(dir, plain); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStoryRecord(filepath.Join(dir, "story.json"))
	if err != nil || !reflect.DeepEqual(got, plain) {
		t.Fatalf("plan-time record: %+v, %v", got, err)
	}

	want := runningRecord()
	if err := SaveStoryRecord(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err = LoadStoryRecord(filepath.Join(dir, "story.json"))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded\n%+v, %v\nwant\n%+v", got, err, want)
	}
	// The planning calls (0.01), RF01 (0.42) and RF02's run (0.1) and abandoned run (0.3).
	if cost := want.TotalCostUSD(); cost < 0.829 || cost > 0.831 {
		t.Errorf("TotalCostUSD = %v, want 0.83", cost)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("SaveStoryRecord left %d entries in the directory, want story.json alone", len(entries))
	}
}

func TestStoryRecord_Refuses(t *testing.T) {
	cases := []struct {
		name   string
		change func(r *StoryRecord)
		want   string
	}{
		{"base with a symbolic ref", func(r *StoryRecord) { r.Bases["billing"] = StoryBase{Branch: "main", SHA: "HEAD"} }, "bases.billing"},
		{"base of another service", func(r *StoryRecord) { r.Bases["web"] = StoryBase{Branch: "main", SHA: testSHA} }, "bases"},
		{"base branch as an option", func(r *StoryRecord) { r.Bases["billing"] = StoryBase{Branch: "-D", SHA: testSHA} }, "bases.billing"},
		{"task ID", func(r *StoryRecord) { r.Tasks["rf01"] = StoryTask{Status: story.TaskPending} }, "tasks"},
		{"task status", func(r *StoryRecord) { r.Tasks["RF03"] = StoryTask{Status: "queued"} }, "tasks.RF03"},
		{"non-SHA base_ref", func(r *StoryRecord) {
			t := r.Tasks["RF02"]
			t.BaseRef = "main"
			r.Tasks["RF02"] = t
		}, "base_ref"},
		{"branch of another task", func(r *StoryRecord) {
			t := r.Tasks["RF02"]
			t.Branch = "story/refunds/RF03"
			r.Tasks["RF02"] = t
		}, "branch"},
		{"branch of another attempt", func(r *StoryRecord) {
			t := r.Tasks["RF02"]
			t.Branch = "story/refunds/RF02"
			r.Tasks["RF02"] = t
		}, "branch"},
		{"branch of another story", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.Branch = "story/other/RF01"
			r.Tasks["RF01"] = t
		}, "branch"},
		{"bad run id", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.RunID = "../../x"
			r.Tasks["RF01"] = t
		}, "run_id"},
		{"bad file hash", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.FileSHA256 = "abc"
			r.Tasks["RF01"] = t
		}, "file_sha256"},
		{"repo that is no service", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.Repo = "web"
			r.Tasks["RF01"] = t
		}, "repo"},
		{"bad jira", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.Jira = "pay-2"
			r.Tasks["RF01"] = t
		}, "jira"},
		{"negative cost", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.CostUSD = -1
			r.Tasks["RF01"] = t
		}, "cost_usd"},
		{"done without a commit", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.Commit = ""
			r.Tasks["RF01"] = t
		}, "commit"},
		{"dispatched without an attempt", func(r *StoryRecord) {
			t := r.Tasks["RF02"]
			t.Attempt = 0
			t.Branch = ""
			r.Tasks["RF02"] = t
		}, "attempt"},
		{"dispatched without approval", func(r *StoryRecord) { r.ApprovedAt = nil }, "approved_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runningRecord()
			tc.change(&r)
			if err := r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error about %q", err, tc.want)
			}
			if err := SaveStoryRecord(t.TempDir(), r); err == nil {
				t.Error("SaveStoryRecord wrote a record Validate refuses")
			}
		})
	}

	// A key story run does not know is refused on load.
	dir := t.TempDir()
	if err := SaveStoryRecord(dir, runningRecord()); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	readJSON(t, filepath.Join(dir, "story.json"), &m)
	m["tasks"].(map[string]any)["RF01"].(map[string]any)["path"] = "/etc/passwd"
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "story.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStoryRecord(filepath.Join(dir, "story.json")); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("a task with an unknown key: %v", err)
	}
}

// A task's branch is the name its repository's rules gave or the story's own; its merge request and
// its repository's spec push are recorded and checked (specs/delivery/SPEC.md §9, §9.1, §13).
func TestStoryRecord_RuleBranchMergeRequestAndSpecPush(t *testing.T) {
	r := runningRecord()
	rf01 := r.Tasks["RF01"]
	rf01.Branch = "feature/PAY-2-store-the-amount"
	rf01.MR = &StoryMR{IID: 12, URL: "https://gitlab.example/g/p/-/merge_requests/12"}
	r.Tasks["RF01"] = rf01
	rf02 := r.Tasks["RF02"]
	rf02.Branch = "" // a running or failed task's branch is absent until its run is read
	r.Tasks["RF02"] = rf02
	b := r.Bases["billing"]
	b.SpecBranch = StorySpecBranch(r.Slug)
	b.SpecPush = &StorySpecPush{State: SpecPushPushed, SHA: testSHA2}
	r.Bases["billing"] = b

	dir := t.TempDir()
	if err := SaveStoryRecord(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStoryRecord(filepath.Join(dir, "story.json"))
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("loaded\n%+v, %v\nwant\n%+v", got, err, r)
	}

	cases := []struct {
		name   string
		change func(r *StoryRecord)
		want   string
	}{
		{"branch that git refuses", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.Branch = "feature/a b"
			r.Tasks["RF01"] = t
		}, "branch"},
		{"done without a branch", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.Branch = ""
			r.Tasks["RF01"] = t
		}, "no branch"},
		{"merge request with a URL on no merge request", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.MR = &StoryMR{IID: 12, URL: "https://gitlab.example/g/p/-/merge_requests/13"}
			r.Tasks["RF01"] = t
		}, "mr"},
		{"merge request without an iid", func(r *StoryRecord) {
			t := r.Tasks["RF01"]
			t.MR = &StoryMR{URL: "https://gitlab.example/g/p/-/merge_requests/12"}
			r.Tasks["RF01"] = t
		}, "mr"},
		{"merge request on a task that is not done", func(r *StoryRecord) {
			t := r.Tasks["RF02"]
			t.MR = &StoryMR{IID: 3, URL: "https://gitlab.example/g/p/-/merge_requests/3"}
			r.Tasks["RF02"] = t
		}, "merge request"},
		{"spec push with a bad state", func(r *StoryRecord) {
			b := r.Bases["billing"]
			b.SpecPush = &StorySpecPush{State: "done", SHA: testSHA}
			r.Bases["billing"] = b
		}, "spec_push"},
		{"spec push with a short sha", func(r *StoryRecord) {
			b := r.Bases["billing"]
			b.SpecPush = &StorySpecPush{State: SpecPushIntent, SHA: "HEAD"}
			r.Bases["billing"] = b
		}, "spec_push"},
		{"spec push with an error on two lines", func(r *StoryRecord) {
			b := r.Bases["billing"]
			b.SpecPush = &StorySpecPush{State: SpecPushIntent, SHA: testSHA, Error: "a\nb"}
			r.Bases["billing"] = b
		}, "spec_push"},
		{"spec push without a spec branch", func(r *StoryRecord) {
			b := r.Bases["billing"]
			b.SpecBranch = ""
			r.Bases["billing"] = b
		}, "spec_push"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := r
			bad.Tasks = map[string]StoryTask{}
			for k, v := range r.Tasks {
				bad.Tasks[k] = v
			}
			bad.Bases = map[string]StoryBase{}
			for k, v := range r.Bases {
				bad.Bases[k] = v
			}
			tc.change(&bad)
			if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error about %q", err, tc.want)
			}
		})
	}
}

func TestStoryTaskBranch(t *testing.T) {
	for attempt, want := range map[int]string{0: "story/refunds/RF02", 1: "story/refunds/RF02", 2: "story/refunds/RF02.2", 11: "story/refunds/RF02.11"} {
		if got := StoryTaskBranch("refunds", "RF02", attempt); got != want {
			t.Errorf("StoryTaskBranch(%d) = %s, want %s", attempt, got, want)
		}
		if !story.ValidBranch(want, "refunds", "RF02") {
			t.Errorf("%s is not a valid branch", want)
		}
	}
}

func TestStoryDir(t *testing.T) {
	dir, err := StoryDir("/home/op/platform", "refunds")
	if err != nil || dir != "/home/op/platform/.orchestrator/stories/refunds" {
		t.Errorf("StoryDir = %q, %v", dir, err)
	}
	for _, slug := range []string{"", "..", "../x", "Refunds", "a/b", ".plan-x"} {
		if _, err := StoryDir("/home", slug); err == nil {
			t.Errorf("StoryDir accepted the slug %q", slug)
		}
	}
}

func TestLockStory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stories", "refunds")
	unlock, err := LockStory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("LockStory did not create the story directory: %v", err)
	}
	if _, err := LockStory(dir); err == nil || !strings.Contains(err.Error(), "being run by another orchestrator process") {
		t.Fatalf("a second holder: %v", err)
	}
	// The lock is on the directory: the files in it can be rewritten while it is held.
	if err := SaveStoryRecord(dir, validRecord()); err != nil {
		t.Errorf("SaveStoryRecord under the lock: %v", err)
	}
	unlock()
	unlock() // twice is harmless
	again, err := LockStory(dir)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
}

func TestWriteStoryTaskInput(t *testing.T) {
	dir := t.TempDir()
	in := story.TaskInput{
		Story: "refunds", Task: "RF02", Attempt: 1, Title: "Expose partial refunds", Branch: "story/refunds/RF02", BaseRef: testSHA,
		BaseBranch: "main", Acceptance: []string{"a"}, Text: "do it",
	}
	file, err := WriteStoryTaskInput(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(dir, "tasks", "RF02.json") {
		t.Errorf("path = %s", file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := story.ParseTaskInput(data)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("read back %+v, %v", got, err)
	}
	bad := in
	bad.BaseRef = "main"
	if _, err := WriteStoryTaskInput(dir, bad); err == nil {
		t.Error("an input the child would refuse was written")
	}
}

func TestReplaceFile_KeepsTheMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TASK.md")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", info.Mode(), err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Errorf("content = %q", data)
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

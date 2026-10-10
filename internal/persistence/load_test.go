package persistence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// fullRun saves a run with every field resume reads back populated, so a round trip
// through strict decoding covers the whole run.json shape.
func fullRun(t *testing.T, store *Store) *domain.Run {
	t.Helper()
	run, err := store.NewRun(domain.Task{Description: "fix the bug", CreatedAt: time.Now(),
		Source: &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "NEW-9", RequestedKey: "PMC-1",
			URL: "https://example.atlassian.net/browse/NEW-9", FetchedAt: time.Now(), Notes: true}})
	if err != nil {
		t.Fatal(err)
	}
	score, p, th := 0.4, 0.9, 0.5
	run.Decision = &domain.Decision{
		Source:       domain.DecisionSourceJev,
		ContextDepth: domain.ContextDepthOutcome{Selected: domain.ContextDepthProject, Probability: 0.8, Threshold: 0.5},
		Complexity: domain.ComplexityOutcome{
			Primary: domain.ComplexitySourceChoice,
			Sources: map[domain.ComplexitySource]domain.ComplexitySourceOutcome{
				domain.ComplexitySourceChoice: {Decision: domain.ComplexityDecision{
					Probabilities: map[domain.Complexity]float64{"simple": 0.2, "medium": 0.5, "complex": 0.3},
					Confidence:    0.5, Score: &score}, Selected: "medium"},
			},
			Selected: "medium", Agree: true,
		},
		Stages: domain.StageSelection{Stages: []domain.StageOutcome{
			{Stage: domain.StageDevelopment, Probability: &p, Threshold: &th, Enabled: true, Reason: "p >= threshold"},
		}},
	}
	run.Workflow = &domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement, Stage: domain.StageDevelopment},
		{Action: domain.ActionTest, Stage: domain.StageTesting},
		{Action: domain.ActionReview, Stage: domain.StageReview},
	}}
	run.Mode = domain.ModeManual
	run.PreRunRef = "0123456789abcdef0123456789abcdef01234567"
	run.HeadRef = run.PreRunRef
	run.ResumeBaseline = json.RawMessage(`{"git_fingerprint":"ab","protected":{}}`)
	run.Executions = []domain.StepExecution{
		{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted,
			Provider:     &domain.ProviderRef{Provider: "claude", Model: "sonnet", Authority: domain.AuthorityEdit},
			ArtifactPath: ArtifactRelPath(0, domain.ActionImplement),
			Usage:        &domain.Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 300, CostUSD: 0.42, Turns: 3},
			VerifyCalls: []domain.VerifyCall{
				{Tool: domain.VerifyToolRunTests, Filter: "./internal/x", ExitCode: 1, Duration: 4 * time.Second, OutputCut: true},
				{Tool: domain.VerifyToolRunCheck, Check: "vet", Success: true, Duration: time.Second},
				{Tool: domain.VerifyToolRunCheck, Refused: "invalid arguments: name is required"},
			}},
		{Index: 1, Action: domain.ActionTest, Status: domain.StepCompleted,
			TestResult:   &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "FAIL", Duration: time.Second},
			ArtifactPath: ArtifactRelPath(1, domain.ActionTest)},
		{Index: 2, Action: domain.ActionImplement, Status: domain.StepFailed, Error: "timeout", Attempt: 1,
			Provider: &domain.ProviderRef{Provider: "claude", Model: "sonnet", Authority: domain.AuthorityEdit}},
		{Index: 3, Action: domain.ActionReview, Status: domain.StepCompleted, Attempt: 1,
			ReviewResult: &domain.ReviewResult{Status: domain.ReviewFail, Issues: []domain.ReviewIssue{{Severity: domain.SeverityHigh, File: "a.go"}}}},
	}
	run.Resumes = []domain.Resume{{At: time.Now(), FromStatus: domain.RunFailed, FromReason: "timeout",
		Entry: domain.ActionImplement, Why: "retry implement", Mode: domain.ModeAuto}}
	run.Status = domain.RunFailed
	run.FailureReason = "boom"
	run.TotalSteps = 4
	run.FixIterations = 1
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	return run
}

func assertRunStateKind(t *testing.T, err error, want apperr.RunStateKind) {
	t.Helper()
	var rse *apperr.RunStateError
	if !errors.As(err, &rse) {
		t.Fatalf("want RunStateError{%s}, got %T: %v", want, err, err)
	}
	if rse.Kind != want {
		t.Fatalf("want kind %s, got %s: %v", want, rse.Kind, err)
	}
}

func TestLoadRun_RoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	saved := fullRun(t, store)

	loaded, err := store.LoadRun(saved.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	// Normalise times (monotonic clock readings do not survive JSON).
	want, _ := json.Marshal(saved)
	got, _ := json.Marshal(loaded)
	if string(want) != string(got) {
		t.Errorf("round trip differs:\nwant %s\ngot  %s", want, got)
	}
}

func TestLoadRun_NewRunIsLoadable(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if loaded.Status != domain.RunPending || len(loaded.Executions) != 0 {
		t.Errorf("unexpected run: %+v", loaded)
	}
}

// editRunJSON rewrites run.json through a generic map, the way a hand edit or a model's
// write would.
func editRunJSON(t *testing.T, store *Store, id string, edit func(m map[string]any)) {
	t.Helper()
	path := filepath.Join(store.runDir(id), "run.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func execAt(m map[string]any, i int) map[string]any {
	return m["executions"].([]any)[i].(map[string]any)
}

func sourceOf(m map[string]any) map[string]any {
	return m["task"].(map[string]any)["source"].(map[string]any)
}

func verdict(index int, evidence string) map[string]any {
	return map[string]any{"index": index, "criterion": "c", "met": false, "evidence": evidence}
}

// setCriteria puts verdicts on the run's review result (execution 3 of fullRun).
func setCriteria(m map[string]any, verdicts ...map[string]any) {
	list := make([]any, len(verdicts))
	for i, v := range verdicts {
		list[i] = v
	}
	execAt(m, 3)["review_result"].(map[string]any)["criteria"] = list
}

// A review result with well-formed criteria loads and keeps them; one written before the
// field existed (fullRun's) loads without.
func TestLoadRun_ReviewCriteria(t *testing.T) {
	store := NewStore(t.TempDir())
	run := fullRun(t, store)
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("a review result without criteria: %v", err)
	}
	if got := loaded.Executions[3].ReviewResult.Criteria; got != nil {
		t.Errorf("criteria = %+v, want none", got)
	}

	editRunJSON(t, store, run.ID, func(m map[string]any) {
		setCriteria(m, verdict(2, "b.go:2"), verdict(1, "a.go:1"))
	})
	loaded, err = store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("a review result with criteria: %v", err)
	}
	if got := loaded.Executions[3].ReviewResult; len(got.Criteria) != 2 || got.Decide() != domain.ReviewFail {
		t.Errorf("review result = %+v", got)
	}
}

func TestLoadRun_RefusesCorruptState(t *testing.T) {
	cases := []struct {
		name string
		edit func(m map[string]any)
		want string
	}{
		{"unknown top-level field", func(m map[string]any) { m["extra"] = 1 }, "unknown field"},
		{"unknown nested field", func(m map[string]any) { execAt(m, 0)["extra"] = 1 }, "unknown field"},
		{"id mismatch", func(m map[string]any) { m["id"] = "00000000-0000-4000-8000-000000000000" }, "id is"},
		{"unknown status", func(m map[string]any) { m["status"] = "paused" }, "unknown status"},
		{"unknown mode", func(m map[string]any) { m["mode"] = "yolo" }, "unknown mode"},
		{"unknown workflow action", func(m map[string]any) {
			m["workflow"].(map[string]any)["steps"].([]any)[0].(map[string]any)["action"] = "deploy"
		}, "workflow step 0"},
		{"index gap", func(m map[string]any) { execAt(m, 1)["index"] = 2 }, "executions[1]: index is 2"},
		{"running execution", func(m map[string]any) { execAt(m, 3)["status"] = "running" }, "executions[3]: status"},
		{"unknown execution action", func(m map[string]any) { execAt(m, 0)["action"] = "deploy" }, "executions[0]: unknown action"},
		{"unknown authority", func(m map[string]any) {
			execAt(m, 0)["provider"].(map[string]any)["authority"] = "root"
		}, "unknown authority"},
		{"negative usage counter", func(m map[string]any) {
			execAt(m, 0)["usage"].(map[string]any)["output_tokens"] = -1
		}, "executions[0]: usage: negative counter"},
		{"negative cost", func(m map[string]any) { execAt(m, 0)["usage"].(map[string]any)["cost_usd"] = -0.5 }, "usage: cost_usd"},
		{"rate limits in usage", func(m map[string]any) { execAt(m, 0)["usage"].(map[string]any)["limits"] = map[string]any{} }, "unknown field"},
		{"artifact path traversal", func(m map[string]any) { execAt(m, 0)["artifact_path"] = "../../../etc/passwd" }, "artifact_path"},
		{"artifact path of another step", func(m map[string]any) {
			execAt(m, 0)["artifact_path"] = ArtifactRelPath(1, domain.ActionTest)
		}, "artifact_path"},
		{"unknown resume status", func(m map[string]any) {
			m["resumes"].([]any)[0].(map[string]any)["from_status"] = "odd"
		}, "resumes[0]"},
		{"criterion index out of range", func(m map[string]any) {
			setCriteria(m, verdict(1, "a.go:1"), verdict(3, "b.go:2"))
		}, "review_result.criteria"},
		{"duplicate criterion index", func(m map[string]any) {
			setCriteria(m, verdict(1, "a.go:1"), verdict(1, "b.go:2"))
		}, "more than once"},
		{"criterion evidence too long", func(m map[string]any) {
			setCriteria(m, verdict(1, strings.Repeat("x", domain.MaxCriterionEvidenceBytes+1)))
		}, "evidence"},
		{"criterion without evidence", func(m map[string]any) { setCriteria(m, verdict(1, "")) }, "no evidence"},
		{"review result on another step", func(m map[string]any) {
			execAt(m, 0)["review_result"] = execAt(m, 3)["review_result"]
		}, "review_result recorded on step action implement"},
		{"unknown task source field", func(m map[string]any) { sourceOf(m)["extra"] = 1 }, "unknown field"},
		{"task source kind", func(m map[string]any) { sourceOf(m)["kind"] = "github" }, `task.source: unknown kind "github"`},
		{"task source kind empty", func(m map[string]any) { sourceOf(m)["kind"] = "" }, "task.source: unknown kind"},
		{"task source lower-case key", func(m map[string]any) { sourceOf(m)["key"] = "pmc-1" },
			`task.source: key "pmc-1" is not an issue key`},
		{"task source path key", func(m map[string]any) { sourceOf(m)["key"] = "../x" }, "is not an issue key"},
		{"task source requested key", func(m map[string]any) { sourceOf(m)["requested_key"] = "PMC 1" },
			`task.source: requested_key "PMC 1" is not an issue key`},
		{"task source url", func(m map[string]any) { sourceOf(m)["url"] = "not a url" },
			`task.source: url "not a url" is not an absolute http(s) URL`},
		{"task source url scheme", func(m map[string]any) { sourceOf(m)["url"] = "file:///etc/passwd" }, "task.source: url"},
		{"task source zero fetched_at", func(m map[string]any) { sourceOf(m)["fetched_at"] = time.Time{} },
			"task.source: fetched_at is not set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(t.TempDir())
			run := fullRun(t, store)
			editRunJSON(t, store, run.ID, tc.edit)

			_, err := store.LoadRun(run.ID)
			assertRunStateKind(t, err, apperr.RunCorrupt)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A run.json written before tasks had a source (a typed task, or any run from before
// specs/jira) still loads, and has no source.
func TestLoadRun_WithoutTaskSource(t *testing.T) {
	store := NewStore(t.TempDir())
	id := "0f8fad5b-d9cb-469f-a165-70867728950e"
	dir := store.runDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "id": "` + id + `",
  "task": {"description": "fix the bug", "created_at": "2026-09-01T10:00:00Z"},
  "status": "failed",
  "executions": [],
  "created_at": "2026-09-01T10:00:00Z",
  "updated_at": "2026-09-01T10:05:00Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := store.LoadRun(id)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Task.Description != "fix the bug" || run.Task.Source != nil {
		t.Errorf("task = %+v", run.Task)
	}
}

func TestLoadRun_RefusesTrailingData(t *testing.T) {
	store := NewStore(t.TempDir())
	run := fullRun(t, store)
	path := filepath.Join(store.runDir(run.ID), "run.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"x"}`)
	f.Close()

	_, err = store.LoadRun(run.ID)
	assertRunStateKind(t, err, apperr.RunCorrupt)
}

func TestLoadRun_NotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.LoadRun("00000000-0000-4000-8000-000000000000")
	assertRunStateKind(t, err, apperr.RunNotFound)

	_, err = store.LoadRun("../x")
	assertRunStateKind(t, err, apperr.RunNotFound)
}

func TestResolveRunID(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	ids := []string{
		"3f2a9c1e-0000-4000-8000-000000000001",
		"3f2a9c1e-0000-4000-8000-000000000002",
		"7b6d1f00-0000-4000-8000-000000000003",
	}
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(base, id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, id, "run.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Not a run directory: must never be matched by a prefix.
	os.MkdirAll(filepath.Join(base, "7b6d1f00-not-a-run"), 0o755)

	t.Run("exact", func(t *testing.T) {
		got, err := store.ResolveRunID(ids[0])
		if err != nil || got != ids[0] {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("exact uppercase", func(t *testing.T) {
		got, err := store.ResolveRunID(strings.ToUpper(ids[2]))
		if err != nil || got != ids[2] {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("unique prefix", func(t *testing.T) {
		got, err := store.ResolveRunID("7b6d1f00")
		if err != nil || got != ids[2] {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("longer unique prefix", func(t *testing.T) {
		got, err := store.ResolveRunID(ids[2][:20])
		if err != nil || got != ids[2] {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("prefix one short of the full ID is still ambiguous", func(t *testing.T) {
		_, err := store.ResolveRunID(ids[1][:35])
		assertRunStateKind(t, err, apperr.RunAmbiguous)
	})
	t.Run("ambiguous lists matches", func(t *testing.T) {
		_, err := store.ResolveRunID("3f2a9c1e")
		assertRunStateKind(t, err, apperr.RunAmbiguous)
		for _, id := range ids[:2] {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("error does not list %s: %v", id, err)
			}
		}
	})
	t.Run("too short", func(t *testing.T) {
		_, err := store.ResolveRunID("3f2a9c1")
		assertRunStateKind(t, err, apperr.RunNotFound)
		if !strings.Contains(err.Error(), "at least 8") {
			t.Errorf("missing hint: %v", err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		_, err := store.ResolveRunID("deadbeef")
		assertRunStateKind(t, err, apperr.RunNotFound)
	})
	t.Run("unknown full id", func(t *testing.T) {
		_, err := store.ResolveRunID("deadbeef-0000-4000-8000-000000000000")
		assertRunStateKind(t, err, apperr.RunNotFound)
	})
	for _, bad := range []string{"", "../../etc", "3f2a9c1e/..", "3f2a9c1e-0000-4000-8000-00000000000z", " "} {
		t.Run("rejects "+bad, func(t *testing.T) {
			_, err := store.ResolveRunID(bad)
			assertRunStateKind(t, err, apperr.RunNotFound)
		})
	}
}

func TestResolveRunID_NoRunsDirectory(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "missing"))
	_, err := store.ResolveRunID("3f2a9c1e")
	assertRunStateKind(t, err, apperr.RunNotFound)
}

func TestIsRunID(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !isRunID(run.ID) {
		t.Errorf("isRunID(%q) = false for a generated ID", run.ID)
	}
	for _, bad := range []string{"", "3f2a9c1e", strings.ToUpper(run.ID), strings.Replace(run.ID, "-", "_", 1), run.ID + "0"} {
		if isRunID(bad) {
			t.Errorf("isRunID(%q) = true", bad)
		}
	}
}

func TestArtifactRelPath_MatchesSaveArtifact(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.SaveArtifact(run.ID, 7, domain.ActionPlan, map[string]string{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := ArtifactRelPath(7, domain.ActionPlan); got != want {
		t.Errorf("SaveArtifact wrote %q, ArtifactRelPath says %q", got, want)
	}
	if filepath.ToSlash(got) != "artifacts/007-plan.json" {
		t.Errorf("unexpected path %q", got)
	}
}

func TestRunIDsAndLoadDecision(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	if ids, err := store.RunIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("RunIDs of an empty store = %v, %v", ids, err)
	}
	if ids, err := NewStore(filepath.Join(base, "missing")).RunIDs(); err != nil || ids != nil {
		t.Fatalf("RunIDs without a runs directory = %v, %v", ids, err)
	}

	decided, _ := store.NewRun(domain.Task{Description: "fix"})
	undecided, _ := store.NewRun(domain.Task{Description: "fix"})
	tampered, _ := store.NewRun(domain.Task{Description: "fix"})
	if err := os.Mkdir(filepath.Join(base, "not-a-run"), 0o755); err != nil {
		t.Fatal(err)
	}
	trace := domain.DecisionTrace{Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev", Model: "jev-1.13.0"}
	if err := store.SaveDecision(decided.ID, trace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, tampered.ID, "decision.json"), []byte(`{"request":"fix","surprise":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ids, err := store.RunIDs()
	if err != nil || len(ids) != 3 || !sort.StringsAreSorted(ids) {
		t.Fatalf("RunIDs = %v, %v; want the three runs, sorted", ids, err)
	}

	got, at, err := store.LoadDecision(decided.ID)
	if err != nil || got.Model != "jev-1.13.0" || got.Source != domain.DecisionSourceJev || at.IsZero() {
		t.Errorf("LoadDecision = %+v, %v, %v", got, at, err)
	}
	withUsage := trace
	withUsage.Usage = &domain.DecisionUsage{
		Translation:    &domain.Usage{InputTokens: 5, OutputTokens: 6, CostUSD: 0.01},
		Classification: &domain.Usage{InputTokens: 1450, OutputTokens: 8},
	}
	if err := store.SaveDecision(undecided.ID, withUsage); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.LoadDecision(undecided.ID); err != nil || !reflect.DeepEqual(got.Usage, withUsage.Usage) {
		t.Errorf("LoadDecision with usage = %+v, %v", got.Usage, err)
	}
	if err := os.WriteFile(filepath.Join(base, undecided.ID, "decision.json"),
		[]byte(`{"request":"fix","usage":{"context_depth":{"input_tokens":-1,"output_tokens":0}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadDecision(undecided.ID); err == nil || !strings.Contains(err.Error(), "usage.context_depth: negative counter") {
		t.Errorf("a negative decision usage must be refused, got %v", err)
	}
	if err := os.Remove(filepath.Join(base, undecided.ID, "decision.json")); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]apperr.RunStateKind{undecided.ID: apperr.RunNotFound, tampered.ID: apperr.RunCorrupt, "../x": apperr.RunNotFound} {
		_, _, err := store.LoadDecision(id)
		var rse *apperr.RunStateError
		if !errors.As(err, &rse) || rse.Kind != want {
			t.Errorf("LoadDecision(%s) = %v, want %s", id, err, want)
		}
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/persistence"
)

// resultFields is the field set of the result line, in order: a change to it is an incompatible
// change of the interface (specs/delivery/SPEC.md §11.2).
var resultFields = []string{"v", "run_id", "status", "exit_code", "failure_reason", "error", "workflow", "commit", "branch",
	"mr", "mr_skipped", "mr_error", "review_verdict", "tests_passed_final", "approval_required", "cost_usd", "cost_partial", "run_dir", "report"}

// pipeStdin is a stdin that is no terminal, as in CI.
func pipeStdin(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r
}

// resultLine checks that stdout holds exactly one line of JSON with the stable field set, and
// returns it decoded.
func resultLine(t *testing.T, stdout string) map[string]any {
	t.Helper()
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("stdout is not exactly one line: %q", stdout)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%q", err, stdout)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := slices.Sorted(slices.Values(resultFields))
	if !slices.Equal(keys, want) {
		t.Errorf("fields = %v, want %v", keys, want)
	}
	return m
}

// A run, resume or review that would ask a question with nobody to answer it is a usage error at
// once: before the config is read, so before anything is called.
func TestRun_NoTerminalNeedsAMode(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	for _, args := range [][]string{
		{"run", "fix it"},
		{"--mode", "manual", "run", "fix it"},
		{"resume", "20261023-101500-ab12cd"},
		{"--mode=manual", "resume", "20261023-101500-ab12cd"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), append([]string{"--config", missing}, args...), pipeStdin(t), &stdout, &stderr)
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) || err.Error() != noTerminalMessage || exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Errorf("Run = %v, want the usage error %q (exit 2)", err, noTerminalMessage)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("output on stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

// A mode that asks nothing is not a usage error: the run goes on to its own checks.
func TestRun_NoTerminalWithAModeThatAsksNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	for _, mode := range []string{"auto", "plan", "accept-edits"} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"--config", missing, "--mode", mode, "run", "fix it"}, pipeStdin(t), &stdout, &stderr)
		if err == nil || err.Error() == noTerminalMessage || !strings.Contains(err.Error(), "failed to read config file") {
			t.Errorf("--mode %s: Run = %v, want the config error: the run got past the terminal check", mode, err)
		}
	}
}

// With --output json the usage error and a config error before the run are each one line on
// stdout. The message itself is main's to print on stderr; what Run writes there is the progress
// of the config chain, if it got that far, and never anything on stdout.
func TestRun_OutputJSONErrorBeforeTheRun(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	cases := []struct {
		name   string
		args   []string
		in     func(*testing.T) *os.File
		want   string
		stderr string
	}{
		{"no terminal", []string{"--output", "json", "run", "fix it"}, pipeStdin, noTerminalMessage, ""},
		{"config error", []string{"--output=json", "--mode", "auto", "run", "fix it"}, pipeStdin, "failed to read config file", "Validating configuration...\n"},
		{"usage error of the command", []string{"--output", "json", "--mode", "auto", "run"}, pipeStdin, "usage: orchestrator run", ""},
		{"resume", []string{"--output", "json", "--mode", "auto", "resume", "20261023-101500-ab12cd"}, pipeStdin, "failed to read config file", "Validating configuration...\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), append([]string{"--config", missing}, c.args...), c.in(t), &stdout, &stderr)
			if err == nil || exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Fatalf("Run = %v, want a config error (exit 2)", err)
			}
			m := resultLine(t, stdout.String())
			if m["v"] != float64(1) || m["run_id"] != "" || m["status"] != "error" || m["exit_code"] != float64(2) ||
				!strings.Contains(m["error"].(string), c.want) {
				t.Errorf("line = %v, want an error line with %q", m, c.want)
			}
			if stderr.String() != c.stderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), c.stderr)
			}
		})
	}
}

// --output json is for run, resume and review, and takes only json; the line is never written for
// a command it does not belong to.
func TestRun_OutputJSONWhereItIsNotValid(t *testing.T) {
	for _, args := range [][]string{{"--output", "json", "version"}, {"--output", "json"}, {"--output", "xml", "run", "x"}, {"--output"}, {"--output", "json", "--output", "json", "run", "x"}} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), args, pipeStdin(t), &stdout, &stderr)
		if exitcode.FromError(err) != exitcode.ConfigOrDependency || stdout.Len() != 0 {
			t.Errorf("Run(%v) = %v, stdout %q; want a usage error and nothing on stdout", args, err, stdout.String())
		}
	}
}

func TestParseArgs_Output(t *testing.T) {
	got, err := parseArgs([]string{"run", "--output=json", "x"})
	if err != nil || got.output != "json" || !slices.Equal(got.rest, []string{"run", "x"}) {
		t.Errorf("parseArgs = %+v, %v", got, err)
	}
}

func resultsRun(t *testing.T) (*persistence.Store, *domain.Run) {
	t.Helper()
	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix it"})
	if err != nil {
		t.Fatal(err)
	}
	return store, run
}

// The line of a completed run, with the run ID, the commit and the runs directory masked, is a
// golden: the field set and the order are the interface.
func TestBuildResult_Golden(t *testing.T) {
	store, run := resultsRun(t)
	passed := true
	run.Status = domain.RunCompleted
	run.Workflow = &domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}}
	run.Commit = "0123456789abcdef0123456789abcdef01234567"
	run.Worktree = &domain.RunWorktree{Branch: domain.WorktreeBranch(run.ID)}
	run.MR = &domain.RunMR{State: domain.MRCreated, Remote: "origin", Source: run.Worktree.Branch, Target: "main", IID: 12,
		URL: "https://gitlab.example.com/group/app/-/merge_requests/12"}
	run.Outcome = &domain.Outcome{Status: domain.RunCompleted, TestsPassedFinal: &passed, ReviewVerdict: domain.ReviewPass,
		Usage: domain.OutcomeUsage{Usage: domain.Usage{CostUSD: 0.84}, Calls: 3}}
	if err := store.SaveReport(run.ID, "# report\n"); err != nil {
		t.Fatal(err)
	}

	mask := func(s string) string {
		s = strings.ReplaceAll(s, filepath.Dir(store.RunDir(run.ID)), ".orchestrator/runs")
		s = strings.ReplaceAll(s, run.ID, "<run-id>")
		return strings.ReplaceAll(s, run.Commit, "<sha>")
	}
	var stdout bytes.Buffer
	writeResult(&stdout, &resultRecorder{run: run, store: store}, nil)
	assertGolden(t, "result_completed.golden", mask(stdout.String()))
	resultLine(t, stdout.String())

	// No run: the error line, with the characters JSON would escape in HTML left as they are.
	stdout.Reset()
	writeResult(&stdout, &resultRecorder{}, &apperr.ConfigError{Msg: `a worktree run needs <remote> & a "target"`})
	assertGolden(t, "result_error.golden", stdout.String())
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s\n got: %s\nwant: %s", name, got, want)
	}
}

// A run that failed is a line with its status, reason and exit code, and no merge request.
func TestBuildResult_FailedRun(t *testing.T) {
	store, run := resultsRun(t)
	run.Status, run.FailureReason = domain.RunFailed, "tests failed after 3 fix iterations"
	err := &apperr.WorkflowError{Msg: run.FailureReason}

	var stdout bytes.Buffer
	writeResult(&stdout, &resultRecorder{run: run, store: store}, err)
	m := resultLine(t, stdout.String())
	if m["run_id"] != run.ID || m["status"] != "failed" || m["exit_code"] != float64(4) || m["failure_reason"] != run.FailureReason ||
		m["error"] != run.FailureReason || m["mr"] != nil || m["report"] != "" {
		t.Errorf("line = %v", m)
	}
	if m["run_dir"] != store.RunDir(run.ID) {
		t.Errorf("run_dir = %v, want %s", m["run_dir"], store.RunDir(run.ID))
	}

	// An error after the run was loaded that is not how it ended is an error, not the old status.
	stdout.Reset()
	run.Status = domain.RunCompleted
	writeResult(&stdout, &resultRecorder{run: run, store: store}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID, Msg: "run.json"})
	if m := resultLine(t, stdout.String()); m["status"] != "error" || m["exit_code"] != float64(1) {
		t.Errorf("line = %v", m)
	}
}

var passingReview = &domain.ReviewResult{Status: domain.ReviewPass, Summary: "fine", SummaryShort: "fine"}

// A worktree run that opened its merge request: exit 0, the merge request, the commit and the
// branch.
func TestBuildResult_MergeRequestCreated(t *testing.T) {
	s, _, out := finishedWorktreeRun(t, domain.ModeAuto, passingReview, nil)
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)
	if sha == "" || err != nil {
		t.Fatalf("commit %q, finishWorktree = %v\n%s", sha, err, out)
	}

	var stdout bytes.Buffer
	writeResult(&stdout, &resultRecorder{run: s.run, store: s.store}, err)
	m := resultLine(t, stdout.String())
	mr, _ := m["mr"].(map[string]any)
	if m["status"] != "completed" || m["exit_code"] != float64(0) || m["commit"] != sha || m["branch"] != s.worktree.Branch ||
		mr["iid"] != float64(1) || mr["url"] != "https://gitlab.test/group/project/-/merge_requests/1" ||
		m["mr_skipped"] != "" || m["mr_error"] != "" || m["review_verdict"] != "pass" {
		t.Errorf("line = %v", m)
	}
}

// A completed run whose commit was not made (nobody to ask) is exit 6 with the reason in
// mr_skipped, and a push the remote refused is exit 6 with the error in mr_error: the status stays
// completed.
func TestBuildResult_NeedsAPerson(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		s, _, out := finishedWorktreeRun(t, domain.ModeAcceptEdits, passingReview, nil)
		ctx := context.Background()
		err := finishWorktree(ctx, s, offerCommit(ctx, s, false))
		if exitcode.FromError(err) != exitcode.NeedsPerson {
			t.Fatalf("finishWorktree = %v, want exit 6\n%s", err, out)
		}
		var stdout bytes.Buffer
		writeResult(&stdout, &resultRecorder{run: s.run, store: s.store}, err)
		m := resultLine(t, stdout.String())
		if m["status"] != "completed" || m["exit_code"] != float64(6) || m["mr"] != nil || m["commit"] != "" ||
			m["mr_skipped"] == "" || m["mr_error"] != "" || !strings.Contains(m["error"].(string), "nothing was delivered") {
			t.Errorf("line = %v", m)
		}
	})
	t.Run("error", func(t *testing.T) {
		s, dir, out := finishedWorktreeRun(t, domain.ModeAuto, passingReview, nil)
		gitIn(t, remoteDir(t, dir), "config", "receive.advertisePushOptions", "false")
		ctx := context.Background()
		sha := offerCommit(ctx, s, false)
		err := finishWorktree(ctx, s, sha)
		if exitcode.FromError(err) != exitcode.NeedsPerson {
			t.Fatalf("finishWorktree = %v, want exit 6\n%s", err, out)
		}
		var stdout bytes.Buffer
		writeResult(&stdout, &resultRecorder{run: s.run, store: s.store}, err)
		m := resultLine(t, stdout.String())
		if m["status"] != "completed" || m["exit_code"] != float64(6) || m["mr"] != nil || m["commit"] != sha ||
			m["mr_skipped"] != "" || !strings.Contains(m["mr_error"].(string), "push options") {
			t.Errorf("line = %v", m)
		}
	})
}

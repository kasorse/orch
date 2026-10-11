package action

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
)

// treeGit answers the git calls of a retried implement: write-tree gives a tree, a numstat
// gives numstat or numstatErr, and it records the numstat's argv.
type treeGit struct {
	numstat    string
	numstatErr error
	numstatArg []string
}

func (g *treeGit) Run(_ context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	switch {
	case slices.Contains(spec.Args, "write-tree"):
		return execution.ProcessResult{Stdout: "tree-now\n"}, nil
	case slices.Contains(spec.Args, "--numstat"):
		g.numstatArg = spec.Args
		return execution.ProcessResult{Stdout: g.numstat}, g.numstatErr
	}
	return execution.ProcessResult{}, nil
}

func retryDispatcher(git *treeGit, output string) (*Dispatcher, *fakeClient) {
	client := &fakeClient{output: output}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	d := New(testConfig(), registry, &repository.Repository{Dir: "/repo", Executor: git}, nil)
	d.Guard = &fakeGuard{}
	d.lastImpl = &domain.ImplementationArtifact{Summary: "first try"}
	return d, client
}

func TestExecuteImplement_RetryGetsTheRunsDiffStat(t *testing.T) {
	git := &treeGit{numstat: "3\t1\tinternal/a.go\x0012\t0\tinternal/a_test.go\x00"}
	d, client := retryDispatcher(git, `{"summary":"second try"}`)
	d.RunsDir = ".orchestrator/runs"
	d.pendingTestFailure = &domain.TestResult{Command: "go test", ExitCode: 1, Stdout: "FAIL"}

	run := &domain.Run{ID: "r", PreRunRef: "pre-run-ref", Task: domain.Task{Description: "fix it"}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}

	prompt := client.lastReq.Prompt
	want := " internal/a.go      |  4 +++-\n internal/a_test.go | 12 ++++++++++++\n 2 files changed, 15 insertions(+), 1 deletion(-)\n"
	var stat string
	for _, block := range fenced(t, prompt) {
		if strings.Contains(block, "files changed") {
			stat = block
		}
	}
	if stat != want {
		t.Errorf("diff stat block = %q, want %q", stat, want)
	}
	if !strings.Contains(prompt, "What the run has changed so far (git diff --stat against the run's start):\n") ||
		!strings.Contains(prompt, "<<<UNTRUSTED-DIFFSTAT-") {
		t.Errorf("the stat is not a fenced diffstat block:\n%s", prompt)
	}
	if !slices.Contains(git.numstatArg, "pre-run-ref") || !slices.Contains(git.numstatArg, "tree-now") ||
		!slices.Contains(git.numstatArg, ":(exclude).orchestrator/runs") {
		t.Errorf("numstat argv = %q, want the run's start, the current tree and the runs directory excluded", git.numstatArg)
	}
}

func TestExecuteImplement_RetryWithoutAStatStillRuns(t *testing.T) {
	git := &treeGit{numstatErr: errors.New("bad object")}
	d, client := retryDispatcher(git, `{"summary":"second try"}`)
	var warned []string
	d.OnWarning = func(step domain.ActionType, msg string) {
		if step != domain.ActionImplement {
			t.Errorf("warning for %s, want implement", step)
		}
		warned = append(warned, msg)
	}

	run := &domain.Run{ID: "r", PreRunRef: "pre-run-ref", Task: domain.Task{Description: "fix it"}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatalf("a missing stat must not fail the step: %v", err)
	}

	if strings.Contains(client.lastReq.Prompt, "DIFFSTAT") || strings.Contains(client.lastReq.Prompt, "What the run has changed") {
		t.Errorf("no stat, no section:\n%s", client.lastReq.Prompt)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "diff stat") || !strings.Contains(warned[0], "bad object") {
		t.Errorf("warnings = %q, want one naming the diff stat and the cause", warned)
	}
}

func TestExecuteImplement_FirstAttemptComputesNoStat(t *testing.T) {
	git := &treeGit{numstat: "1\t0\ta.go\x00"}
	d, client := retryDispatcher(git, `{"summary":"done"}`)
	d.lastImpl = nil
	d.OnWarning = func(domain.ActionType, string) { t.Error("no warning expected") }

	run := &domain.Run{ID: "r", PreRunRef: "pre-run-ref", Task: domain.Task{Description: "fix it"}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.lastReq.Prompt, "DIFFSTAT") {
		t.Errorf("the first attempt must not carry a stat:\n%s", client.lastReq.Prompt)
	}
}

func TestDispatcherWarn_IsNilSafe(t *testing.T) {
	(&Dispatcher{}).warn(domain.ActionImplement, "x")
}

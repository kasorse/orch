package logging

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// prettyRun renders a whole run the way the terminal shows it. ORCHESTRATOR_DEMO=1 prints
// it, so `ORCHESTRATOR_DEMO=1 go test ./internal/logging -run TestPretty_FullRun -v` is a
// quick look at the renderer.
func prettyRun(t *testing.T) string {
	t.Helper()
	var buf syncBuffer
	r := &ConsoleReporter{Out: &buf, Pretty: true, Version: "0.1.0", Cwd: "/work/shop", TestCommand: "npm test", MaxFixIterations: 2}

	r.ConfigValid()
	r.RunHeader("20261002-120000-ab12cd34", "/work/shop/.orchestrator/runs/20261002-120000-ab12cd34")
	r.Deciding()
	trace := domain.DecisionTrace{
		Provider: "claude", Model: "opus", Source: "provider",
		Translation:  domain.TranslationTrace{Detected: "ru", Translated: true, Provider: "claude", Model: "haiku", LatencyMS: 1400},
		ContextDepth: &domain.ContextDepthTrace{ContextDepthOutcome: domain.ContextDepthOutcome{Selected: "project", Probability: 0.91, Threshold: 0.5}},
		Complexity: domain.ComplexityOutcome{Selected: domain.Complexity("medium"), Primary: domain.ComplexitySourceChoice,
			Sources: map[domain.ComplexitySource]domain.ComplexitySourceOutcome{
				domain.ComplexitySourceChoice: {Selected: "medium", Decision: domain.ComplexityDecision{Probabilities: map[domain.Complexity]float64{"medium": 0.63}}},
			}},
		Stages: []domain.StageOutcome{
			{Stage: "research", Probability: ptr(0.05), Threshold: ptr(0.7)},
			{Stage: "investigation", Probability: ptr(0.92), Threshold: ptr(0.6), Enabled: true},
			{Stage: "development", Probability: ptr(0.99), Threshold: ptr(0.6), Enabled: true},
			{Stage: "testing", Probability: ptr(0.95), Threshold: ptr(0.6), Enabled: true},
			{Stage: "review", Probability: ptr(0.55), Threshold: ptr(0.7), Enabled: true, Forced: true},
		},
		Workflow: []domain.ActionType{"investigate", "implement", "test", "review"},
	}
	r.Decided(trace)
	wf := domain.Workflow{Steps: []domain.Step{{Action: "investigate"}, {Action: "implement"}, {Action: "test"}, {Action: "review"}}}
	r.Authority(wf, "npm test", false)
	r.RestorePoint("3c6b929")

	run := &domain.Run{CreatedAt: time.Now().Add(-95 * time.Second)}
	start := time.Now()
	step := func(a domain.ActionType, outcome workflow.StepOutcome, exec domain.StepExecution) {
		idx := len(run.Executions)
		r.StepStarted(run, idx, a)
		if a != domain.ActionTest {
			r.ProviderCallStarted("claude", "sonnet")
			r.ProviderCallFinished("claude", "sonnet", time.Second, nil)
		}
		exec.Index, exec.Action, exec.Status = idx, a, domain.StepCompleted
		exec.StartedAt, exec.FinishedAt = start, start.Add(12300*time.Millisecond)
		if a != domain.ActionTest {
			exec.Provider = &domain.ProviderRef{Provider: "claude", Model: "sonnet"}
		}
		run.Executions = append(run.Executions, exec)
		run.TotalSteps++
		r.StepFinished(run, idx, exec, outcome)
	}
	step("investigate", workflow.StepOutcome{Outcome: workflow.OutcomeSuccess, Artifact: domain.InvestigationArtifact{
		Summary: "Email is not validated on signup.", AffectedFiles: []string{"src/signup.ts"}}}, domain.StepExecution{})
	step("implement", workflow.StepOutcome{Outcome: workflow.OutcomeSuccess, Artifact: domain.ImplementationArtifact{
		Summary: "Added validateEmail.", ChangedFiles: []string{"src/signup.ts", "src/signup.test.ts"}}}, domain.StepExecution{})
	step("test", workflow.StepOutcome{Outcome: workflow.OutcomeFailure}, domain.StepExecution{
		TestResult: &domain.TestResult{Command: "npm test", ExitCode: 1, Stdout: "FAIL src/signup.test.ts\n  expected true, got false"}})
	run.FixIterations++
	step("implement", workflow.StepOutcome{Outcome: workflow.OutcomeSuccess, Artifact: domain.ImplementationArtifact{Summary: "Fixed the regex."}}, domain.StepExecution{})
	step("test", workflow.StepOutcome{Outcome: workflow.OutcomeSuccess}, domain.StepExecution{
		TestResult: &domain.TestResult{Command: "npm test", Success: true}})
	step("review", workflow.StepOutcome{Outcome: workflow.OutcomeSuccess}, domain.StepExecution{
		ReviewResult: &domain.ReviewResult{Summary: "Looks good.", Issues: []domain.ReviewIssue{{Severity: domain.SeverityLow, File: "src/signup.ts", Description: "name the regex"}}}})
	run.Status = domain.RunCompleted
	r.RunFinished(run)

	out := buf.String()
	if os.Getenv("ORCHESTRATOR_DEMO") != "" {
		os.Stdout.WriteString("\n" + out + "\n")
	}
	return out
}

func TestPretty_FullRun(t *testing.T) {
	out := prettyRun(t)
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("pretty output has no colour")
	}
	plain := ansiRE.ReplaceAllString(out, "")
	for _, want := range []string{
		"✻ AI Orchestrator v0.1.0",
		"run   20261002-120000-ab12cd34",
		"⏺ Decision  claude · opus",
		"workflow    investigate → implement → test → review",
		"⏺ Investigate  step 1 · read access",
		"✓ Done (claude · sonnet · 12.3s)",
		"✗ Tests failed (exit 1 · 12.3s)",
		"↻ Returning to implement (fix 1/2)",
		"✓ Tests passed (npm test · 12.3s)",
		"✓ Run completed",
		"investigate ✓ → implement ✓ → test ✗ → implement ✓ → test ✓ → review ✓",
		"[low] src/signup.ts: name the regex",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in:\n%s", want, plain)
		}
	}
	// Every box line is the same width.
	var widths []int
	for _, l := range strings.Split(plain, "\n") {
		if strings.HasPrefix(l, "│") || strings.HasPrefix(l, "╭") || strings.HasPrefix(l, "╰") {
			widths = append(widths, visibleWidth(l))
		}
	}
	for _, w := range widths[1:] {
		if w != widths[0] && w != widths[len(widths)-1] {
			t.Errorf("box lines have different widths: %v", widths)
			break
		}
	}
}

func TestPlainReporterNeverEmitsANSI(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.RunHeader("id", "dir")
	r.Deciding()
	r.StepStarted(&domain.Run{}, 0, domain.ActionInvestigate)
	r.ProviderCallStarted("claude", "sonnet")
	r.RunFinished(&domain.Run{Status: domain.RunCompleted})
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("plain output contains escapes: %q", buf.String())
	}
}

func TestPrettyFor_NonTerminalIsPlain(t *testing.T) {
	if PrettyFor(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	if FormatError(&bytes.Buffer{}, errors.New("boom\ndetail")) != "boom\ndetail" {
		t.Error("errors off a terminal must be unchanged")
	}
}

// The decided-stage table marks an explored stage like a forced one.
func TestPretty_StageRowMarksExplored(t *testing.T) {
	var buf bytes.Buffer
	r := &ConsoleReporter{Out: &buf, Pretty: true}
	r.stageRow(domain.StageOutcome{Stage: "review", Probability: ptr(0.67), Threshold: ptr(0.7), Enabled: true, Explored: true,
		Reason: "explored (P 0.67, threshold 0.70)"})
	got := ansiRE.ReplaceAllString(buf.String(), "")
	if !strings.Contains(got, "0.67") || !strings.Contains(got, "on (explored)") {
		t.Errorf("explored stage row = %q, want the probability and \"on (explored)\"", got)
	}
}

func TestStylePrompt(t *testing.T) {
	r := &ConsoleReporter{Pretty: true}
	got := ansiRE.ReplaceAllString(r.stylePrompt("\nApprove this workflow? [a]pprove / [c]ancel: "), "")
	if got != "\n? Approve this workflow? [a]pprove / [c]ancel: " {
		t.Errorf("got %q", got)
	}
}

func TestSpinnerStopClearsLine(t *testing.T) {
	var buf syncBuffer
	s := startSpinner(&buf, style{on: true}, "Planning", "claude · sonnet")
	time.Sleep(3 * spinnerInterval)
	s.Stop()
	s.Stop() // idempotent
	out := buf.String()
	if !strings.Contains(out, "Planning…") || !strings.HasSuffix(out, "\r\x1b[K\r\x1b[?25h") {
		t.Errorf("unexpected spinner output %q", out)
	}
}

// syncBuffer is a bytes.Buffer safe for the spinner's goroutine and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestPretty_AuthorityWarnsWithoutSandbox(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	for _, tc := range []struct {
		sandboxed bool
		want      string
		not       string
	}{
		{true, "$ npm test (sandboxed)", "NOT sandboxed"},
		{false, "NOT sandboxed: network, $HOME and your files are reachable", "(sandboxed)"},
	} {
		var buf bytes.Buffer
		r := &ConsoleReporter{Out: &buf, Pretty: true}
		r.Authority(wf, "npm test", tc.sandboxed)
		if out := buf.String(); !strings.Contains(out, tc.want) || strings.Contains(out, tc.not) {
			t.Errorf("sandboxed=%v: got %q", tc.sandboxed, out)
		}
	}
}

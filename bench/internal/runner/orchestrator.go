package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"orchbench/internal/config"
	"orchbench/internal/task"
)

// The orchestrator's exit codes that matter here (internal/exitcode in the core, which bench
// does not import).
const (
	exitConfigOrDependency = 2
	exitProviderExecution  = 3
	exitUserCancellation   = 5
)

// runsDir is where the orchestrator keeps its runs, relative to the workspace.
var runsDir = filepath.Join(".orchestrator", "runs")

// usageJSON is domain.Usage as run.json and decision.json write it. An outcome's usage is the
// same plus calls.
type usageJSON struct {
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	Calls               int     `json:"calls"`
}

// outcomeJSON is the `outcome` block of run.json (SPEC.md §11.2).
type outcomeJSON struct {
	FixIterations    int       `json:"fix_iterations"`
	TestsPassedFinal *bool     `json:"tests_passed_final"`
	ReviewVerdict    string    `json:"review_verdict"`
	Usage            usageJSON `json:"usage"`
	DecisionOverhead struct {
		CostUSD float64 `json:"cost_usd"`
		MS      int64   `json:"ms"`
		Calls   int     `json:"calls"`
	} `json:"decision_overhead"`
}

// executionJSON is one entry of run.json's executions.
type executionJSON struct {
	Index        int        `json:"index"`
	Action       string     `json:"action"`
	Status       string     `json:"status"`
	ArtifactPath string     `json:"artifact_path"`
	Usage        *usageJSON `json:"usage"`
	Error        string     `json:"error"`
	TestResult   *struct {
		Success bool `json:"success"`
	} `json:"test_result"`
	ReviewResult *struct {
		Issues []struct {
			Severity string `json:"severity"`
		} `json:"issues"`
	} `json:"review_result"`
}

// runJSON is run.json.
type runJSON struct {
	Status        string          `json:"status"`
	FailureReason string          `json:"failure_reason"`
	FixIterations int             `json:"fix_iterations"`
	Executions    []executionJSON `json:"executions"`
	Outcome       *outcomeJSON    `json:"outcome"`
}

// decisionJSON is decision.json.
type decisionJSON struct {
	Source     string `json:"source"`
	Complexity struct {
		Selected string `json:"selected"`
	} `json:"complexity"`
	Stages []struct {
		Stage   string `json:"stage"`
		Enabled bool   `json:"enabled"`
	} `json:"stages"`
	Usage *struct {
		Translation    *usageJSON `json:"translation"`
		ContextDepth   *usageJSON `json:"context_depth"`
		Classification *usageJSON `json:"classification"`
	} `json:"usage"`
	Latency struct {
		TranslationMS    int64 `json:"translation_ms"`
		PreCheckMS       int64 `json:"pre_check_ms"`
		ContextMS        int64 `json:"context_ms"`
		ClassificationMS int64 `json:"classification_ms"`
	} `json:"latency"`
}

// stageOfAction is the fixed stage → action mapping of the orchestrator, read backwards: the
// stage a recorded execution belongs to.
var stageOfAction = map[string]string{
	"research":    "research",
	"analyze":     "analysis",
	"investigate": "investigation",
	"implement":   "development",
	"test":        "testing",
	"review":      "review",
}

// runOrchestrator runs configs B, C, D and B′: render the config for the task, run
// `orchestrator run --mode auto [--stage …] "<task>"` in the workspace, and read the run
// directory it leaves.
func runOrchestrator(ctx context.Context, cfg config.Config, t task.Task, ws Workspace) (Outcome, error) {
	text := strings.TrimSpace(t.Task)
	switch {
	case text == "":
		return Outcome{}, fmt.Errorf("task %s has no text", t.ID)
	case strings.HasPrefix(text, "-"):
		// The orchestrator reads its flags from anywhere in the command line and has no
		// `--`: a task starting with a dash would be taken for one.
		return Outcome{}, fmt.Errorf("task %s: the text starts with %q, which the orchestrator would read as a flag", t.ID, text[:1])
	}

	rendered, sha, err := cfg.RenderConfig(t)
	if err != nil {
		return Outcome{}, err
	}
	if ws.OutDir == "" {
		return Outcome{}, errors.New("runner: the output directory is empty")
	}
	configPath, err := filepath.Abs(filepath.Join(ws.OutDir, "configs", t.ID+"-"+cfg.Name+".yaml"))
	if err != nil {
		return Outcome{}, err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return Outcome{}, err
	}
	if err := os.WriteFile(configPath, rendered, 0o644); err != nil {
		return Outcome{}, err
	}
	out := Outcome{ConfigPath: configPath, ConfigSHA256: sha, ExitCode: -1}

	bin, err := lookBin(ws.Bins.Orchestrator, "orchestrator")
	if err != nil {
		return out, err
	}
	// A reused workspace would hand us somebody else's run.
	if dirs, err := runDirs(ws.Dir); err != nil {
		return out, &InfraError{Reason: "reading the runs directory", Err: err}
	} else if len(dirs) > 0 {
		return out, infra("the workspace already has a run directory: %s", dirs[0])
	}

	args := []string{"--config", configPath, "--mode", "auto", "run"}
	for _, s := range cfg.Stages {
		args = append(args, "--stage", s)
	}
	args = append(args, text)

	runCtx, cancel := context.WithTimeout(ctx, t.Timeout.Std()+orchestratorGrace)
	defer cancel()

	proc, err := execute(runCtx, command{
		Bin:    bin,
		Args:   args,
		Dir:    ws.Dir,
		Env:    environ(ws.Bins),
		Stdout: ws.Stdout,
		Stderr: ws.Stderr,
	})
	out.ExitCode = proc.ExitCode
	if err != nil {
		return out, &InfraError{Reason: "running the orchestrator", Err: err}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}

	return classifyOrchestrator(out, t, ws.Dir, proc)
}

// authFailureLine is the detail line of the core's claude auth failure (apperr.FailureAuth,
// MVP.md §46: a refused credential or the wrong one used), which exits 2 like a config error.
// shortcut: parses the core's error wording; upgrade when run.json records the failure kind.
var authFailureLine = regexp.MustCompile(`^detail: claude auth \((api|login)\)`)

// classifyOrchestrator turns a finished orchestrator process and the run directory it left
// into an Outcome or an *InfraError. The order is the rule: what is certainly not the model's
// comes first, then the timeout, then the run's own record.
func classifyOrchestrator(out Outcome, t task.Task, workDir string, proc process) (Outcome, error) {
	if !proc.TimedOut {
		switch proc.ExitCode {
		case exitConfigOrDependency:
			if line, ok := matchLine([]*regexp.Regexp{authFailureLine}, proc.Stderr); ok {
				return out, authError("the provider", line)
			}
			return out, infra("the orchestrator refused the config or a dependency is missing%s", tailNote(proc.Stderr))
		case exitUserCancellation:
			return out, infra("the orchestrator was cancelled from outside (exit %d)", proc.ExitCode)
		}
	}

	dirs, err := runDirs(workDir)
	if err != nil {
		return out, &InfraError{Reason: "reading the runs directory", Err: err}
	}
	if len(dirs) > 1 {
		return out, infra("%d run directories in the workspace, expected one", len(dirs))
	}
	if len(dirs) == 0 {
		if proc.TimedOut {
			return timedOut(out), nil
		}
		return out, infra("the orchestrator left no run directory (exit %d)%s", proc.ExitCode, tailNote(proc.Stderr))
	}
	runDir := dirs[0]
	out.RunDir = runDir

	var run runJSON
	if err := readJSON(filepath.Join(runDir, "run.json"), &run); err != nil {
		if proc.TimedOut {
			return timedOut(out), nil
		}
		return out, &InfraError{Reason: "reading run.json", Err: err}
	}
	// decision.json is written after the decision: a run that died before has none, and a
	// --stage run's is bare. Neither is an error.
	var decision *decisionJSON
	var d decisionJSON
	if err := readJSON(filepath.Join(runDir, "decision.json"), &d); err == nil {
		decision = &d
	}

	fill(&out, run, decision)
	if t.Category == task.CategoryQuestion {
		out.Answer = lastAnswer(runDir, run)
	}

	if proc.TimedOut {
		return timedOut(out), nil
	}
	// Before the crash check: a decision call the limit refused leaves no step either.
	if proc.ExitCode == exitProviderExecution {
		texts := []string{proc.Stderr, run.FailureReason}
		if n := len(run.Executions); n > 0 {
			texts = append(texts, run.Executions[n-1].Error)
		}
		if line, ok := rateLimited(texts...); ok {
			return out, limitError("the provider", line, texts, resetsAtText(texts...))
		}
	}
	if len(run.Executions) == 0 && proc.ExitCode != 0 {
		return Outcome{ConfigPath: out.ConfigPath, ConfigSHA256: out.ConfigSHA256, RunDir: runDir, ExitCode: proc.ExitCode},
			infra("the orchestrator crashed before any step (exit %d)%s", proc.ExitCode, tailNote(proc.Stderr))
	}

	switch run.Status {
	case StatusCompleted, StatusFailed, StatusStopped:
		out.Status, out.FailureReason = run.Status, run.FailureReason
	default:
		// Pending or running at exit: the process died without finishing the run.
		out.Status = StatusFailed
		out.FailureReason = fmt.Sprintf("the orchestrator exited with code %d before the run finished (status %q)", proc.ExitCode, run.Status)
	}
	if out.Status == StatusFailed && out.FailureReason == "" {
		out.FailureReason = fmt.Sprintf("the orchestrator exited with code %d", proc.ExitCode)
	}
	return out, nil
}

// timedOut marks the run as one the task's timeout ended.
func timedOut(out Outcome) Outcome {
	out.Status, out.FailureReason, out.TimedOut = StatusFailed, ReasonTimeout, true
	return out
}

// runDirs lists the run directories of a workspace.
func runDirs(workDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(workDir, runsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(workDir, runsDir, e.Name()))
		}
	}
	return dirs, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// fill copies cost, tokens, calls, decision overhead, stages, complexity, fix iterations,
// review verdict and the final test result into out. They come from the run's `outcome` block;
// a run that has none (killed, or written before the block existed) gets the same numbers
// computed from its executions and its decision, as the core's report does.
func fill(out *Outcome, run runJSON, decision *decisionJSON) {
	o := run.Outcome
	if o == nil {
		o = recompute(run, decision)
	}
	out.CostUSD = o.Usage.CostUSD
	out.Tokens = Tokens{
		Input:         o.Usage.InputTokens,
		Output:        o.Usage.OutputTokens,
		CacheRead:     o.Usage.CacheReadTokens,
		CacheCreation: o.Usage.CacheCreationTokens,
	}
	out.LLMCalls = o.Usage.Calls
	out.Decision = Decision{CostUSD: o.DecisionOverhead.CostUSD, MS: o.DecisionOverhead.MS, Calls: o.DecisionOverhead.Calls}
	out.FixIterations = o.FixIterations
	out.ReviewVerdict = o.ReviewVerdict
	out.TestsPassedFinal = o.TestsPassedFinal

	out.Stages = stages(run, decision)
	if decision != nil {
		out.Complexity = decision.Complexity.Selected
	}
}

// recompute is domain.ComputeOutcome over the minimal structs.
func recompute(run runJSON, decision *decisionJSON) *outcomeJSON {
	o := &outcomeJSON{FixIterations: run.FixIterations}
	add := func(u *usageJSON) {
		o.Usage.InputTokens += u.InputTokens
		o.Usage.OutputTokens += u.OutputTokens
		o.Usage.CacheReadTokens += u.CacheReadTokens
		o.Usage.CacheCreationTokens += u.CacheCreationTokens
		o.Usage.CostUSD += u.CostUSD
		o.Usage.Calls++
	}

	for _, ex := range run.Executions {
		switch ex.Action {
		case "test":
			o.TestsPassedFinal = nil
			if ex.TestResult != nil {
				passed := ex.TestResult.Success
				o.TestsPassedFinal = &passed
			}
		case "review":
			o.ReviewVerdict = ""
			if ex.ReviewResult != nil {
				o.ReviewVerdict = "pass"
				for _, issue := range ex.ReviewResult.Issues {
					if issue.Severity == "critical" || issue.Severity == "high" {
						o.ReviewVerdict = "fail"
					}
				}
			}
		}
		if ex.Usage != nil {
			add(ex.Usage)
		}
	}

	if decision != nil && decision.Source != "manual" {
		if decision.Usage != nil {
			for _, u := range []*usageJSON{decision.Usage.Translation, decision.Usage.ContextDepth, decision.Usage.Classification} {
				if u == nil {
					continue
				}
				add(u)
				o.DecisionOverhead.CostUSD += u.CostUSD
				o.DecisionOverhead.Calls++
			}
		}
		l := decision.Latency
		o.DecisionOverhead.MS = l.TranslationMS + l.PreCheckMS + l.ContextMS + l.ClassificationMS
	}
	return o
}

// stages are the stage names the workflow ran, in order: the enabled stages of the decision, or
// without any (no decision.json) the stages of the executions, each once.
func stages(run runJSON, decision *decisionJSON) []string {
	var names []string
	if decision != nil {
		for _, s := range decision.Stages {
			if s.Enabled {
				names = append(names, s.Stage)
			}
		}
	}
	if len(names) > 0 {
		return names
	}
	seen := map[string]bool{}
	for _, ex := range run.Executions {
		if s, ok := stageOfAction[ex.Action]; ok && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	return names
}

// lastAnswer is the `answer` of the last completed read-only step's artifact: without
// implement the last of research, analyze and investigate writes the reply to the user's request. It
// is empty when there is none.
func lastAnswer(runDir string, run runJSON) string {
	for i := len(run.Executions) - 1; i >= 0; i-- {
		ex := run.Executions[i]
		switch ex.Action {
		case "research", "analyze", "investigate":
		default:
			continue
		}
		if ex.Status != "completed" {
			continue
		}
		if ex.ArtifactPath == "" || !filepath.IsLocal(ex.ArtifactPath) {
			return ""
		}
		var artifact struct {
			Answer string `json:"answer"`
		}
		if err := readJSON(filepath.Join(runDir, ex.ArtifactPath), &artifact); err != nil {
			return ""
		}
		return artifact.Answer
	}
	return ""
}

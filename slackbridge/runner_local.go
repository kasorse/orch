package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Outcome is what a run reports back to the Slack thread.
type Outcome struct {
	OK   bool
	Text string // Slack mrkdwn
	// NeedsInput: the orchestrator stopped with "task needs input"; a reply can add to the task.
	NeedsInput bool
}

// Runner runs one task for one project. progress posts an intermediate line to the thread.
type Runner interface {
	Run(ctx context.Context, req Request, progress func(string)) Outcome
}

// orchResult is the one line `orchestrator --output json` prints on stdout (MVP.md §50.10).
type orchResult struct {
	RunID         string   `json:"run_id"`
	Status        string   `json:"status"`
	ExitCode      int      `json:"exit_code"`
	FailureReason string   `json:"failure_reason"`
	Error         string   `json:"error"`
	Workflow      []string `json:"workflow"`
	Branch        string   `json:"branch"`
	Commit        string   `json:"commit"`
	MR            *struct {
		IID int    `json:"iid"`
		URL string `json:"url"`
	} `json:"mr"`
	MRSkipped     string  `json:"mr_skipped"`
	MRError       string  `json:"mr_error"`
	ReviewVerdict string  `json:"review_verdict"`
	TestsPassed   *bool   `json:"tests_passed_final"`
	CostUSD       float64 `json:"cost_usd"`
	RunDir        string  `json:"run_dir"`
	Report        string  `json:"report"`
}

type LocalRunner struct {
	cfg *Local
}

func (r *LocalRunner) command(req Request) []string {
	return r.argv("auto", runArgs(req)...)
}

// forcedConfig writes a copy of the orchestrator config whose clarity threshold lets any task
// through, for "Run anyway": the model's clarify step still records its assumptions.
func (r *LocalRunner) forcedConfig() (string, func(), error) {
	path := r.cfg.Config
	if path == "" {
		path = "orchestrator.yaml"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.cfg.Dir, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", nil, err
	}
	policy, _ := doc["policy"].(map[string]any)
	if policy == nil {
		policy = map[string]any{}
		doc["policy"] = policy
	}
	clarify, _ := policy["clarify"].(map[string]any)
	if clarify == nil {
		clarify = map[string]any{}
		policy["clarify"] = clarify
	}
	clarify["threshold"] = 0.01
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp("", "orchestrator-force-*.yaml")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// withForce runs fn with r's config switched to the forced copy when req.Force is set.
func (r *LocalRunner) withForce(req Request, fn func(*LocalRunner) (Outcome, string)) (Outcome, string) {
	if !req.Force {
		return fn(r)
	}
	path, cleanup, err := r.forcedConfig()
	if err != nil {
		return Outcome{Text: "could not prepare run anyway: " + err.Error()}, ""
	}
	defer cleanup()
	cp := *r.cfg
	cp.Config = path
	return fn(&LocalRunner{cfg: &cp})
}

// runArgs is `run [--jira REF] [task]`; each value is one argv element.
func runArgs(req Request) []string {
	a := []string{"run"}
	if req.Jira != "" {
		a = append(a, "--jira", req.Jira)
	}
	if req.Task != "" {
		// The orchestrator has no "--" terminator and reads any argv element that equals or
		// starts like a flag as one: a task such as "--config /x" must stay text.
		a = append(a, safeTask(req.Task))
	}
	return a
}

// safeTask keeps a task from being read as a flag by prefixing a leading dash with a space.
func safeTask(t string) string {
	if strings.HasPrefix(t, "-") {
		return " " + t
	}
	return t
}

// argv is the orchestrator command line; the last element is never parsed by a shell.
func (r *LocalRunner) argv(mode string, sub ...string) []string {
	bin := r.cfg.Orchestrator
	if bin == "" {
		bin = "orchestrator"
	}
	args := []string{bin}
	if r.cfg.Config != "" {
		args = append(args, "--config", r.cfg.Config)
	}
	args = append(args, r.cfg.Args...)
	// The task is one argv element: Slack text never reaches a shell.
	args = append(args, "--mode", mode, "--output", "json")
	return append(args, sub...)
}

func (r *LocalRunner) Run(ctx context.Context, req Request, progress func(string)) Outcome {
	o, _ := r.withForce(req, func(lr *LocalRunner) (Outcome, string) {
		return lr.exec(ctx, lr.command(req), false, progress)
	})
	return o
}

// Plan runs the decision only (--mode plan) and returns the run id to resume.
func (r *LocalRunner) Plan(ctx context.Context, req Request, progress func(string)) (Outcome, string) {
	return r.withForce(req, func(lr *LocalRunner) (Outcome, string) {
		return lr.exec(ctx, lr.argv("plan", runArgs(req)...), true, progress)
	})
}

// Resume runs a planned run to the end.
func (r *LocalRunner) Resume(ctx context.Context, runID string, progress func(string)) Outcome {
	o, _ := r.exec(ctx, r.argv("auto", "resume", runID), false, progress)
	return o
}

// progressEvery is how often the live log in Slack is refreshed (Slack allows ~1 edit/s).
var progressEvery = 3 * time.Second

func (r *LocalRunner) exec(ctx context.Context, argv []string, plan bool, progress func(string)) (Outcome, string) {
	timeout := 60 * time.Minute
	if r.cfg.Timeout != "" {
		if d, err := time.ParseDuration(r.cfg.Timeout); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	env := os.Environ()
	if r.cfg.EnvCommand != "" {
		extra, err := envFromCommand(ctx, r.cfg.EnvCommand)
		if err != nil {
			return Outcome{Text: "could not prepare the environment: " + err.Error()}, ""
		}
		env = mergeEnv(env, extra)
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = r.cfg.Dir
	cmd.Env = env
	var stdout bytes.Buffer
	stderr := &tailBuffer{max: 40}
	cmd.Stdout = &stdout
	if plan {
		// --mode plan still asks "Approve this workflow?". Approving a plan only previews it:
		// nothing runs until the Slack Run button resumes it with --mode auto.
		cmd.Stdin = strings.NewReader("a\n")
	}
	cmd.Stderr = stderr
	start := time.Now()
	title := ":gear: *running*"
	if plan {
		title = ":mag: *planning*"
	}
	show := func(final bool) {
		if progress == nil {
			return
		}
		head := fmt.Sprintf("%s · %s", title, time.Since(start).Round(time.Second))
		if final {
			head = fmt.Sprintf("%s · done in %s", title, time.Since(start).Round(time.Second))
		}
		progress(head + "\n" + codeBlock(lastLines(stderr.String(), 15)))
	}
	show(false)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(progressEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				show(false)
			}
		}
	}()
	err := cmd.Run()
	close(done)
	show(true)
	dur := time.Since(start).Round(time.Second)

	res, perr := lastJSON(stdout.Bytes())
	if perr != nil {
		msg := "the orchestrator gave no result"
		if err != nil {
			msg += " (" + err.Error() + ")"
		}
		return Outcome{Text: msg + "\n" + codeBlock(stderr.String())}, ""
	}
	if res.ExitCode == 4 && res.FailureReason == "task needs input" {
		return Outcome{NeedsInput: true, Text: formatNeedsInput(res)}, res.RunID
	}
	if plan && res.ExitCode == 0 {
		return Outcome{OK: true, Text: formatPlan(res)}, res.RunID
	}
	return Outcome{OK: res.ExitCode == 0, Text: formatResult(res, dur, stderr.String())}, res.RunID
}

func formatResult(res orchResult, dur time.Duration, logTail string) string {
	var b strings.Builder
	icon := ":white_check_mark:"
	if res.ExitCode != 0 {
		icon = ":x:"
		if res.ExitCode == 6 {
			icon = ":warning:"
		}
	}
	fmt.Fprintf(&b, "%s *%s* (exit %d, %s", icon, res.Status, res.ExitCode, dur)
	if res.CostUSD > 0 {
		fmt.Fprintf(&b, ", $%.2f", res.CostUSD)
	}
	b.WriteString(")\n")
	if len(res.Workflow) > 0 {
		fmt.Fprintf(&b, "workflow: `%s`\n", strings.Join(res.Workflow, " → "))
	}
	if res.MR != nil && res.MR.URL != "" {
		fmt.Fprintf(&b, "merge request: %s\n", res.MR.URL)
	} else if res.Branch != "" {
		fmt.Fprintf(&b, "branch: `%s`\n", res.Branch)
	}
	if res.MRSkipped != "" {
		fmt.Fprintf(&b, "no merge request: %s\n", res.MRSkipped)
	}
	if res.MRError != "" {
		fmt.Fprintf(&b, "merge request error: %s\n", res.MRError)
	}
	if res.ReviewVerdict != "" {
		fmt.Fprintf(&b, "review: %s\n", res.ReviewVerdict)
	}
	if res.FailureReason != "" {
		fmt.Fprintf(&b, "reason: %s\n", res.FailureReason)
	}
	if res.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", redact(res.Error))
	}
	if res.RunID != "" {
		fmt.Fprintf(&b, "run: `%s`\n", res.RunID)
	}
	if res.ExitCode != 0 && res.ExitCode != 6 && strings.TrimSpace(logTail) != "" {
		b.WriteString(codeBlock(logTail))
	}
	return b.String()
}

// formatNeedsInput shows the orchestrator's questions, without its CLI advice.
func formatNeedsInput(res orchResult) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(res.Error), "\n") {
		if strings.HasPrefix(l, "Run status:") || strings.HasPrefix(l, "Reason:") || strings.HasPrefix(l, "Add what is missing") {
			continue
		}
		keep = append(keep, l)
	}
	return ":speech_balloon: *the task needs more detail*\n" + strings.TrimSpace(strings.Join(keep, "\n")) +
		"\n\n*Reply in this thread* with the answers or more context and I will plan again, " +
		"or press *Run anyway* to go on with the model's own assumptions."
}

func formatPlan(res orchResult) string {
	var b strings.Builder
	b.WriteString(":clipboard: *plan*\n")
	if len(res.Workflow) > 0 {
		fmt.Fprintf(&b, "workflow: `%s`\n", strings.Join(res.Workflow, " → "))
	}
	if res.CostUSD > 0 {
		fmt.Fprintf(&b, "planning cost: $%.2f\n", res.CostUSD)
	}
	fmt.Fprintf(&b, "run: `%s`", res.RunID)
	return b.String()
}

// lastJSON finds the last line of out that is a JSON object.
func lastJSON(out []byte) (orchResult, error) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") {
			var r orchResult
			if err := json.Unmarshal([]byte(l), &r); err == nil {
				return r, nil
			}
		}
	}
	return orchResult{}, fmt.Errorf("no JSON result line")
}

// envFromCommand runs an operator-configured command and reads `export NAME=value` lines.
func envFromCommand(ctx context.Context, command string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("env_command failed: %v", err)
	}
	var env []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		l = strings.TrimPrefix(l, "export ")
		name, val, ok := strings.Cut(l, "=")
		if !ok || name == "" || strings.ContainsAny(name, " \t'\"") {
			continue
		}
		env = append(env, name+"="+unquoteShell(val))
	}
	if len(env) == 0 {
		return nil, fmt.Errorf("env_command printed no export lines")
	}
	return env, nil
}

// unquoteShell undoes the single or double quoting of a shell assignment's value.
func unquoteShell(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], `'\''`, `'`)
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

func mergeEnv(base, extra []string) []string {
	idx := map[string]int{}
	out := append([]string(nil), base...)
	for i, kv := range out {
		n, _, _ := strings.Cut(kv, "=")
		idx[n] = i
	}
	for _, kv := range extra {
		n, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[n]; ok {
			out[i] = kv
		} else {
			idx[n] = len(out)
			out = append(out, kv)
		}
	}
	return out
}

// tailBuffer keeps the last max lines written to it.
type tailBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
	part  string
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.part + string(p)
	parts := strings.Split(s, "\n")
	t.part = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		t.lines = append(t.lines, l)
		if len(t.lines) > t.max {
			t.lines = t.lines[1:]
		}
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := append([]string(nil), t.lines...)
	if t.part != "" {
		all = append(all, t.part)
	}
	return strings.Join(all, "\n")
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

func codeBlock(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(redact(s), "```", "'''"))
	if s == "" {
		return ""
	}
	if len(s) > 2500 {
		s = "…" + s[len(s)-2500:]
	}
	return "```\n" + s + "\n```"
}

// Command fake is the claude and the orchestrator of bench's end-to-end test. The test builds it
// once and installs it under both names; the name it is run as says which program it plays, and
// the plan file in $FAKE_PLAN says what it does for each task and config:
//
//	fix, regression   copy $FAKE_PATCHES/<kind>/ over the working directory
//	nochange          leave the tree alone
//	answer-ok         answer as the reference answer does
//	answer-bad        answer with something the answer checks reject
//	infra             fail as a broken installation does, with no result
//
// The task is found by the "[e2e:<task id>]" the test puts in front of every task text.
// $FAKE_BLOCK names one "<task>/<config>" cell that sleeps on its first call, after writing
// $FAKE_STATE/blocked, so that the test can interrupt orchbench while a cell is running.
//
// It uses the standard library only; the module is its own, so that bench's ./... and the
// import-boundary test never see it.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// referenceAnswer is what a good answer to the question task says: its answer_checks match it.
const referenceAnswer = "It is the mul function in calc.sh: it adds its arguments instead of multiplying them."

// stageOrder is the orchestrator's fixed order of stages.
var stageOrder = []string{"research", "investigation", "development", "testing", "review"}

// marker names the task in a prompt.
var marker = regexp.MustCompile(`\[e2e:([a-z0-9-]+)\]`)

func main() {
	args := os.Args[1:]
	switch name := filepath.Base(os.Args[0]); name {
	case "claude":
		os.Exit(claude(args))
	case "orchestrator":
		os.Exit(orchestrator(args))
	default:
		fmt.Fprintf(os.Stderr, "fake: run me as claude or orchestrator, not %q\n", name)
		os.Exit(64)
	}
}

// claude is `claude -p` for config A: the prompt on stdin, stream-json on stdout.
func claude(args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("2.1.0 (Claude Code)")
		return 0
	}
	prompt, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fail("claude", err)
	}
	task, kind, cost, err := lookup(string(prompt), "A")
	if err != nil {
		return fail("claude", err)
	}
	block(task, "A")
	if kind == "infra" {
		fmt.Fprintln(os.Stderr, "fake claude: the canned installation failure")
		return 1
	}
	if err := applyPatch(kind); err != nil {
		return fail("claude", err)
	}

	emit(map[string]any{"type": "system", "subtype": "init", "session_id": "e2e"})
	emit(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": "working on it"}})
	result := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": answer(kind)}
	result["total_cost_usd"] = cost
	result["num_turns"] = 3
	result["usage"] = map[string]any{"input_tokens": 1000, "output_tokens": 200, "cache_read_input_tokens": 3000, "cache_creation_input_tokens": 100}
	emit(result)
	return 0
}

// orchestrator is `orchestrator --config <file> --mode auto run [--stage <name>]... <task>`: it
// leaves a run directory in the working directory, as the real one does.
func orchestrator(args []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println("orchestrator e2e")
		return 0
	}

	var configPath, text string
	var stages []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--config" && i+1 < len(args):
			i++
			configPath = args[i]
		case arg == "--mode" && i+1 < len(args):
			i++
		case arg == "--stage" && i+1 < len(args):
			i++
			stages = append(stages, args[i])
		case arg == "run":
		default:
			text = arg
		}
	}
	if configPath == "" || text == "" {
		return fail("orchestrator", fmt.Errorf("want --config and a task, got %q", args))
	}
	if _, err := os.Stat(configPath); err != nil {
		return fail("orchestrator", err)
	}

	// The runner writes the config as <task>-<config>.yaml.
	name := strings.TrimSuffix(filepath.Base(configPath), ".yaml")
	config := name[strings.LastIndex(name, "-")+1:]
	task, kind, cost, err := lookup(text, config)
	if err != nil {
		return fail("orchestrator", err)
	}
	block(task, config)
	if kind == "infra" {
		fmt.Fprintln(os.Stderr, "fake orchestrator: the canned configuration failure")
		return 2
	}
	if err := applyPatch(kind); err != nil {
		return fail("orchestrator", err)
	}
	if err := writeRun(kind, cost, stages); err != nil {
		return fail("orchestrator", err)
	}
	fmt.Println("run e2e-1 completed")
	return 0
}

// fail reports an error of the fake itself, which is not one of the plan's outcomes.
func fail(who string, err error) int {
	fmt.Fprintf(os.Stderr, "fake %s: %v\n", who, err)
	return 70
}

// lookup finds the task in the text and what the plan says for it with config.
func lookup(text, config string) (task, kind string, cost float64, err error) {
	m := marker.FindStringSubmatch(text)
	if m == nil {
		return "", "", 0, fmt.Errorf("no [e2e:<task id>] marker in the task text")
	}
	data, err := os.ReadFile(os.Getenv("FAKE_PLAN"))
	if err != nil {
		return "", "", 0, err
	}
	var plan map[string]map[string]any
	if err := json.Unmarshal(data, &plan); err != nil {
		return "", "", 0, err
	}
	entry, ok := plan[m[1]+"/"+config]
	if !ok {
		return "", "", 0, fmt.Errorf("the plan has no entry for %s/%s", m[1], config)
	}
	kind, _ = entry["kind"].(string)
	cost, _ = entry["cost"].(float64)
	return m[1], kind, cost, nil
}

// block sleeps on the first call of the cell $FAKE_BLOCK names, after leaving the marker the
// test waits for. The sleep ends when orchbench kills the process group.
func block(task, config string) {
	if os.Getenv("FAKE_BLOCK") != task+"/"+config {
		return
	}
	marked := filepath.Join(os.Getenv("FAKE_STATE"), "blocked")
	if _, err := os.Stat(marked); err == nil {
		return
	}
	if err := os.WriteFile(marked, []byte("blocked\n"), 0o644); err != nil {
		return
	}
	time.Sleep(5 * time.Minute)
}

// applyPatch copies the canned tree of a fix over the working directory; every other kind
// changes no file.
func applyPatch(kind string) error {
	if kind != "fix" && kind != "regression" {
		return nil
	}
	root := filepath.Join(os.Getenv("FAKE_PATCHES"), kind)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			return err
		}
		return os.WriteFile(rel, data, 0o644)
	})
}

// answer is the reply of a question task, and "Done." for a task that changes code.
func answer(kind string) string {
	switch kind {
	case "answer-ok":
		return referenceAnswer
	case "answer-bad":
		return "I could not find where that is implemented."
	}
	return "Done."
}

// emit prints one stream-json event.
func emit(event map[string]any) {
	data, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}

// writeRun writes .orchestrator/runs/e2e-1 the way a finished run leaves it: run.json with its
// outcome, decision.json, and for a question the artifact that holds the answer. Without
// --stage the decision chose the stages, as for config B; with it the decision is manual.
func writeRun(kind string, cost float64, stages []string) error {
	dir := filepath.Join(".orchestrator", "runs", "e2e-1")
	question := strings.HasPrefix(kind, "answer")

	usage := map[string]any{"input_tokens": 1000, "output_tokens": 200, "cache_read_tokens": 3000, "cache_creation_tokens": 100, "cost_usd": cost, "calls": 3}
	overhead := map[string]any{"cost_usd": 0, "ms": 0, "calls": 0}
	source, complexity, enabled := "manual", "", stages
	if len(stages) == 0 {
		source, complexity = "llm", "medium"
		overhead = map[string]any{"cost_usd": 0.01, "ms": 500, "calls": 2}
		enabled = []string{"development", "testing"}
		if question {
			enabled = []string{"investigation"}
		}
	}
	var decided []any
	for _, s := range stageOrder {
		decided = append(decided, map[string]any{"stage": s, "enabled": slices.Contains(enabled, s)})
	}
	decision := map[string]any{"source": source, "complexity": map[string]any{"selected": complexity}, "stages": decided}

	var executions []any
	if question {
		executions = append(executions, map[string]any{"index": 1, "action": "investigate", "status": "completed", "artifact_path": "artifacts/001-investigate.json", "usage": usage})
		if err := writeJSON(filepath.Join(dir, "artifacts", "001-investigate.json"), map[string]any{"answer": answer(kind)}); err != nil {
			return err
		}
	} else {
		executions = append(executions,
			map[string]any{"index": 1, "action": "implement", "status": "completed", "usage": usage},
			map[string]any{"index": 2, "action": "test", "status": "completed", "test_result": map[string]any{"success": true}})
	}

	var testsPassed any
	if !question {
		testsPassed = true
	}
	verdict := ""
	if slices.Contains(stages, "review") {
		verdict = "pass"
	}
	outcome := map[string]any{"fix_iterations": 0, "tests_passed_final": testsPassed, "review_verdict": verdict, "usage": usage, "decision_overhead": overhead}
	run := map[string]any{"status": "completed", "failure_reason": "", "fix_iterations": 0, "executions": executions, "outcome": outcome}

	if err := writeJSON(filepath.Join(dir, "run.json"), run); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "decision.json"), decision)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

package results

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// specLine is the example of SPEC.md §9.
const specLine = `{"schema": 1, "task": "go-pgqueue-retry-backoff", "task_sha256": "ab", "config": "B", "rep": 2,
 "language": "go", "category": "bugfix", "lang": "en", "pair": "go-pgqueue-retry-backoff-ru", "contributor": "owner",
 "started_at": "2026-10-07T10:00:00Z", "wall_ms": 412000,
 "result": "pass", "fail_reason": "",
 "oracle_exit": 0, "suite_exit": 0, "tests_reset": 0, "answer_checks_failed": [],
 "run_status": "completed", "failure_reason": "",
 "cost_usd": 1.84, "tokens": {"input": 1, "output": 2, "cache_read": 3, "cache_creation": 4},
 "llm_calls": 6, "decision": {"cost_usd": 0.07, "ms": 21000, "calls": 3},
 "fix_iterations": 1, "review_verdict": "pass",
 "stages": ["analysis","development","testing"], "expected_stages": ["analysis","development","testing"],
 "complexity": "medium",
 "diff": {"files": 3, "added": 54, "deleted": 12},
 "model": "sonnet", "claude_cli": "2.1.290", "orchestrator_commit": "c0ffee", "config_sha256": "cd"}`

// oneLine puts a multi-line JSON object on one line, as results.jsonl holds it.
func oneLine(s string) string {
	var buf strings.Builder
	for _, l := range strings.Split(s, "\n") {
		buf.WriteString(strings.TrimSpace(l) + " ")
	}
	return strings.TrimSpace(buf.String())
}

func line(task, config string, rep int, result string) string {
	b, err := json.Marshal(Record{Schema: Schema, Task: task, Config: config, Rep: rep, Result: result})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func read(t *testing.T, text string) Records {
	t.Helper()
	recs, err := Read(strings.NewReader(text))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return recs
}

func TestReadSpecExample(t *testing.T) {
	recs := read(t, oneLine(specLine)+"\n")
	if len(recs.Cells) != 1 || recs.Lines != 1 || recs.Replaced != 0 || recs.Partial != 0 {
		t.Fatalf("records = %+v", recs)
	}
	want := Record{
		Schema: 1, Task: "go-pgqueue-retry-backoff", TaskSHA256: "ab", Config: "B", Rep: 2,
		Language: "go", Category: "bugfix", Lang: "en", Pair: "go-pgqueue-retry-backoff-ru", Contributor: "owner",
		StartedAt: "2026-10-07T10:00:00Z", WallMS: 412000,
		Result: Pass, AnswerChecksFailed: []string{}, RunStatus: "completed",
		CostUSD: 1.84, Tokens: Tokens{Input: 1, Output: 2, CacheRead: 3, CacheCreation: 4},
		LLMCalls: 6, Decision: Decision{CostUSD: 0.07, MS: 21000, Calls: 3},
		FixIterations: 1, ReviewVerdict: "pass",
		Stages:         []string{"analysis", "development", "testing"},
		ExpectedStages: []string{"analysis", "development", "testing"},
		Complexity:     "medium", Diff: Diff{Files: 3, Added: 54, Deleted: 12},
		Model: "sonnet", ClaudeCLI: "2.1.290", OrchestratorCommit: "c0ffee", ConfigSHA256: "cd",
	}
	if got := recs.Cells[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("record\n%+v\nwant\n%+v", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	want := Record{
		Schema: 1, Task: "t", Config: "A", Rep: 1, Result: Fail, FailReason: FailOracle,
		Stages: []string{"development"}, CostUSD: 0.5, Decision: Decision{Calls: 2},
		AnswerChecksFailed: []string{"x"},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	recs := read(t, string(b)+"\n")
	if len(recs.Cells) != 1 || !reflect.DeepEqual(recs.Cells[0], want) {
		t.Fatalf("round trip: %+v, want %+v", recs.Cells, want)
	}
}

func TestLatestLineWinsPerCell(t *testing.T) {
	text := strings.Join([]string{
		line("t1", "A", 1, InfraError),
		line("t1", "B", 1, Pass),
		line("t1", "A", 1, Fail),
		line("t1", "A", 1, Pass),
	}, "\n") + "\n"

	recs := read(t, text)
	if recs.Lines != 4 || recs.Replaced != 2 || len(recs.Cells) != 2 {
		t.Fatalf("records = %+v", recs)
	}
	if got := recs.Cells[0]; got.Config != "A" || got.Result != Pass {
		t.Errorf("first cell = %+v, want A pass", got)
	}
}

func TestBlankLinesAreSkipped(t *testing.T) {
	text := "\n" + line("t1", "A", 1, Pass) + "\n   \n\n" + line("t1", "A", 2, Pass) + "\n\n"
	recs := read(t, text)
	if len(recs.Cells) != 2 || recs.Lines != 2 || recs.Partial != 0 {
		t.Fatalf("records = %+v", recs)
	}
}

func TestEmptyInput(t *testing.T) {
	recs := read(t, "")
	if len(recs.Cells) != 0 || recs.Lines != 0 {
		t.Fatalf("records = %+v", recs)
	}
}

func TestLastLineWithoutNewline(t *testing.T) {
	complete := line("t1", "A", 1, Pass)
	tests := []struct {
		name        string
		text        string
		cells       int
		partial     int
		wantLineErr string
	}{
		{"a complete last line is kept", complete + "\n" + line("t1", "A", 2, Pass), 2, 0, ""},
		{"a truncated last line is dropped", complete + "\n" + `{"schema":1,"task":"t1","conf`, 1, 1, ""},
		{"a newline-terminated broken line is an error", complete + "\n" + `{"schema":1,"task":"t1","conf` + "\n", 0, 0, "line 2"},
		{"a last line that parses but is wrong is an error", complete + "\n" + `{"schema":2}`, 0, 0, "line 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs, err := Read(strings.NewReader(tt.text))
			if tt.wantLineErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantLineErr) {
					t.Fatalf("error = %v, want one naming %q", err, tt.wantLineErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(recs.Cells) != tt.cells || recs.Partial != tt.partial {
				t.Fatalf("records = %+v", recs)
			}
		})
	}
}

func TestMalformedLines(t *testing.T) {
	ok := line("t1", "A", 1, Pass)
	tests := []struct {
		name string
		bad  string
		want string
	}{
		{"not json", `not json`, "line 2"},
		{"wrong schema", `{"schema":2,"task":"t","config":"A","rep":1,"result":"pass"}`, "schema 2"},
		{"no schema", `{"task":"t","config":"A","rep":1,"result":"pass"}`, "schema 0"},
		{"no task", `{"schema":1,"config":"A","rep":1,"result":"pass"}`, "no task"},
		{"no config", `{"schema":1,"task":"t","rep":1,"result":"pass"}`, "no config"},
		{"no rep", `{"schema":1,"task":"t","config":"A","result":"pass"}`, "rep 0"},
		{"no result", `{"schema":1,"task":"t","config":"A","rep":1}`, `result ""`},
		{"unknown result", `{"schema":1,"task":"t","config":"A","rep":1,"result":"maybe"}`, `result "maybe"`},
		{"wrong type", `{"schema":1,"task":"t","config":"A","rep":"one","result":"pass"}`, "line 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Read(strings.NewReader(ok + "\n" + tt.bad + "\n" + ok + "\n"))
			if err == nil {
				t.Fatal("Read succeeded")
			}
			if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want line 2 and %q", err, tt.want)
			}
		})
	}
}

func TestOrderDoesNotDependOnInput(t *testing.T) {
	lines := []string{
		line("t2", "B", 1, Pass),
		line("t1", "B", 2, Fail),
		line("t1", "A", 1, Pass),
		line("t1", "B", 1, Pass),
		line("t2", "A", 1, Pass),
	}
	forward := read(t, strings.Join(lines, "\n")+"\n")

	reversed := make([]string, len(lines))
	for i, l := range lines {
		reversed[len(lines)-1-i] = l
	}
	backward := read(t, strings.Join(reversed, "\n")+"\n")

	if !reflect.DeepEqual(forward.Cells, backward.Cells) {
		t.Fatalf("order differs:\n%+v\n%+v", forward.Cells, backward.Cells)
	}
	var got []Cell
	for _, r := range forward.Cells {
		got = append(got, r.Cell())
	}
	want := []Cell{{"t1", "A", 1}, {"t1", "B", 1}, {"t1", "B", 2}, {"t2", "A", 1}, {"t2", "B", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cells = %v, want %v", got, want)
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	if err := os.WriteFile(path, []byte(line("t1", "A", 1, Pass)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadFile(path)
	if err != nil || len(recs.Cells) != 1 {
		t.Fatalf("ReadFile = %+v, %v", recs, err)
	}

	_, err = ReadFile(filepath.Join(dir, "missing.jsonl"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: error = %v, want ErrNotExist", err)
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(bad); err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("a malformed file: error = %v, want the path and line 1", err)
	}
}

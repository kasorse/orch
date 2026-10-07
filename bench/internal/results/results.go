// Package results is the result line of specs/benchmark/SPEC.md §9: one JSON object per cell
// (task × config × repetition) in results.jsonl. It holds the record type and the reader that
// `orchbench summarize` (BM11) needs; BM10 (`orchbench run`) adds the append-only writer and the
// manifest here.
package results

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// Schema is the version of the result line this package reads.
const Schema = 1

// The values of Record.Result.
const (
	Pass       = "pass"
	Fail       = "fail"
	InfraError = "infra_error"
)

// The values of Record.FailReason (SPEC.md §7.5, §5.3).
const (
	FailOracle      = "oracle"
	FailRegression  = "regression"
	FailAnswer      = "answer"
	FailTreeChanged = "tree_changed"
)

// Record is one result line (SPEC.md §9). A field a line leaves out is its zero value.
type Record struct {
	Schema     int    `json:"schema"`
	Task       string `json:"task"`
	TaskSHA256 string `json:"task_sha256"`
	Config     string `json:"config"`
	Rep        int    `json:"rep"`

	Language    string `json:"language"`
	Category    string `json:"category"`
	Lang        string `json:"lang"`
	Pair        string `json:"pair,omitempty"`
	Contributor string `json:"contributor"`

	StartedAt string `json:"started_at"`
	WallMS    int64  `json:"wall_ms"`

	Result             string   `json:"result"`
	FailReason         string   `json:"fail_reason"`
	OracleExit         int      `json:"oracle_exit"`
	SuiteExit          int      `json:"suite_exit"`
	TestsReset         int      `json:"tests_reset"`
	AnswerChecksFailed []string `json:"answer_checks_failed"`
	RunStatus          string   `json:"run_status"`
	FailureReason      string   `json:"failure_reason"`

	CostUSD       float64  `json:"cost_usd"`
	Tokens        Tokens   `json:"tokens"`
	LLMCalls      int      `json:"llm_calls"`
	Decision      Decision `json:"decision"`
	FixIterations int      `json:"fix_iterations"`
	ReviewVerdict string   `json:"review_verdict"`

	Stages         []string `json:"stages"`
	ExpectedStages []string `json:"expected_stages"`
	Complexity     string   `json:"complexity"`
	Diff           Diff     `json:"diff"`

	Model              string `json:"model"`
	ClaudeCLI          string `json:"claude_cli"`
	OrchestratorCommit string `json:"orchestrator_commit"`
	ConfigSHA256       string `json:"config_sha256"`
}

// Tokens is the token usage of a cell.
type Tokens struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
}

// Decision is what the decision rounds of an orchestrator cell cost (SPEC.md §11.2).
type Decision struct {
	CostUSD float64 `json:"cost_usd"`
	MS      int64   `json:"ms"`
	Calls   int     `json:"calls"`
}

// Diff is the size of the change a cell left behind.
type Diff struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// Cell identifies a record: a later line for the same cell replaces an earlier one.
type Cell struct {
	Task   string
	Config string
	Rep    int
}

// Cell is the record's key.
func (r Record) Cell() Cell { return Cell{Task: r.Task, Config: r.Config, Rep: r.Rep} }

// Records is what a results.jsonl holds.
type Records struct {
	// Cells has the latest record of every cell, sorted by task, config and repetition.
	Cells []Record
	// Lines is the number of result lines read, Replaced how many of them a later line for
	// the same cell replaced (`run --redo` appends).
	Lines    int
	Replaced int
	// Partial is 1 when the file ends in a half-written line, which was ignored: `run` may be
	// writing it right now.
	Partial int
}

// Latest keeps the last record of every cell and sorts the result by task, config and
// repetition. It also returns how many records were replaced.
func Latest(recs []Record) (kept []Record, replaced int) {
	index := make(map[Cell]int, len(recs))
	for _, r := range recs {
		if i, ok := index[r.Cell()]; ok {
			kept[i] = r
			replaced++
			continue
		}
		index[r.Cell()] = len(kept)
		kept = append(kept, r)
	}
	slices.SortFunc(kept, func(a, b Record) int {
		return cmp.Or(cmp.Compare(a.Task, b.Task), cmp.Compare(a.Config, b.Config), cmp.Compare(a.Rep, b.Rep))
	})
	return kept, replaced
}

// ReadFile reads a results.jsonl. A file that does not exist is an error for which
// errors.Is(err, fs.ErrNotExist) holds.
func ReadFile(path string) (Records, error) {
	f, err := os.Open(path)
	if err != nil {
		return Records{}, err
	}
	defer f.Close()

	recs, err := Read(f)
	if err != nil {
		return Records{}, fmt.Errorf("%s: %w", path, err)
	}
	return recs, nil
}

// Read reads result lines. Blank lines are skipped. A line that does not parse is an error
// naming its number, except for the last one when no newline ends it: that is a line `run` is
// still writing, and it is dropped and counted in Partial. The summary is read during a run.
func Read(r io.Reader) (Records, error) {
	var out Records
	var all []Record

	br := bufio.NewReader(r)
	for n := 1; ; n++ {
		line, err := br.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return Records{}, fmt.Errorf("line %d: %w", n, err)
		}
		unterminated := err != nil

		if text := strings.TrimSpace(line); text != "" {
			rec, perr := parse(text)
			var syntax *json.SyntaxError
			switch {
			case perr == nil:
				out.Lines++
				all = append(all, rec)
			case unterminated && errors.As(perr, &syntax):
				out.Partial++
			default:
				return Records{}, fmt.Errorf("line %d: %w", n, perr)
			}
		}
		if unterminated {
			break
		}
	}

	out.Cells, out.Replaced = Latest(all)
	return out, nil
}

func parse(text string) (Record, error) {
	var rec Record
	if err := json.Unmarshal([]byte(text), &rec); err != nil {
		return Record{}, err
	}
	switch {
	case rec.Schema != Schema:
		return Record{}, fmt.Errorf("schema %d, want %d", rec.Schema, Schema)
	case rec.Task == "":
		return Record{}, errors.New("no task")
	case rec.Config == "":
		return Record{}, errors.New("no config")
	case rec.Rep < 1:
		return Record{}, fmt.Errorf("rep %d, want 1 or more", rec.Rep)
	}
	switch rec.Result {
	case Pass, Fail, InfraError:
	default:
		return Record{}, fmt.Errorf("result %q, want %s, %s or %s", rec.Result, Pass, Fail, InfraError)
	}
	return rec, nil
}

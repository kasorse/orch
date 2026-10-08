package results

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resultsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "results.jsonl")
}

func TestWriterAppendsWholeLinesFromManyGoroutines(t *testing.T) {
	path := resultsPath(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}

	const n = 50
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := Record{Task: "t", Config: "A", Rep: i, Result: Pass, CostUSD: 0.5}
			if err := w.Append(rec); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	recs, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if recs.Lines != n || len(recs.Cells) != n || recs.Partial != 0 {
		t.Fatalf("read %d lines, %d cells, %d partial; want %d whole lines", recs.Lines, len(recs.Cells), recs.Partial, n)
	}
	if recs.CostUSD != 25 {
		t.Errorf("cost = %v, want 25", recs.CostUSD)
	}
}

func TestWriterWritesTheSchemaEmptyListsAndUnescapedText(t *testing.T) {
	path := resultsPath(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	rec := Record{Task: "t", Config: "A", Rep: 1, Result: Fail, FailureReason: "a && b <c>"}
	if err := w.Append(rec); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"schema":1`, `"answer_checks_failed":[]`, `"stages":[]`, `"expected_stages":[]`, `"a && b <c>"`} {
		if !strings.Contains(text, want) {
			t.Errorf("line does not contain %s:\n%s", want, text)
		}
	}
	if strings.Count(text, "\n") != 1 || !strings.HasSuffix(text, "\n") {
		t.Errorf("want exactly one newline-ended line:\n%q", text)
	}
}

func TestWriterContinuesAnExistingFile(t *testing.T) {
	path := resultsPath(t)
	for rep := 1; rep <= 2; rep++ {
		w, err := OpenWriter(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Append(Record{Task: "t", Config: "A", Rep: rep, Result: Pass}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if recs, err := ReadFile(path); err != nil || recs.Lines != 2 {
		t.Fatalf("lines = %d, err %v; want 2", recs.Lines, err)
	}
}

func TestWriterCutsAHalfWrittenLastLine(t *testing.T) {
	tests := []struct {
		name  string
		start string
		lines int
	}{
		{"after whole lines", line("t", "A", 1, Pass) + "\n" + `{"schema":1,"task":"cut`, 1},
		{"only a partial line", `{"schema":1,"task":"cut`, 0},
		{"a long partial line", line("t", "A", 1, Pass) + "\n" + strings.Repeat("x", 10_000), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := resultsPath(t)
			if err := os.WriteFile(path, []byte(tt.start), 0o644); err != nil {
				t.Fatal(err)
			}

			w, err := OpenWriter(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Append(Record{Task: "t", Config: "B", Rep: 1, Result: Pass}); err != nil {
				t.Fatal(err)
			}
			w.Close()

			recs, err := ReadFile(path)
			if err != nil {
				t.Fatalf("the file does not read after the cut: %v", err)
			}
			if recs.Lines != tt.lines+1 || recs.Partial != 0 {
				t.Errorf("lines = %d, partial = %d; want %d and 0", recs.Lines, recs.Partial, tt.lines+1)
			}
		})
	}
}

func TestWriterLeavesAFileEndingInANewlineAlone(t *testing.T) {
	path := resultsPath(t)
	before := line("t", "A", 1, Pass) + "\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	if data, _ := os.ReadFile(path); string(data) != before {
		t.Fatalf("file changed to %q", data)
	}
}

func TestWriterRefusesAppendAfterClose(t *testing.T) {
	w, err := OpenWriter(resultsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := w.Append(Record{Task: "t", Config: "A", Rep: 1, Result: Pass}); err == nil {
		t.Error("Append after Close succeeded")
	}
}

func TestRecordsCostCountsReplacedLines(t *testing.T) {
	var sb strings.Builder
	for _, cost := range []float64{1.5, 2.0, 0.25} {
		rec := Record{Schema: Schema, Task: "t", Config: "A", Rep: 1, Result: Pass, CostUSD: cost}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteString("\n")
	}
	recs := read(t, sb.String())
	if recs.Replaced != 2 || len(recs.Cells) != 1 {
		t.Fatalf("replaced = %d, cells = %d", recs.Replaced, len(recs.Cells))
	}
	if recs.CostUSD != 3.75 {
		t.Errorf("cost = %v, want 3.75: replaced lines were paid for", recs.CostUSD)
	}
}

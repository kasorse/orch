package story

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func goodSurvey() map[string]any {
	return map[string]any{
		"summary":        "Refunds live in internal/refund; the service exposes them over HTTP.",
		"relevant_files": []any{"internal/refund/service.go", "api/openapi.yaml"},
		"interfaces":     []any{"POST /v1/refunds (api/openapi.yaml)"},
		"constraints":    []any{"amounts are integer cents"},
	}
}

func goodSpecify() map[string]any {
	m := map[string]any{"slug": "refunds", "prefix": "RF", "title": "Partial refunds"}
	m["spec"] = map[string]any{"problem": "p", "approach": "a", "contracts": "c", "out_of_scope": "o", "risks": "r"}
	m["tasks"] = []any{
		map[string]any{"id": "RF01", "repo": "billing", "title": "Refund an amount", "size": "M", "depends_on": []any{},
			"goal": "Refund part of a payment.", "acceptance": []any{"a refund of 5 of 10 leaves 5"}, "notes": "Keep cents."},
		map[string]any{"id": "RF02", "repo": "gateway", "title": "Expose partial refunds", "size": "S", "depends_on": []any{"RF01"},
			"goal": "Accept an amount in POST /v1/refunds.", "acceptance": []any{"the API takes an amount", "a test covers it"}},
	}
	return m
}

func task(m map[string]any, i int) map[string]any { return m["tasks"].([]any)[i].(map[string]any) }

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func wantAnswerErr(t *testing.T, err error, field string) {
	t.Helper()
	var aerr *AnswerError
	if !errors.As(err, &aerr) {
		t.Fatalf("err = %v, want an *AnswerError for %q", err, field)
	}
	if aerr.Field != field {
		t.Fatalf("field = %q (%v), want %q", aerr.Field, aerr, field)
	}
}

func TestParseSurvey(t *testing.T) {
	a, err := ParseSurvey(marshal(t, goodSurvey()))
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary == "" || len(a.RelevantFiles) != 2 || a.Interfaces[0] != "POST /v1/refunds (api/openapi.yaml)" || a.Constraints[0] != "amounts are integer cents" {
		t.Errorf("answer = %+v", a)
	}
	if _, err := ParseSurvey([]byte(`{"summary": "s"}`)); err != nil {
		t.Errorf("the lists are optional: %v", err)
	}
}

func TestParseSurvey_Errors(t *testing.T) {
	many := func(n int, item string) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = item
		}
		return out
	}
	cases := []struct {
		name   string
		change func(m map[string]any)
		field  string
	}{
		{"unknown key", func(m map[string]any) { m["verdict"] = "ok" }, "verdict"},
		{"wrong type", func(m map[string]any) { m["summary"] = 3 }, "summary"},
		{"empty summary", func(m map[string]any) { m["summary"] = "  " }, "summary"},
		{"summary too long", func(m map[string]any) { m["summary"] = strings.Repeat("x", MaxSurveySummaryBytes+1) }, "summary"},
		{"too many files", func(m map[string]any) { m["relevant_files"] = many(MaxSurveyFiles+1, "a.go") }, "relevant_files"},
		{"absolute path", func(m map[string]any) { m["relevant_files"] = []any{"a.go", "/etc/passwd"} }, "relevant_files[1]"},
		{"dot-dot path", func(m map[string]any) { m["relevant_files"] = []any{"../other/a.go"} }, "relevant_files[0]"},
		{"inner dot-dot", func(m map[string]any) { m["relevant_files"] = []any{"a/../../b"} }, "relevant_files[0]"},
		{"unclean path", func(m map[string]any) { m["relevant_files"] = []any{"./a.go"} }, "relevant_files[0]"},
		{"double slash", func(m map[string]any) { m["relevant_files"] = []any{"a//b.go"} }, "relevant_files[0]"},
		{"root", func(m map[string]any) { m["relevant_files"] = []any{"."} }, "relevant_files[0]"},
		{"empty path", func(m map[string]any) { m["relevant_files"] = []any{""} }, "relevant_files[0]"},
		{"NUL in path", func(m map[string]any) { m["relevant_files"] = []any{"a\x00.go"} }, "relevant_files[0]"},
		{"backslash", func(m map[string]any) { m["relevant_files"] = []any{`internal\a.go`} }, "relevant_files[0]"},
		{"too many interfaces", func(m map[string]any) { m["interfaces"] = many(MaxSurveyInterfaces+1, "x") }, "interfaces"},
		{"too many constraints", func(m map[string]any) { m["constraints"] = many(MaxSurveyConstraints+1, "x") }, "constraints"},
		{"empty constraint", func(m map[string]any) { m["constraints"] = []any{"x", ""} }, "constraints[1]"},
		{"long interface", func(m map[string]any) { m["interfaces"] = []any{strings.Repeat("x", MaxSurveyItemBytes+1)} }, "interfaces[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodSurvey()
			tc.change(m)
			_, err := ParseSurvey(marshal(t, m))
			wantAnswerErr(t, err, tc.field)
		})
	}
}

func TestParseAnswers_Shape(t *testing.T) {
	for name, obj := range map[string]string{
		"not an object": `["summary"]`,
		"null":          `null`,
		"trailing data": `{} {}`,
		"not JSON":      `{"summary": `,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSurvey([]byte(obj))
			wantAnswerErr(t, err, "")
			_, err = ParseSpecify([]byte(obj), []string{"billing"}, 12, nil)
			wantAnswerErr(t, err, "")
		})
	}
}

func TestParseSpecify(t *testing.T) {
	a, err := ParseSpecify(marshal(t, goodSpecify()), []string{"gateway", "billing"}, 12, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug != "refunds" || a.Prefix != "RF" || a.Spec.Contracts != "c" || len(a.TaskAnswers) != 2 || a.TaskAnswers[1].Size != "S" {
		t.Errorf("answer = %+v", a)
	}
	tasks := a.Tasks()
	if err := Validate(tasks, []string{"billing", "gateway"}, 12); err != nil {
		t.Errorf("Tasks() does not pass Validate: %v", err)
	}
	if tasks[0].Jira != "" || tasks[0].DependsOn != nil || !reflect.DeepEqual(tasks[1].DependsOn, []string{"RF01"}) {
		t.Errorf("tasks = %+v", tasks)
	}
	want := "\n## Goal\n\nRefund part of a payment.\n\n## Acceptance\n\n- a refund of 5 of 10 leaves 5\n\n## Notes\n\nKeep cents.\n"
	if tasks[0].Body != want {
		t.Errorf("body =\n%q\nwant\n%q", tasks[0].Body, want)
	}
	if strings.Contains(tasks[1].Body, "## Notes") {
		t.Errorf("a task without notes has no Notes section:\n%s", tasks[1].Body)
	}
}

// What Tasks gives is what the task file Go renders from it parses back to, acceptance items
// included, also for text that looks like the file's own structure.
func TestSpecifyTasks_RoundTripThroughTheTaskFile(t *testing.T) {
	m := goodSpecify()
	first := task(m, 0)
	first["title"] = "Refund  an\tamount"
	first["goal"] = "Refund part of a payment.\n\n### Details\n\n- not an acceptance item\n\n```\n## Acceptance\n- inside a fence\n```\n"
	first["acceptance"] = []any{"[ ] boxed item", "spread\nover lines", "- dashed"}
	first["notes"] = "  - a note\n"
	a, err := ParseSpecify(marshal(t, m), []string{"billing", "gateway"}, 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range a.Tasks() {
		got, err := ParseTaskFile("TASK-"+want.ID+"-x.md", RenderTaskFile(want, a.TaskAnswers[i].Size))
		if err != nil {
			t.Fatalf("%s: %v", want.ID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: parsed back\n%#v\nwant\n%#v", want.ID, got, want)
		}
	}
}

func TestParseSpecify_Errors(t *testing.T) {
	repos := []string{"billing", "gateway"}
	cases := []struct {
		name   string
		change func(m map[string]any)
		exists bool
		field  string
	}{
		{"unknown key", func(m map[string]any) { m["status"] = "ok" }, false, "status"},
		{"unknown task key", func(m map[string]any) { task(m, 0)["priority"] = 1 }, false, "priority"},
		{"slug shape", func(m map[string]any) { m["slug"] = "Refunds" }, false, "slug"},
		{"slug starts with a dash", func(m map[string]any) { m["slug"] = "-refunds" }, false, "slug"},
		{"slug too short", func(m map[string]any) { m["slug"] = "r" }, false, "slug"},
		{"slug too long", func(m map[string]any) { m["slug"] = strings.Repeat("r", MaxSlugLen+1) }, false, "slug"},
		{"slug path", func(m map[string]any) { m["slug"] = "../refunds" }, false, "slug"},
		{"spec dir exists", func(m map[string]any) {}, true, "slug"},
		{"prefix shape", func(m map[string]any) { m["prefix"] = "R1" }, false, "prefix"},
		{"prefix length", func(m map[string]any) { m["prefix"] = "RFX" }, false, "prefix"},
		{"empty title", func(m map[string]any) { m["title"] = "" }, false, "title"},
		{"title of two lines", func(m map[string]any) { m["title"] = "a\nb" }, false, "title"},
		{"spec too long", func(m map[string]any) {
			m["spec"].(map[string]any)["approach"] = strings.Repeat("x", MaxSpecTextBytes)
		}, false, "spec"},
		{"no tasks", func(m map[string]any) { m["tasks"] = []any{} }, false, "tasks"},
		{"too many tasks", func(m map[string]any) {
			var tasks []any
			for _, id := range []string{"RF01", "RF02", "RF03"} {
				tasks = append(tasks, map[string]any{"id": id, "repo": "billing", "title": "t", "size": "S", "goal": "g"})
			}
			m["tasks"] = tasks
		}, false, "tasks"},
		{"id shape", func(m map[string]any) { task(m, 1)["id"] = "RF2" }, false, "tasks[1].id"},
		{"id prefix", func(m map[string]any) { task(m, 1)["id"] = "XX02" }, false, "tasks[1].id"},
		{"id twice", func(m map[string]any) { task(m, 1)["id"] = "RF01" }, false, "tasks[1].id"},
		{"foreign repo", func(m map[string]any) { task(m, 1)["repo"] = "ledger" }, false, "tasks[1].repo"},
		{"task title", func(m map[string]any) { task(m, 0)["title"] = " " }, false, "tasks[0].title"},
		{"size", func(m map[string]any) { task(m, 0)["size"] = "XL" }, false, "tasks[0].size"},
		{"depends on an unknown task", func(m map[string]any) { task(m, 1)["depends_on"] = []any{"RF09"} }, false, "tasks[1].depends_on"},
		{"depends on itself", func(m map[string]any) { task(m, 1)["depends_on"] = []any{"RF02"} }, false, "tasks[1].depends_on"},
		{"depends twice", func(m map[string]any) { task(m, 1)["depends_on"] = []any{"RF01", "RF01"} }, false, "tasks[1].depends_on"},
		{"empty goal", func(m map[string]any) { task(m, 0)["goal"] = "" }, false, "tasks[0].goal"},
		{"heading in the goal", func(m map[string]any) { task(m, 0)["goal"] = "g\n## Acceptance\n- forged" }, false, "tasks[0].goal"},
		{"As built in the notes", func(m map[string]any) { task(m, 0)["notes"] = "```\n## As built\n```" }, false, "tasks[0].notes"},
		{"unclosed fence in the goal", func(m map[string]any) { task(m, 0)["goal"] = "g\n```\ncode" }, false, "tasks[0].goal"},
		{"too many acceptance items", func(m map[string]any) {
			items := make([]any, MaxAcceptanceItems+1)
			for i := range items {
				items[i] = "x"
			}
			task(m, 0)["acceptance"] = items
		}, false, "tasks[0].acceptance"},
		{"long acceptance item", func(m map[string]any) {
			task(m, 0)["acceptance"] = []any{"ok", strings.Repeat("я", MaxAcceptanceRunes+1)}
		}, false, "tasks[0].acceptance[1]"},
		{"empty acceptance item", func(m map[string]any) { task(m, 0)["acceptance"] = []any{" \n "} }, false, "tasks[0].acceptance[0]"},
		{"task text too long", func(m map[string]any) { task(m, 0)["notes"] = strings.Repeat("x", MaxTaskTextBytes) }, false, "tasks[0]"},
		{"cycle", func(m map[string]any) { task(m, 0)["depends_on"] = []any{"RF02"} }, false, "tasks"},
		{"two bases in one repository", func(m map[string]any) {
			m["tasks"] = []any{
				map[string]any{"id": "RF01", "repo": "billing", "title": "a", "size": "S", "goal": "g"},
				map[string]any{"id": "RF02", "repo": "billing", "title": "b", "size": "S", "goal": "g"},
				map[string]any{"id": "RF03", "repo": "billing", "title": "c", "size": "S", "goal": "g", "depends_on": []any{"RF01", "RF02"}},
			}
		}, false, "tasks[2].depends_on"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodSpecify()
			tc.change(m)
			maxTasks := 12
			if tc.name == "too many tasks" {
				maxTasks = 2
			}
			_, err := ParseSpecify(marshal(t, m), repos, maxTasks, func(string) bool { return tc.exists })
			wantAnswerErr(t, err, tc.field)
		})
	}
}

// A value of the wrong type is named by its path, whatever encoding/json spells of it.
func TestParseSpecify_WrongTypeNamesTheField(t *testing.T) {
	m := goodSpecify()
	task(m, 1)["depends_on"] = "RF01"
	_, err := ParseSpecify(marshal(t, m), []string{"billing", "gateway"}, 12, nil)
	var aerr *AnswerError
	if !errors.As(err, &aerr) || !strings.HasSuffix(aerr.Field, "depends_on") || !strings.Contains(aerr.Msg, "want a JSON array") {
		t.Errorf("err = %#v", err)
	}
}

func TestParseSpecify_AsksAboutTheSlugItChecks(t *testing.T) {
	var asked []string
	_, err := ParseSpecify(marshal(t, goodSpecify()), []string{"billing", "gateway"}, 12, func(slug string) bool {
		asked = append(asked, slug)
		return false
	})
	if err != nil || !reflect.DeepEqual(asked, []string{"refunds"}) {
		t.Errorf("err = %v, asked = %v", err, asked)
	}
}

func TestValidSlug(t *testing.T) {
	for _, s := range []string{"refunds", "r2", "2fa-login", "a-", strings.Repeat("a", 40)} {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false", s)
		}
	}
	for _, s := range []string{"", "a", "-a", "A1", "a_b", "a.b", "a/b", strings.Repeat("a", 41)} {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true", s)
		}
	}
}

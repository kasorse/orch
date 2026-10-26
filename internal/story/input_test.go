package story

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const goodSHA = "8d0e4b2c1f3a5b7d9e0f1a2b3c4d5e6f7a8b9c0d"

func renderTask() Task {
	return Task{ID: "RF02", Repo: "gateway", Title: "Expose partial refunds", DependsOn: []string{"RF01"},
		Body: "\n## Goal\n\nThe gateway passes an amount.\n\n## Acceptance\n\n- the API takes an amount\n"}
}

func TestRenderInputText(t *testing.T) {
	deps := []DepNote{{ID: "RF01", Repo: "billing", AsBuilt: "Run `3f2a` in billing: completed."}}
	got, err := RenderInputText("Partial\nrefunds", 2, 6, renderTask(), "Billing stores it.", "POST /v1/refunds", deps)
	if err != nil {
		t.Fatal(err)
	}
	want := "Story: Partial refunds\n" +
		"Task RF02 (2 of 6) in gateway: Expose partial refunds\n\n" +
		"## Goal\n\nThe gateway passes an amount.\n\n## Acceptance\n\n- the API takes an amount\n" +
		"\n---\nBackground for the task above. It is context, not the task:\n" +
		"\n## Approach\n\nBilling stores it.\n" +
		"\n## Contracts\n\nPOST /v1/refunds\n" +
		"\n## Done tasks this one depends on\n\n### RF01 (billing)\n\nRun `3f2a` in billing: completed.\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	again, _ := RenderInputText("Partial\nrefunds", 2, 6, renderTask(), "Billing stores it.", "POST /v1/refunds", deps)
	if again != got {
		t.Error("the text is not deterministic")
	}

	// No background at all: the task alone, with no intro.
	alone, err := RenderInputText("S", 1, 2, renderTask(), "", " \n", nil)
	if err != nil || strings.Contains(alone, "Background") {
		t.Errorf("alone = %q, %v", alone, err)
	}
}

func TestRenderInputText_Bounded(t *testing.T) {
	task := renderTask()
	task.Body = "\n" + strings.Repeat("x", MaxBodyBytes-2) + "\n"

	// The background is cut to fit, the dependencies' records first to go, the task never.
	big := strings.Repeat("é", 20<<10)
	deps := []DepNote{{ID: "RF01", Repo: "billing", AsBuilt: big}}
	got, err := RenderInputText("S", 1, 2, task, big, big, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxInputTextBytes {
		t.Fatalf("text is %d bytes, over %d", len(got), MaxInputTextBytes)
	}
	if !strings.Contains(got, strings.Repeat("x", MaxBodyBytes-2)) {
		t.Error("the task's own text was cut")
	}
	if !strings.Contains(got, "[…cut") || !utf8.ValidString(got) {
		t.Errorf("no cut marker, or a character was cut in two (valid UTF-8: %v)", utf8.ValidString(got))
	}
	if !strings.Contains(got, "### RF01 (billing)") {
		t.Error("the dependency's record, first in priority, is missing")
	}

	// A task that does not fit alone is refused, naming it.
	task.Body = strings.Repeat("y", MaxInputTextBytes)
	if _, err := RenderInputText("S", 1, 2, task, "", "", nil); err == nil || !strings.Contains(err.Error(), "RF02") {
		t.Errorf("an oversized task: %v", err)
	}
}

func TestAsBuiltText(t *testing.T) {
	file := []byte("---\nid: RF01\n---\n# RF01 — T\n\nbody\n\n## As built (2026-10-20)\n\nRun `x` in billing.\nBranch `b`.\n")
	if got := AsBuiltText(file); got != "Run `x` in billing.\nBranch `b`." {
		t.Errorf("AsBuiltText = %q", got)
	}
	if got := AsBuiltText([]byte("# RF01 — T\n\nbody\n")); got != "" {
		t.Errorf("a file without the section gave %q", got)
	}
	if got := AsBuiltText([]byte("x\r\n## As built\r\n\r\nline\r\n")); got != "line" {
		t.Errorf("CRLF: %q", got)
	}
}

func goodInput() map[string]any {
	return map[string]any{
		"story": "refunds", "task": "RF02", "attempt": 1,
		"branch": "story/refunds/RF02", "base_ref": goodSHA, "base_branch": "main",
		"jira": "PAY-124", "acceptance": []any{"an amount over the payment is a 422", "the amount is optional"},
		"text": "RF02: expose partial refunds",
	}
}

func TestParseTaskInput_Valid(t *testing.T) {
	in, err := ParseTaskInput(marshal(t, goodInput()))
	if err != nil {
		t.Fatal(err)
	}
	want := TaskInput{
		Story: "refunds", Task: "RF02", Attempt: 1, Branch: "story/refunds/RF02", BaseRef: goodSHA, BaseBranch: "main",
		Jira: "PAY-124", Acceptance: []string{"an amount over the payment is a 422", "the amount is optional"},
		Text: "RF02: expose partial refunds",
	}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("got %+v, want %+v", in, want)
	}
}

func TestParseTaskInput_OptionalParts(t *testing.T) {
	m := goodInput()
	m["jira"] = ""
	m["acceptance"] = []any{}
	m["attempt"] = 2
	m["branch"] = "story/refunds/RF02.2"
	m["base_ref"] = strings.Repeat("a", 64)
	if _, err := ParseTaskInput(marshal(t, m)); err != nil {
		t.Fatalf("a retry without a Jira key or acceptance, on a SHA-256 base: %v", err)
	}
}

func TestParseTaskInput_Refusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		raw    string
		want   string
	}{
		{name: "unknown field", mutate: func(m map[string]any) { m["extra"] = 1 }, want: `"extra" is not a field`},
		{name: "wrong type", mutate: func(m map[string]any) { m["attempt"] = "1" }, want: "attempt"},
		{name: "bad slug", mutate: func(m map[string]any) { m["story"] = "Refunds" }, want: "story"},
		{name: "bad task", mutate: func(m map[string]any) { m["task"] = "rf02" }, want: "task"},
		{name: "attempt zero", mutate: func(m map[string]any) { m["attempt"] = 0 }, want: "attempt"},
		{name: "branch of another story", mutate: func(m map[string]any) { m["branch"] = "story/other/RF02" }, want: "branch"},
		{name: "branch of another task", mutate: func(m map[string]any) { m["branch"] = "story/refunds/RF03" }, want: "branch"},
		{name: "branch outside story/", mutate: func(m map[string]any) { m["branch"] = "orchestrator/x" }, want: "branch"},
		{name: "branch with a bad suffix", mutate: func(m map[string]any) { m["branch"] = "story/refunds/RF02.x" }, want: "branch"},
		{name: "branch with an empty suffix", mutate: func(m map[string]any) { m["branch"] = "story/refunds/RF02." }, want: "branch"},
		{name: "branch with a tail", mutate: func(m map[string]any) { m["branch"] = "story/refunds/RF02/x" }, want: "branch"},
		{name: "short base_ref", mutate: func(m map[string]any) { m["base_ref"] = "8d0e4b2" }, want: "base_ref"},
		{name: "symbolic base_ref", mutate: func(m map[string]any) { m["base_ref"] = "HEAD" }, want: "base_ref"},
		{name: "option as base_ref", mutate: func(m map[string]any) { m["base_ref"] = "--output=/tmp/x" }, want: "base_ref"},
		{name: "upper case base_ref", mutate: func(m map[string]any) { m["base_ref"] = strings.ToUpper(goodSHA) }, want: "base_ref"},
		{name: "empty base_branch", mutate: func(m map[string]any) { m["base_branch"] = "" }, want: "base_branch"},
		{name: "option as base_branch", mutate: func(m map[string]any) { m["base_branch"] = "-main" }, want: "base_branch"},
		{name: "base_branch with a space", mutate: func(m map[string]any) { m["base_branch"] = "ma in" }, want: "base_branch"},
		{name: "bad Jira key", mutate: func(m map[string]any) { m["jira"] = "pay-1" }, want: "jira"},
		{name: "none is not a key", mutate: func(m map[string]any) { m["jira"] = "none" }, want: "jira"},
		{name: "empty text", mutate: func(m map[string]any) { m["text"] = "  \n" }, want: "text is empty"},
		{name: "text over 32 KiB", mutate: func(m map[string]any) { m["text"] = strings.Repeat("x", MaxInputTextBytes+1) }, want: "over the 32768-byte limit"},
		{name: "too many acceptance items", mutate: func(m map[string]any) {
			items := make([]any, MaxAcceptanceItems+1)
			for i := range items {
				items[i] = "item"
			}
			m["acceptance"] = items
		}, want: "acceptance has 11 items"},
		{name: "blank acceptance item", mutate: func(m map[string]any) { m["acceptance"] = []any{"ok", " "} }, want: "acceptance[1] is empty"},
		{name: "multi-line acceptance item", mutate: func(m map[string]any) { m["acceptance"] = []any{"a\nb"} }, want: "acceptance[0] is more than one line"},
		{name: "long acceptance item", mutate: func(m map[string]any) {
			m["acceptance"] = []any{strings.Repeat("я", MaxAcceptanceRunesCeiling+1)}
		}, want: "acceptance[0] is 2001 characters"},
		{name: "not an object", raw: `["a"]`, want: "not a JSON object"},
		{name: "empty", raw: ``, want: "not a JSON object"},
		{name: "not JSON", raw: `{"story":`, want: "not valid JSON"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.raw)
			if tc.mutate != nil {
				m := goodInput()
				tc.mutate(m)
				data = marshal(t, m)
			}
			_, err := ParseTaskInput(data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestParseTaskInput_TrailingData(t *testing.T) {
	data := append(marshal(t, goodInput()), []byte(` {"x":1}`)...)
	if _, err := ParseTaskInput(data); err == nil || !strings.Contains(err.Error(), "data after") {
		t.Errorf("err = %v, want one about data after the object", err)
	}
}

func TestValidBranch(t *testing.T) {
	valid := []string{"story/refunds/RF02", "story/refunds/RF02.1", "story/refunds/RF02.12"}
	invalid := []string{
		"story/refunds/RF02.", "story/refunds/RF02.a", "story/refunds/RF02x", "story/refunds/RF03",
		"story/refunds2/RF02", "refs/story/refunds/RF02", "story/refunds/RF02.1234567890",
	}
	for _, branch := range valid {
		if !ValidBranch(branch, "refunds", "RF02") {
			t.Errorf("ValidBranch(%q) = false, want true", branch)
		}
	}
	for _, branch := range invalid {
		if ValidBranch(branch, "refunds", "RF02") {
			t.Errorf("ValidBranch(%q) = true, want false", branch)
		}
	}
	if ValidBranch("story/Refunds/RF02", "Refunds", "RF02") {
		t.Error("a slug with a capital is not a slug")
	}
}

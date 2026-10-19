package story

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRenderSpecGolden(t *testing.T) {
	spec := SpecText{
		Problem:    "  Refunds are all or nothing.\r\nA partial one needs a manual fix.\n",
		Approach:   "Billing stores the amount; the gateway exposes it.\n\n### Billing\n\n- `amount` on refund",
		OutOfScope: "Reports.",
		Risks:      " \n",
	}
	got := string(RenderSpec("Partial\nrefunds", spec))
	if again := string(RenderSpec("Partial\nrefunds", spec)); again != got {
		t.Fatal("SPEC.md is not deterministic")
	}

	path := filepath.Join("testdata", "spec.golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (go test ./internal/story -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("SPEC.md differs from %s (go test ./internal/story -update to accept):\n--- got\n%s\n--- want\n%s", path, got, want)
	}
	if strings.Contains(got, "\r") || !strings.HasSuffix(got, "—\n") {
		t.Errorf("want LF only and one final line break:\n%q", got)
	}
}

// ParseSpec reads back what RenderSpec wrote, and tolerates a hand-written file.
func TestParseSpec(t *testing.T) {
	rendered := RenderSpec("Partial\nrefunds", SpecText{
		Problem:   "Refunds are all or nothing.",
		Approach:  "Billing stores the amount.\n\n### Billing\n\n```\n## Not a section\n```\n",
		Contracts: "POST /v1/refunds takes an amount.",
	})
	title, approach, contracts := ParseSpec(rendered)
	if title != "Partial refunds" {
		t.Errorf("title = %q", title)
	}
	if want := "Billing stores the amount.\n\n### Billing\n\n```\n## Not a section\n```"; approach != want {
		t.Errorf("approach = %q, want %q", approach, want)
	}
	if contracts != "POST /v1/refunds takes an amount." {
		t.Errorf("contracts = %q", contracts)
	}

	// RenderSpec marks an empty section with a dash.
	_, approach, contracts = ParseSpec(RenderSpec("T", SpecText{Approach: "a"}))
	if approach != "a" || contracts != "" {
		t.Errorf("approach = %q, contracts = %q; want a and nothing", approach, contracts)
	}

	// A hand-written file: no title, CRLF, only some sections.
	title, approach, contracts = ParseSpec([]byte("## Approach\r\nDo it.\r\n\r\n## Risks\r\nMany.\r\n"))
	if title != "" || approach != "Do it." || contracts != "" {
		t.Errorf("hand-written: %q, %q, %q", title, approach, contracts)
	}
	if title, approach, contracts = ParseSpec(nil); title != "" || approach != "" || contracts != "" {
		t.Errorf("empty file: %q, %q, %q", title, approach, contracts)
	}
}

func TestTaskFileName(t *testing.T) {
	cases := []struct {
		id, title, want string
	}{
		{"RF01", "Refund amount in the billing model", "TASK-RF01-refund-amount-in-the-billing-model.md"},
		{"RF02", "Expose partial refunds in the public API (v2)!", "TASK-RF02-expose-partial-refunds-in-the-public-api.md"},
		{"RF03", "Expose partial refunds in the public gateway", "TASK-RF03-expose-partial-refunds-in-the-public.md"},
		{"RF04", "Сумма возврата", "TASK-RF04-task.md"},
		{"RF05", "Добавить API для billing", "TASK-RF05-api-billing.md"},
		{"RF06", " --- !!! ", "TASK-RF06-task.md"},
		{"RF07", strings.Repeat("a", 50), "TASK-RF07-" + strings.Repeat("a", 40) + ".md"},
		{"RF08", "../../etc/passwd; rm -rf /", "TASK-RF08-etc-passwd-rm-rf.md"},
	}
	for _, tc := range cases {
		got := TaskFileName(tc.id, tc.title)
		if got != tc.want {
			t.Errorf("TaskFileName(%q, %q) = %q, want %q", tc.id, tc.title, got, tc.want)
		}
		if id, err := idFromFileName(got); err != nil || id != tc.id {
			t.Errorf("%q does not read back as %s: %q %v", got, tc.id, id, err)
		}
	}
}

// A task Go writes under the name TaskFileName gives it parses back to the same task.
func TestTaskFileName_RoundTrip(t *testing.T) {
	m := goodSpecify()
	task(m, 1)["title"] = "Принимать сумму в API"
	a, err := ParseSpecify(marshal(t, m), []string{"billing", "gateway"}, 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range a.Tasks() {
		got, err := ParseTaskFile(TaskFileName(want.ID, want.Title), RenderTaskFile(want, a.TaskAnswers[i].Size))
		if err != nil {
			t.Fatalf("%s: %v", want.ID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: parsed back\n%#v\nwant\n%#v", want.ID, got, want)
		}
	}
}

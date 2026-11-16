package listener

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestRefsReadTheirLabelsLikeAProjectLabel(t *testing.T) {
	labels := []string{"ai-run", "ref:HnBI/pim/product-master.git", "ref:HnBI/pim/product-master-ui", "ref:HnBI/pim/product-master", "gitlab:HnBI/pim/sku-master"}
	got, err := Refs(labels, "ref:", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"HnBI/pim/product-master", "HnBI/pim/product-master-ui"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Refs = %q, want %q (distinct, sorted, .git stripped, gitlab: labels not read)", got, want)
	}

	if _, err := Refs(labels, "ref:", 1); !errors.Is(err, ErrManyRefs) {
		t.Errorf("two references with max 1: err = %v, want ErrManyRefs", err)
	}
	for _, bad := range []string{"ref:onlyone", "ref:a/../b", "ref:a//b", "ref:a/b c"} {
		if _, err := Refs([]string{bad}, "ref:", 3); !errors.Is(err, ErrInvalidProject) {
			t.Errorf("Refs(%q): err = %v, want ErrInvalidProject", bad, err)
		}
	}
	if got, err := Refs(labels, "", 3); err != nil || got != nil {
		t.Errorf("an empty prefix names nothing: %q, %v", got, err)
	}
}

func TestEpicProjectsAllowSeveralLabels(t *testing.T) {
	got, err := EpicProjects([]string{"gitlab:HnBI/pim/sku-master-fe", "gitlab:HnBI/pim/sku-master", "gitlab:HnBI/pim/sku-master", "ref:HnBI/pim/x"}, "gitlab:")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"HnBI/pim/sku-master", "HnBI/pim/sku-master-fe"}; !reflect.DeepEqual(got, want) {
		t.Errorf("EpicProjects = %q, want %q", got, want)
	}
	if _, err := EpicProjects([]string{"gitlab:nogroup"}, "gitlab:"); !errors.Is(err, ErrInvalidProject) {
		t.Errorf("a label that is not a project path: err = %v", err)
	}
	if got, _ := EpicProjects([]string{"ai-run"}, "gitlab:"); len(got) != 0 {
		t.Errorf("no label, no project: %q", got)
	}
}

func TestChainLinksEachServicesTasksInTheStorysOrder(t *testing.T) {
	tasks := []ChainTask{
		{"PM01", "sku-master"}, {"PM02", "sku-master-fe"}, {"PM03", "sku-master"}, {"PM04", "pim-qa"}, {"PM05", "sku-master"}, {"PM06", "sku-master-fe"},
	}
	order := []string{"PM01", "PM02", "PM03", "PM04", "PM05", "PM06"}
	want := []Link{{Blocked: "PM03", Blocker: "PM01"}, {Blocked: "PM05", Blocker: "PM03"}, {Blocked: "PM06", Blocker: "PM02"}}
	if got := Chain(tasks, order); !reflect.DeepEqual(got, want) {
		t.Errorf("Chain = %v, want %v", got, want)
	}
	// The story's order, not the IDs', is the chain's.
	if got := Chain(tasks[:3], []string{"PM03", "PM01", "PM02"}); !reflect.DeepEqual(got, []Link{{Blocked: "PM01", Blocker: "PM03"}}) {
		t.Errorf("Chain in another order = %v", got)
	}
	if got := Chain(tasks[:1], []string{"PM01"}); len(got) != 0 {
		t.Errorf("one task has no link: %v", got)
	}
	if got := Chain(tasks, nil); len(got) != 0 {
		t.Errorf("tasks outside the order have no place in a chain: %v", got)
	}
}

func TestCatalogDeduplicatesSortsAndCuts(t *testing.T) {
	long := strings.Repeat("é", 400)
	a := []domain.Project{{Path: "g/b", Name: "b", Description: "line one\nline two\x00\ttab"}, {Path: "g/a", Name: "a", Description: long}}
	b := []domain.Project{{Path: "g/b", Name: "b again"}, {Path: "g/c", Name: "c"}, {Path: "", Name: "none"}}
	got, cut := Catalog([][]domain.Project{a, b}, 10)
	if cut != 0 || len(got) != 3 || got[0].Path != "g/a" || got[1].Path != "g/b" || got[2].Path != "g/c" {
		t.Fatalf("Catalog = %v (cut %d), want g/a, g/b, g/c sorted and deduplicated", got, cut)
	}
	if n := len([]rune(got[0].Description)); n != MaxCatalogDescription {
		t.Errorf("description has %d runes, want it cut to %d", n, MaxCatalogDescription)
	}
	if got[1].Name != "b" || strings.ContainsAny(got[1].Description, "\n\x00\t") || got[1].Description != "line one line two tab" {
		t.Errorf("the first b is kept, on one line: %+v", got[1])
	}

	got, cut = Catalog([][]domain.Project{a, b}, 2)
	if cut != 1 || len(got) != 2 || got[1].Path != "g/b" {
		t.Errorf("Catalog with max 2 = %v (cut %d), want the first two by path", got, cut)
	}
}

func TestParseSelection(t *testing.T) {
	catalog := []string{"g/pim/sku-master", "g/pim/sku-master-fe", "g/pim/product-master", "g/qa/sku-master", "g/pim/pim-qa"}
	refs := []string{"g/pim/product-master"}
	const ok = `{"services":[{"path":"g/pim/sku-master","why":"the dictionary"},{"path":"g/pim/sku-master-fe","why":"the editor"}]}`
	got, err := ParseSelection([]byte(ok), catalog, refs, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Choice{{"g/pim/sku-master", "the dictionary"}, {"g/pim/sku-master-fe", "the editor"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSelection = %v, want %v", got, want)
	}

	tests := []struct{ name, answer, want string }{
		{"not an object", `[]`, "not a JSON object"},
		{"unknown key", `{"services":[{"path":"g/pim/sku-master","why":"w"}],"extra":1}`, "unknown field"},
		{"data after", `{"services":[{"path":"g/pim/sku-master","why":"w"}]} {}`, "data after"},
		{"empty", `{"services":[]}`, "services: is empty"},
		{"too many", `{"services":[{"path":"g/pim/sku-master","why":"w"},{"path":"g/pim/pim-qa","why":"w"},{"path":"g/pim/sku-master-fe","why":"w"}]}`, "more than"},
		{"outside the catalog", `{"services":[{"path":"g/pim/billing","why":"w"}]}`, "services[0].path"},
		{"a reference", `{"services":[{"path":"g/pim/product-master","why":"w"}]}`, "reference repository"},
		{"twice", `{"services":[{"path":"g/pim/sku-master","why":"w"},{"path":"g/pim/sku-master","why":"w"}]}`, "chosen twice"},
		{"same last segment", `{"services":[{"path":"g/pim/sku-master","why":"w"},{"path":"g/qa/sku-master","why":"w"}]}`, "same last segment"},
		{"no why", `{"services":[{"path":"g/pim/sku-master","why":" "}]}`, "services[0].why: is empty"},
		{"why on two lines", `{"services":[{"path":"g/pim/sku-master","why":"a\nb"}]}`, "one line"},
		{"why too long", `{"services":[{"path":"g/pim/sku-master","why":"` + strings.Repeat("x", MaxWhyRunes+1) + `"}]}`, "over the 200"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			max := 2
			_, err := ParseSelection([]byte(tc.answer), catalog, refs, max)
			var serr *SelectionError
			if !errors.As(err, &serr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a *SelectionError containing %q", err, tc.want)
			}
		})
	}
}

func TestPlanDirIsDerivedFromTheKey(t *testing.T) {
	if got := PlanDir("PMC-2636", 2); got != "plans/PMC-2636.2" {
		t.Errorf("PlanDir = %q", got)
	}
	for _, dir := range []string{"plans/PMC-1.1", "plans/PMC-1.12"} {
		if !ValidPlanDir("PMC-1", dir) {
			t.Errorf("%q is refused", dir)
		}
	}
	for _, dir := range []string{"", "plans/PMC-2.1", "plans/PMC-1.", "plans/PMC-1.0", "plans/PMC-1.01", "plans/PMC-1.1/x", "/plans/PMC-1.1", "plans/PMC-1.1234567890", "plans/PMC-1.a"} {
		if ValidPlanDir("PMC-1", dir) {
			t.Errorf("%q is accepted", dir)
		}
	}
}

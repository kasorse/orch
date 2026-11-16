package persistence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewListenerPlanNamesTheDirectoryAfterTheKeyAndNeverReusesOne(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plans")
	first, n, err := NewListenerPlan(base, "PMC-2636", 1)
	if err != nil || n != 1 || filepath.Base(first.Dir()) != "PMC-2636.1" {
		t.Fatalf("first plan = %v, %d, %v, want PMC-2636.1", first, n, err)
	}
	for _, sub := range []string{"artifacts", "logs"} {
		if info, err := os.Stat(filepath.Join(first.Dir(), sub)); err != nil || !info.IsDir() {
			t.Errorf("%s is not a directory: %v", sub, err)
		}
	}
	if err := first.SaveArtifact("select", PlanningArtifact{Output: "o", Prompt: "p"}); err != nil {
		t.Errorf("a select artifact is refused: %v", err)
	}

	// The same attempt again (a plan cut off by a stop) takes the next free number.
	second, n, err := NewListenerPlan(base, "PMC-2636", 1)
	if err != nil || n != 2 || filepath.Base(second.Dir()) != "PMC-2636.2" {
		t.Fatalf("second plan = %v, %d, %v, want PMC-2636.2", second, n, err)
	}
	if _, err := os.Stat(filepath.Join(first.Dir(), "artifacts", "select.json")); err != nil {
		t.Errorf("the first plan lost its artifact: %v", err)
	}

	if err := second.SaveFile("plan.json", map[string]int{"v": 1}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../plan.json", "", ".hidden", "a/b.json"} {
		if err := second.SaveFile(bad, 1); err == nil {
			t.Errorf("SaveFile(%q) was accepted", bad)
		}
	}
}

func TestNewListenerPlanRefusesAKeyThatIsNotOne(t *testing.T) {
	for _, key := range []string{"", "pmc-1", "../x", "PMC-1/x", "PMC", "PMC-1.2"} {
		if _, _, err := NewListenerPlan(t.TempDir(), key, 1); err == nil || !strings.Contains(err.Error(), "not an issue key") {
			t.Errorf("key %q: err = %v, want it refused", key, err)
		}
	}
}

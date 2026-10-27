package ci

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// The example is copied by teams; it must at least be YAML with the two jobs of §11.4.
func TestGitlabCIParses(t *testing.T) {
	data, err := os.ReadFile("gitlab-ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("gitlab-ci.yml: %v", err)
	}
	for _, job := range []string{"ai-implement", "ai-review"} {
		j, ok := doc[job].(map[string]any)
		if !ok {
			t.Fatalf("job %s missing", job)
		}
		if _, ok := j["script"]; !ok {
			t.Errorf("job %s has no script", job)
		}
		if _, ok := j["artifacts"]; !ok {
			t.Errorf("job %s has no artifacts", job)
		}
	}
}

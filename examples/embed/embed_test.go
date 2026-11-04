package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// yamlBlocks are the README's ```yaml fences, in order.
func yamlBlocks(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case !in && strings.TrimSpace(line) == "```yaml":
			in, cur = true, nil
		case in && strings.TrimSpace(line) == "```":
			in = false
			blocks = append(blocks, strings.Join(cur, "\n"))
		case in:
			cur = append(cur, line)
		}
	}
	if in {
		t.Fatal("README.md: an unterminated yaml fence")
	}
	return blocks
}

func hasKey(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if k == key || hasKey(c, key) {
				return true
			}
		}
	case []any:
		for _, c := range x {
			if hasKey(c, key) {
				return true
			}
		}
	}
	return false
}

// The operator copies the README's blocks; each must be YAML, and together they must hold the
// files of SPEC §5.
func TestREADMEYAMLBlocksParse(t *testing.T) {
	blocks := yamlBlocks(t)
	var docs []map[string]any
	for i, b := range blocks {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(b), &doc); err != nil {
			t.Fatalf("yaml block %d: %v\n%s", i+1, err, b)
		}
		if doc == nil {
			t.Fatalf("yaml block %d is empty", i+1)
		}
		docs = append(docs, doc)
	}

	has := func(keys ...string) bool {
		for _, d := range docs {
			ok := true
			for _, k := range keys {
				if _, found := d[k]; !found {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	if !has("include") {
		t.Error("no .gitlab-ci.yml block with the platform includes")
	}
	if !has("replicaCount", "secretKeys", "persistence", "resources", "terminationGracePeriodSeconds") {
		t.Error("no deployment values block with secretKeys, one replica, the volume, resources and the stop timeout")
	}
	if !has("jira", "listener", "gitlab", "providers") {
		t.Error("no listener.yaml block")
	}
	if !has("clarify", "actions", "test") {
		t.Error("no default.yaml block")
	}
	if !has("allowed_models", "limits") {
		t.Error("no org.yaml block")
	}
	for _, d := range docs {
		if _, ok := d["replicaCount"]; ok {
			if d["replicaCount"] != 1 {
				t.Errorf("replicaCount = %v, want 1", d["replicaCount"])
			}
			if !hasKey(d, "secretKeys") {
				t.Error("deployment values without secretKeys")
			}
		}
	}
}

// The README names the files of SPEC §5 and repeats the host's duties of §4.3 as a checklist.
func TestREADMEListsTheFilesAndTheChecklist(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"main.go", "bootstrap", "Dockerfile", "bubblewrap", ".gitlab-ci.yml", "secretKeys",
		"listener.yaml", "default.yaml", "/etc/orchestrator/org.yaml", "orchestrator.Embedded",
		"AddRunnerFunc", "example.com",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("README.md does not mention %q", want)
		}
	}
	items := regexp.MustCompile(`(?m)^- \[ \] `).FindAllString(text, -1)
	if len(items) < 8 {
		t.Errorf("the host's duties checklist has %d items, want at least 8", len(items))
	}
	for _, duty := range []string{"flock", "volume", "`git`", "`bwrap`", "toolchains", "provider CLI", "environment variable", "One replica", "stop timeout"} {
		if !strings.Contains(text, duty) {
			t.Errorf("the checklist does not mention %q", duty)
		}
	}
}

// main.go imports nothing outside this module and the standard library, and calls Embedded
// before Listen, under a signal.NotifyContext. `go vet ./...` compiles it.
func TestMainImportsAndOrder(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(p, "/")
		if strings.Contains(first, ".") && !strings.HasPrefix(p, "github.com/kasorse/orch") {
			t.Errorf("main.go imports %q: only this module and the standard library", p)
		}
	}

	pos := map[string]token.Pos{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			name := sel.Sel.Name
			if _, seen := pos[name]; !seen {
				pos[name] = call.Pos()
			}
		}
		return true
	})
	for _, name := range []string{"Embedded", "NotifyContext", "Listen"} {
		if !pos[name].IsValid() {
			t.Fatalf("main.go does not call %s", name)
		}
	}
	// Source order: Embedded (in main) first; NotifyContext before Listen (in run, which main
	// calls after Embedded).
	if !(pos["Embedded"] < pos["NotifyContext"] && pos["NotifyContext"] < pos["Listen"]) {
		t.Errorf("want Embedded, then signal.NotifyContext, then Listen; got positions %v %v %v",
			fset.Position(pos["Embedded"]), fset.Position(pos["NotifyContext"]), fset.Position(pos["Listen"]))
	}
}

package projectctx

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestDenyList(t *testing.T) {
	d := newDenyList([]string{"*.sqlite", "deploy/prod/*"})
	cases := map[string]bool{
		".env":                   true,
		".ENV":                   true,
		".env.production":        true,
		"config/.env.local":      true,
		"certs/server.pem":       true,
		"certs/server.KEY":       true,
		"credentials.json":       true,
		"aws/Credentials":        true,
		"secrets.yaml":           true,
		"secrets/db.txt":         true, // a denied directory hides everything under it
		".git/config":            true,
		".orchestrator/runs/x":   true,
		"data/app.sqlite":        true,
		"deploy/prod/values.yml": true,
		"deploy/staging/values":  false,
		"internal/keys.go":       false,
		"docs/env.md":            false,
		"environment.go":         false,
		"secret_santa.go":        false,
	}
	for p, want := range cases {
		if got := d.denied(p); got != want {
			t.Errorf("denied(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestReadText_SkipsBinaryLargeAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.go", []byte("package x\n"))
	write("bin.dat", []byte("abc\x00def"))
	write("big.txt", []byte(strings.Repeat("a", maxFileSize+1)))
	if err := os.Symlink(filepath.Join(dir, "ok.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	if _, ok := readText(dir, "ok.go"); !ok {
		t.Error("regular text file must be readable")
	}
	for _, name := range []string{"bin.dat", "big.txt", "link.go", "missing.go"} {
		if _, ok := readText(dir, name); ok {
			t.Errorf("%s must not be read", name)
		}
	}
}

func TestQueryTokens(t *testing.T) {
	q := newQueryTokens("добавь кэш в ProductRepository и load_config", "Add a cache to the ProductRepository, HTTP client and API")
	for _, want := range []string{"ProductRepository", "load_config"} {
		if !contains(q.identifiers, want) {
			t.Errorf("identifiers %v missing %s", q.identifiers, want)
		}
	}
	for _, unwanted := range []string{"HTTP", "API"} {
		if contains(q.identifiers, unwanted) {
			t.Errorf("all-caps word %s must not be an identifier", unwanted)
		}
	}
	for _, want := range []string{"product", "repository", "cache", "load", "config", "http", "client", "api"} {
		if !contains(q.words, want) {
			t.Errorf("words %v missing %s", q.words, want)
		}
	}
	for _, unwanted := range []string{"add", "the", "and"} {
		if contains(q.words, unwanted) {
			t.Errorf("stopword %s kept: %v", unwanted, q.words)
		}
	}
	if !newQueryTokens("добавь кэш", "").empty() {
		t.Error("a purely Cyrillic request has no searchable tokens")
	}
}

func TestSplitIdentifier(t *testing.T) {
	cases := map[string][]string{
		"ProductRepository":        {"product", "repository"},
		"HTTPProductRepository_v2": {"http", "product", "repository", "v2"},
		"loadConfig":               {"load", "config"},
		"snake_case_name":          {"snake", "case", "name"},
	}
	for in, want := range cases {
		if got := splitIdentifier(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWordsMatch(t *testing.T) {
	for _, pair := range [][2]string{{"cache", "caching"}, {"repository", "repositories"}, {"test", "testing"}, {"config", "config"}} {
		if !wordsMatch(pair[0], pair[1]) {
			t.Errorf("%s ~ %s should match", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"cat", "catalog"}, {"order", "ordinal"}, {"product", "protocol"}} {
		if wordsMatch(pair[0], pair[1]) {
			t.Errorf("%s ~ %s should not match", pair[0], pair[1])
		}
	}
}

func TestExtractRules(t *testing.T) {
	md := "# Title\n\nIntro text.\n\n- first rule\n- second rule\n  continues here\n* third\n1. numbered\n\n```\n- code, not a rule\n```\n\nParagraph.\n"
	want := []string{"first rule", "second rule continues here", "third", "numbered"}
	if got := extractRules(md); !reflect.DeepEqual(got, want) {
		t.Errorf("extractRules = %q, want %q", got, want)
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct{ file, content, want string }{
		{"a.go", "// Package a does things.\npackage a\n", "Package a does things."},
		{"b.go", "//go:build linux\n\n// Package b is linux-only.\npackage b\n", "Package b is linux-only."},
		{"c.go", "package c\n\nimport (\n\t\"fmt\"\n)\n\n// Thing is the main type.\ntype Thing struct{}\n", "Thing is the main type."},
		{"d.go", "package d\n\nimport \"fmt\"\n\ntype D struct{}\nfunc (D) F() {}\n", "type D struct{} func (D) F() {}"},
		{"e.py", "#!/usr/bin/env python\n# Copyright 2024 X\n# Loads the catalogue.\nimport os\n", "Loads the catalogue."},
		{"f.md", "Some intro\n# Architecture notes\n", "Architecture notes"},
	}
	for _, tc := range cases {
		if got := summarize(tc.file, tc.content); got != tc.want {
			t.Errorf("summarize(%s) = %q, want %q", tc.file, got, tc.want)
		}
	}
}

func TestTestKeys(t *testing.T) {
	pairs := [][2]string{
		{"pkg/repo.go", "pkg/repo_test.go"},
		{"src/foo.ts", "src/foo.test.ts"},
		{"src/foo.js", "src/foo.spec.js"},
		{"app/models.py", "app/test_models.py"},
		{"svc/Foo.java", "svc/FooTest.java"},
	}
	for _, p := range pairs {
		if sourceKey(p[0]) != testKey(p[1]) {
			t.Errorf("%s and %s should share a key: %q vs %q", p[0], p[1], sourceKey(p[0]), testKey(p[1]))
		}
	}
	if testKey("pkg/latest.test.ts") != "pkg/latest" {
		t.Errorf("testKey must not eat a lowercase -test suffix of the name: %q", testKey("pkg/latest.test.ts"))
	}
}

func TestManifestParsers(t *testing.T) {
	name, deps := parsePackageJSON(`{"name":"shop-ui","dependencies":{"react":"^18"},"devDependencies":{"vitest":"1"}}`)
	if name != "shop-ui" || strings.Join(deps, ",") != "react,vitest" {
		t.Errorf("package.json: %s %v", name, deps)
	}
	name, deps = parsePyproject("[project]\nname = \"shop\"\ndependencies = [\n  \"requests>=2\",\n  \"pydantic[email]==2.0\",\n]\n\n[tool.x]\nname = \"other\"\n")
	if name != "shop" || strings.Join(deps, ",") != "requests,pydantic" {
		t.Errorf("pyproject: %s %v", name, deps)
	}
	name, deps = parseCargo("[package]\nname = \"shop\"\n\n[dependencies]\nserde = \"1\"\ntokio = { version = \"1\" }\n\n[dev-dependencies]\nproptest = \"1\"\n")
	if name != "shop" || strings.Join(deps, ",") != "serde,tokio" {
		t.Errorf("Cargo.toml: %s %v", name, deps)
	}
	name, deps = parsePom("<project><parent><artifactId>parent</artifactId></parent><artifactId>shop</artifactId><dependencies><dependency><groupId>g</groupId><artifactId>guava</artifactId></dependency></dependencies></project>")
	if name != "shop" || strings.Join(deps, ",") != "guava" {
		t.Errorf("pom.xml: %s %v", name, deps)
	}
	name, deps = parseGoMod("module x\n\nrequire github.com/a/b v1.0.0\nrequire (\n\tgithub.com/c/d v1 // indirect\n\tgithub.com/e/f v2\n)\n")
	if name != "x" || strings.Join(deps, ",") != "github.com/a/b,github.com/e/f" {
		t.Errorf("go.mod: %s %v", name, deps)
	}
}

func TestApplyBudget_DropsInOrder(t *testing.T) {
	pc := &domain.ProjectContext{
		Language:      "go",
		Rules:         []string{strings.Repeat("r", 100)},
		Docs:          []domain.DocExcerpt{{Path: "README.md", Text: strings.Repeat("d", 100)}},
		RelevantFiles: []domain.RelevantFile{{Path: "a.go", Summary: strings.Repeat("a", 100)}, {Path: "b.go", Summary: strings.Repeat("b", 100)}},
	}
	// Enough room for the rules and one file, not for the docs or the second file.
	applyBudget(pc, 10, encodedSize(&domain.ProjectContext{
		Language: "go", Rules: pc.Rules, RelevantFiles: pc.RelevantFiles[:1], Truncated: true,
	}))
	if pc.Docs != nil || len(pc.RelevantFiles) != 1 || pc.RelevantFiles[0].Path != "a.go" || len(pc.Rules) != 1 || !pc.Truncated {
		t.Errorf("budget dropped the wrong things: %+v", pc)
	}
}

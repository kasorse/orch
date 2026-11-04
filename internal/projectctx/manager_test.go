package projectctx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/repository"
)

// Secret markers: none of these may appear anywhere in a collected context.
const (
	envSecret     = "DATABASE_PASSWORD=hunter2-env"
	keySecret     = "PRIVATE-KEY-MATERIAL-x-key"
	secretsYAML   = "api_token: yaml-secret-4242"
	ignoredMarker = "IGNORED-BUILD-OUTPUT ProductRepository"
	binaryMarker  = "BINARY-PAYLOAD ProductRepository"
)

var fixtureFiles = map[string]string{
	"go.mod":                               "module example.com/shop\n\ngo 1.22\n\nrequire (\n\tgithub.com/lib/pq v1.10.9\n\tgolang.org/x/sync v0.7.0 // indirect\n)\n",
	"CLAUDE.md":                            "# Shop\n\n## Rules\n\n- Repository changes require integration tests.\n- Generated files must not be modified;\n  regenerate them instead.\n\n```\n- not a rule, inside code\n```\n",
	"README.md":                            "# Shop\n\nAn example shop service.\n\n## Caching\n\nProduct lookups are slow; there is no cache layer yet.\n\n## Deployment\n\nDeployed with Helm.\n",
	"internal/product/repository.go":       "// Package product holds the product catalogue.\npackage product\n\ntype ProductRepository interface {\n\tGet(id string) (*Product, error)\n}\n",
	"internal/product/postgres.go":         "package product\n\n// PostgresProductRepository implements ProductRepository on PostgreSQL.\ntype PostgresProductRepository struct{}\n",
	"internal/product/postgres_test.go":    "//go:build integration\n\npackage product\n\nimport \"testing\"\n\nfunc TestPostgres(t *testing.T) {}\n",
	"internal/product/service.go":          "package product\n\n// Service depends on ProductRepository.\ntype Service struct{ repo ProductRepository }\n",
	"internal/order/order.go":              "// Package order handles orders.\npackage order\n",
	"internal/order/order_test.go":         "package order\n\nimport \"testing\"\n\nfunc TestOrder(t *testing.T) {}\n",
	".gitignore":                           "build/\n",
	".env":                                 envSecret + "\n",
	"certs/x.key":                          keySecret + "\n",
	"config/credentials.yaml":              secretsYAML + "\n",
	"build/output.txt":                     ignoredMarker + "\n",
	"internal/product/product_catalog.bin": binaryMarker + "\x00\x01\x02",
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// fixtureRepo builds a real git repository: everything committed except a modified
// service.go and the untracked (not ignored) .env.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for rel, content := range fixtureFiles {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "--", ".gitignore", "go.mod", "CLAUDE.md", "README.md", "internal", "certs", "config")
	git(t, dir, "commit", "-q", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(dir, "internal/product/service.go"), []byte(fixtureFiles["internal/product/service.go"]+"// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newFixtureManager(t *testing.T, dir string, opts Options) *Manager {
	t.Helper()
	repo, err := repository.Open(context.Background(), dir, execution.NewOSProcessExecutor())
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(repo, dir, opts)
}

func buildJSON(t *testing.T, m *Manager, request, requestEN string) (*domain.ProjectContext, string) {
	t.Helper()
	pc, err := m.Build(context.Background(), request, requestEN)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := json.Marshal(pc)
	if err != nil {
		t.Fatal(err)
	}
	return pc, string(raw)
}

func paths(files []domain.RelevantFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func TestBuild_NeverLeaksSecretsIgnoredOrBinaryFiles(t *testing.T) {
	dir := fixtureRepo(t)
	m := newFixtureManager(t, dir, Options{MaxFiles: 20, MaxBytes: 1 << 20})
	// Name every sensitive file in the request to make sure matching can't pull them in.
	_, raw := buildJSON(t, m, "добавь кэш в ProductRepository", "add a cache to ProductRepository; check env key credentials yaml build output product catalog bin")

	for _, forbidden := range []string{
		envSecret, keySecret, secretsYAML, ignoredMarker, binaryMarker,
		".env", "x.key", "credentials.yaml", "build/output.txt", "product_catalog.bin",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("context contains %q:\n%s", forbidden, raw)
		}
	}
}

func TestBuild_RussianRequestFindsIdentifier(t *testing.T) {
	dir := fixtureRepo(t)
	m := newFixtureManager(t, dir, Options{MaxFiles: 20, MaxBytes: 1 << 20})
	// No translation: only the identifier survives, and it is enough.
	pc, _ := buildJSON(t, m, "добавь кэш в ProductRepository", "")

	got := paths(pc.RelevantFiles)
	for _, want := range []string{"internal/product/repository.go", "internal/product/postgres.go", "internal/product/service.go"} {
		if !contains(got, want) {
			t.Errorf("relevant files %v do not include %s", got, want)
		}
	}
	if contains(got, "internal/order/order.go") {
		t.Errorf("unrelated file selected: %v", got)
	}
	if got[0] != "internal/product/repository.go" {
		t.Errorf("most relevant = %s, want the file named after both identifier parts", got[0])
	}

	// postgres_test.go follows postgres.go as its related test.
	i := indexOf(got, "internal/product/postgres.go")
	if i < 0 || i+1 >= len(got) || got[i+1] != "internal/product/postgres_test.go" {
		t.Errorf("related test does not follow its source: %v", got)
	}
	for _, f := range pc.RelevantFiles {
		if f.Path == "internal/product/postgres_test.go" && f.Purpose != domain.PurposeTest {
			t.Errorf("test purpose = %q", f.Purpose)
		}
		if f.Path == "internal/product/repository.go" && f.Summary != "Package product holds the product catalogue." {
			t.Errorf("summary = %q", f.Summary)
		}
	}
}

func TestBuild_MetadataRulesDocsTestsGit(t *testing.T) {
	dir := fixtureRepo(t)
	m := newFixtureManager(t, dir, Options{MaxFiles: 20, MaxBytes: 1 << 20})
	pc, _ := buildJSON(t, m, "добавь кэш в ProductRepository", "add a cache to ProductRepository")

	if pc.Language != "go" || pc.ProjectType != "go-module" || pc.Module != "example.com/shop" {
		t.Errorf("metadata = %s/%s/%s", pc.Language, pc.ProjectType, pc.Module)
	}
	if strings.Join(pc.Dependencies, ",") != "github.com/lib/pq" {
		t.Errorf("dependencies = %v, want direct requirements only", pc.Dependencies)
	}
	wantRules := []string{
		"Repository changes require integration tests.",
		"Generated files must not be modified; regenerate them instead.",
	}
	if strings.Join(pc.Rules, "|") != strings.Join(wantRules, "|") {
		t.Errorf("rules = %q, want %q", pc.Rules, wantRules)
	}
	if len(pc.Docs) != 1 || pc.Docs[0].Section != "Caching" || !strings.Contains(pc.Docs[0].Text, "no cache layer") {
		t.Errorf("docs = %+v, want only the Caching section", pc.Docs)
	}
	if !pc.TestInfo.Exists || !pc.TestInfo.UnitTests || !pc.TestInfo.IntegrationTests {
		t.Errorf("test info = %+v", pc.TestInfo)
	}
	if pc.Git.Branch != "main" || !pc.Git.Dirty {
		t.Errorf("git = %+v", pc.Git)
	}
	if strings.Join(pc.Git.ChangedFiles, ",") != "internal/product/service.go" {
		t.Errorf("changed files = %v, want service.go only (.env is untracked but denied)", pc.Git.ChangedFiles)
	}
	if pc.Truncated {
		t.Error("nothing should be truncated with a generous budget")
	}
}

func TestBuild_BudgetIsRespected(t *testing.T) {
	dir := fixtureRepo(t)

	m := newFixtureManager(t, dir, Options{MaxFiles: 2, MaxBytes: 1 << 20})
	pc, _ := buildJSON(t, m, "ProductRepository", "")
	if len(pc.RelevantFiles) != 2 || !pc.Truncated {
		t.Errorf("max_files=2: got %d files, truncated=%v", len(pc.RelevantFiles), pc.Truncated)
	}

	const maxBytes = 500
	m = newFixtureManager(t, dir, Options{MaxFiles: 20, MaxBytes: maxBytes})
	pc, raw := buildJSON(t, m, "ProductRepository cache", "")
	if len(raw) > maxBytes {
		t.Errorf("encoded context is %d bytes, budget %d", len(raw), maxBytes)
	}
	if !pc.Truncated {
		t.Error("expected truncated")
	}
	if pc.Language != "go" {
		t.Error("metadata must survive the budget")
	}
}

func TestBuild_ExtraDeny(t *testing.T) {
	dir := fixtureRepo(t)
	m := newFixtureManager(t, dir, Options{MaxFiles: 20, MaxBytes: 1 << 20, ExtraDeny: []string{"internal/product/postgres*"}})
	pc, raw := buildJSON(t, m, "ProductRepository", "")
	if strings.Contains(raw, "postgres") {
		t.Errorf("extra_deny pattern not applied: %v", paths(pc.RelevantFiles))
	}
}

type failingRepo struct{ err error }

func (f failingRepo) ListFiles(context.Context) ([]string, error)    { return nil, f.err }
func (f failingRepo) Branch(context.Context) (string, error)         { return "", f.err }
func (f failingRepo) ChangedFiles(context.Context) ([]string, error) { return nil, f.err }

func TestBuild_ListFailureIsAnError(t *testing.T) {
	cause := errors.New("not a git repository")
	_, err := NewManager(failingRepo{cause}, t.TempDir(), Options{MaxFiles: 5, MaxBytes: 1000}).
		Build(context.Background(), "x", "")
	if !errors.Is(err, cause) {
		t.Errorf("err = %v, want it to wrap the listing failure", err)
	}
}

func contains(ss []string, s string) bool { return indexOf(ss, s) >= 0 }

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

func TestBuild_NamedFileCarriesItsContent(t *testing.T) {
	dir := fixtureRepo(t)
	m := newFixtureManager(t, dir, Options{MaxFiles: 20, MaxBytes: 1 << 20})
	pc, raw := buildJSON(t, m, "implement the task in `internal/order/order.go`; see .env and config/credentials.yaml", "")

	if got := pc.RelevantFiles[0]; got.Path != "internal/order/order.go" || got.Content != fixtureFiles["internal/order/order.go"] {
		t.Errorf("first relevant file = %+v, want order.go with its content", got)
	}
	for _, f := range pc.RelevantFiles[1:] {
		if f.Content != "" {
			t.Errorf("%s is not named but carries content", f.Path)
		}
	}
	for _, forbidden := range []string{envSecret, secretsYAML} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("a named deny-listed file leaked %q", forbidden)
		}
	}
}

func TestNamedFiles(t *testing.T) {
	files := []string{"a/b.md", "b.md", "a/b.md.bak"}
	for request, want := range map[string]string{
		"do a/b.md":      "a/b.md",
		"do ./a/b.md.":   "a/b.md",
		"do `a/b.md`":    "a/b.md",
		"do x/a/b.md":    "",
		"do a/b.md.bak":  "a/b.md.bak",
		"do b.md please": "b.md",
		"do a/b.mdx":     "",
	} {
		got := namedFiles(request, files)
		if (want == "" && len(got) != 0) || (want != "" && (len(got) != 1 || !got[want])) {
			t.Errorf("namedFiles(%q) = %v, want %q", request, got, want)
		}
	}
}

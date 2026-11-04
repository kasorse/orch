package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/apperr"
)

// A missing configuration is a ConfigError before any request goes out.
func TestListen_MissingConfigurationIsAConfigErrorBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	// The configuration that would have named the server is missing: nothing can reach it.
	dir := t.TempDir()
	for name, o := range map[string]Options{
		"file does not exist": {Dir: dir, Config: filepath.Join(dir, "nope.yaml"), Once: true},
		"no file named":       {Dir: dir, Once: true},
	} {
		var stderr bytes.Buffer
		o.Stderr = &stderr
		err := Listen(context.Background(), o)
		var ce *apperr.ConfigError
		if !errors.As(err, &ce) {
			t.Errorf("%s: Listen returned %v, want a ConfigError", name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the server was hit %d times", n)
	}
	if _, err := os.Stat(filepath.Join(dir, ".orchestrator")); err == nil {
		t.Errorf("a failed Listen left a state directory in Dir")
	}
}

func TestEmbedded_UnknownWordIsNotHandled(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, args := range [][]string{nil, {}, {"serve"}, {"--flag"}, {"--config", "x.yaml", "serve"}} {
		code, ok := Embedded(args, strings.NewReader(""), &out, &errOut)
		if ok || code != 0 {
			t.Errorf("%q: got (%d, %v), want (0, false)", args, code, ok)
		}
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("a word that is not a command wrote output: %q %q", out.String(), errOut.String())
	}
}

func TestEmbedded_VersionIsTheCLIsExitCode(t *testing.T) {
	var out, errOut bytes.Buffer
	code, ok := Embedded([]string{"version"}, nil, &out, &errOut)
	if !ok || code != 0 {
		t.Fatalf("got (%d, %v), want (0, true); stderr: %s", code, ok, errOut.String())
	}
	if want := "orchestrator version " + app.Version + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestEmbedded_UsageErrorIsExit2(t *testing.T) {
	var out, errOut bytes.Buffer
	code, ok := Embedded([]string{"git-credential", "fill"}, strings.NewReader(""), &out, &errOut)
	if !ok || code != 2 {
		t.Errorf("got (%d, %v), want (2, true)", code, ok)
	}
	if errOut.Len() == 0 {
		t.Errorf("the error was not reported on stderr")
	}
}

// git starts the credential helper as `<exe> --config <path> git-credential get`; the host's
// main hands it to Embedded, which answers for gitlab.base_url only.
func TestEmbedded_GitCredentialGet(t *testing.T) {
	// A fake credential, assembled at run time.
	secret := strings.Repeat("x", 20)
	t.Setenv("EMBED_TEST_GITLAB_TOKEN", secret)
	cfg := filepath.Join(t.TempDir(), "listener.yaml")
	yaml := "version: 2\ngitlab:\n  base_url: https://gitlab.test\n  token_env: EMBED_TEST_GITLAB_TOKEN\n  username: oauth2\n  allowed_groups: [group]\n"
	if err := os.WriteFile(cfg, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code, ok := Embedded([]string{"--config", cfg, "git-credential", "get"}, strings.NewReader("protocol=https\nhost=gitlab.test\n\n"), &out, &errOut)
	if !ok || code != 0 {
		t.Fatalf("got (%d, %v), want (0, true); stderr: %s", code, ok, errOut.String())
	}
	if want := "username=oauth2\npassword=" + secret + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if strings.Contains(errOut.String(), secret) {
		t.Errorf("the credential is on stderr")
	}

	// Another host gets nothing.
	out.Reset()
	code, ok = Embedded([]string{"--config", cfg, "git-credential", "get"}, strings.NewReader("protocol=https\nhost=other.test\n\n"), &out, &errOut)
	if !ok || code != 0 || out.Len() != 0 {
		t.Errorf("another host: got (%d, %v) and %q, want (0, true) and nothing", code, ok, out.String())
	}
}

// The package imports nothing but internal/app, internal/exitcode and the standard library, and
// exports exactly Options, Listen and Embedded.
func TestPackageImportsAndExports(t *testing.T) {
	allowed := map[string]bool{
		"github.com/kasorse/orch/internal/app":      true,
		"github.com/kasorse/orch/internal/exitcode": true,
	}
	fset := token.NewFileSet()
	var files int
	var exported []string
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Errorf("%s: bad import %s", name, imp.Path.Value)
				continue
			}
			first, _, _ := strings.Cut(p, "/")
			if strings.Contains(first, ".") && !allowed[p] {
				t.Errorf("%s imports %q: the public package may import only internal/app, internal/exitcode and the standard library", name, p)
			}
		}
		for n := range f.Scope.Objects {
			if token.IsExported(n) {
				exported = append(exported, n)
			}
		}
	}
	if files == 0 {
		t.Fatal("found no Go files to check")
	}
	sort.Strings(exported)
	if got, want := strings.Join(exported, ","), "Embedded,Listen,Options"; got != want {
		t.Errorf("exported = %s, want %s", got, want)
	}
}

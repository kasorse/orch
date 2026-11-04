package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
)

// writeOrg writes content as org.yaml in a fresh directory and returns its path.
func writeOrg(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "org.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write org config: %v", err)
	}
	return path
}

// layerYAML merges a version 2 repo file holding repoBody with a version 1 org file holding
// orgBody, both written under t.TempDir().
func layerYAML(t *testing.T, repoBody, orgBody string) (cfg Config, repoPath, orgPath string, err error) {
	t.Helper()
	repoPath = writeTempConfig(t, "version: 2\n"+repoBody)
	orgPath = writeOrg(t, "version: 1\n"+orgBody)
	cfg, _, err = LoadLayered(repoPath, orgPath)
	return cfg, repoPath, orgPath, err
}

// orgRow is one row of SPEC §6.2: a repository body against an org body. wantErr is the
// key the conflict names; empty means the merge succeeds and check inspects the result.
type orgRow struct {
	name    string
	repo    string
	org     string
	wantErr string
	check   func(t *testing.T, cfg Config)
}

func runOrgRows(t *testing.T, rows []orgRow) {
	t.Helper()
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			cfg, repoPath, orgPath, err := layerYAML(t, tc.repo, tc.org)
			if tc.wantErr != "" {
				var ce *apperr.ConfigError
				if !errors.As(err, &ce) {
					t.Fatalf("expected a ConfigError, got %T: %v", err, err)
				}
				for _, want := range []string{tc.wantErr, repoPath, orgPath} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error lacks %q:\n%v", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestLoadLayered_RequireCleanWorktree(t *testing.T) {
	runOrgRows(t, []orgRow{
		{name: "repo false is weaker", repo: "security:\n  require_clean_worktree: false\n",
			org: "security:\n  require_clean_worktree: true\n", wantErr: "security.require_clean_worktree: false is weaker"},
		{name: "repo true", repo: "security:\n  require_clean_worktree: true\n",
			org: "security:\n  require_clean_worktree: true\n",
			check: func(t *testing.T, cfg Config) {
				if !cfg.Security.CleanWorktreeRequired() {
					t.Error("clean worktree not required")
				}
			}},
		{name: "repo absent", repo: "", org: "security:\n  require_clean_worktree: true\n",
			check: func(t *testing.T, cfg Config) {
				if r := cfg.Security.RequireCleanWorktree; r == nil || !*r {
					t.Errorf("require_clean_worktree = %v, want true", r)
				}
			}},
	})
}

func TestLoadLayered_ProtectedPaths(t *testing.T) {
	runOrgRows(t, []orgRow{
		{name: "org entries first, deduped", repo: "security:\n  protected_paths: [\"b/**\", \"a/**\", \"c.txt\"]\n",
			org: "security:\n  protected_paths: [\"a/**\", \"x.txt\"]\n",
			check: func(t *testing.T, cfg Config) {
				want := []string{"a/**", "x.txt", "b/**", "c.txt"}
				if !reflect.DeepEqual(cfg.Security.ProtectedPaths, want) {
					t.Errorf("protected_paths = %v, want %v", cfg.Security.ProtectedPaths, want)
				}
			}},
	})
}

func TestLoadLayered_AllowUnverifiedCLI(t *testing.T) {
	runOrgRows(t, []orgRow{
		{name: "repo true is weaker", repo: "security:\n  allow_unverified_cli: true\n",
			org: "security:\n  allow_unverified_cli: false\n", wantErr: "security.allow_unverified_cli: true is weaker"},
		{name: "repo false", repo: "security:\n  allow_unverified_cli: false\n",
			org: "security:\n  allow_unverified_cli: false\n",
			check: func(t *testing.T, cfg Config) {
				if cfg.Security.AllowUnverifiedCLI {
					t.Error("allow_unverified_cli = true")
				}
			}},
		{name: "repo absent", repo: "", org: "security:\n  allow_unverified_cli: false\n",
			check: func(t *testing.T, cfg Config) {
				if cfg.Security.AllowUnverifiedCLI {
					t.Error("allow_unverified_cli = true")
				}
			}},
	})
}

func TestLoadLayered_SecretScanMode(t *testing.T) {
	keeps := func(want string) func(t *testing.T, cfg Config) {
		return func(t *testing.T, cfg Config) {
			if cfg.Security.SecretScan.Mode != want {
				t.Errorf("mode = %q, want %q", cfg.Security.SecretScan.Mode, want)
			}
		}
	}
	scan := func(mode string) string { return "security:\n  secret_scan:\n    mode: " + mode + "\n" }
	runOrgRows(t, []orgRow{
		{name: "warn under stop", repo: scan("warn"), org: scan("stop"), wantErr: "security.secret_scan.mode: warn is weaker"},
		{name: "off under warn", repo: scan("off"), org: scan("warn"), wantErr: "security.secret_scan.mode: off is weaker"},
		{name: "absent means stop, equal to stop", repo: "", org: scan("stop"), check: keeps("")},
		{name: "absent means stop, stricter than warn", repo: "", org: scan("warn"), check: keeps("")},
		{name: "equal", repo: scan("warn"), org: scan("warn"), check: keeps("warn")},
		{name: "stricter", repo: scan("stop"), org: scan("warn"), check: keeps("stop")},
		{name: "unknown repo value is left to Validate", repo: scan("bogus"), org: scan("warn"), check: keeps("bogus")},
	})
}

func TestLoadLayered_SecretScanAllowPaths(t *testing.T) {
	runOrgRows(t, []orgRow{
		{name: "repo sets its own", repo: "security:\n  secret_scan:\n    allow_paths: [\"testdata/**\"]\n",
			org:     "security:\n  secret_scan:\n    mode: warn\n    allow_paths: [\"fixtures/**\"]\n",
			wantErr: "security.secret_scan.allow_paths is set by the org config"},
		{name: "org list is the merged list", repo: "",
			org: "security:\n  secret_scan:\n    mode: warn\n    allow_paths: [\"fixtures/**\"]\n",
			check: func(t *testing.T, cfg Config) {
				if want := []string{"fixtures/**"}; !reflect.DeepEqual(cfg.Security.SecretScan.AllowPaths, want) {
					t.Errorf("allow_paths = %v, want %v", cfg.Security.SecretScan.AllowPaths, want)
				}
			}},
		{name: "org without secret_scan leaves the repository's", repo: "security:\n  secret_scan:\n    allow_paths: [\"testdata/**\"]\n",
			org: "security:\n  require_clean_worktree: true\n",
			check: func(t *testing.T, cfg Config) {
				if want := []string{"testdata/**"}; !reflect.DeepEqual(cfg.Security.SecretScan.AllowPaths, want) {
					t.Errorf("allow_paths = %v, want %v", cfg.Security.SecretScan.AllowPaths, want)
				}
			}},
	})
}

func TestLoadLayered_Sandbox(t *testing.T) {
	keeps := func(want string) func(t *testing.T, cfg Config) {
		return func(t *testing.T, cfg Config) {
			if cfg.Test.Sandbox != want || !cfg.Test.Sandboxed() {
				t.Errorf("sandbox = %q (sandboxed %v), want %q", cfg.Test.Sandbox, cfg.Test.Sandboxed(), want)
			}
		}
	}
	runOrgRows(t, []orgRow{
		{name: "repo none is weaker", repo: "test:\n  sandbox: none\n", org: "test:\n  sandbox: bwrap\n", wantErr: "test.sandbox: none is weaker"},
		{name: "repo bwrap", repo: "test:\n  sandbox: bwrap\n", org: "test:\n  sandbox: bwrap\n", check: keeps("bwrap")},
		{name: "repo absent", repo: "", org: "test:\n  sandbox: bwrap\n", check: keeps("")},
	})
}

func TestLoadLayered_PolicyPaths(t *testing.T) {
	runOrgRows(t, []orgRow{
		{name: "org rules first, then the repository's",
			repo: "policy:\n  paths:\n    - match: \"docs/**\"\n      require_stages: [review]\n",
			org:  "policy:\n  paths:\n    - match: \"migrations/**\"\n      approval: manual\n",
			check: func(t *testing.T, cfg Config) {
				var got []string
				for _, p := range cfg.Policy.Paths {
					got = append(got, p.Match)
				}
				if want := []string{"migrations/**", "docs/**"}; !reflect.DeepEqual(got, want) {
					t.Errorf("rules = %v, want %v", got, want)
				}
			}},
	})
}

func TestLoadLayered_Force(t *testing.T) {
	runOrgRows(t, []orgRow{
		{name: "per complexity union keeps order",
			repo: "policy:\n  stages:\n    force:\n      complex: [investigation, review]\n      medium: [testing]\n",
			org:  "policy:\n  stages:\n    force:\n      complex: [review, research]\n      simple: [testing]\n",
			check: func(t *testing.T, cfg Config) {
				want := map[string][]string{
					"complex": {"review", "research", "investigation"},
					"medium":  {"testing"},
					"simple":  {"testing"},
				}
				if !reflect.DeepEqual(cfg.Policy.Stages.Force, want) {
					t.Errorf("force = %v, want %v", cfg.Policy.Stages.Force, want)
				}
			}},
	})
}

// The org file's template is checked on its own, before any merge: analysis and planning are
// refused there as in a repository file, naming specs/clarify.
func TestOrgProblems_RefusesAnalysisInTheTemplate(t *testing.T) {
	org := OrgConfig{Policy: OrgPolicy{Stages: OrgStages{Force: map[string][]string{"complex": {"analysis", "planning", "review"}}}}}
	got := strings.Join(orgProblems(org), "\n")
	for _, want := range []string{"policy.stages.force.complex: analysis is not a stage any more",
		"policy.stages.force.complex: planning was renamed investigation", "specs/clarify"} {
		if !strings.Contains(got, want) {
			t.Errorf("orgProblems lacks %q:\n%s", want, got)
		}
	}
}

func TestLoadLayered_MaxCostUSD(t *testing.T) {
	cost := func(v string) string { return "limits:\n  max_cost_usd: " + v + "\n" }
	keeps := func(want float64) func(t *testing.T, cfg Config) {
		return func(t *testing.T, cfg Config) {
			if v := cfg.Limits.MaxCostUSD; v == nil || *v != want {
				t.Errorf("max_cost_usd = %v, want %v", v, want)
			}
		}
	}
	runOrgRows(t, []orgRow{
		{name: "above the ceiling", repo: cost("20"), org: cost("5"), wantErr: "limits.max_cost_usd 20 is above the org's 5"},
		{name: "equal", repo: cost("5"), org: cost("5"), check: keeps(5)},
		{name: "lower is kept", repo: cost("2.5"), org: cost("5"), check: keeps(2.5)},
		{name: "absent inherits the ceiling", repo: "", org: cost("5"), check: keeps(5)},
	})
}

func TestLoadLayered_JoinsEveryConflict(t *testing.T) {
	_, repoPath, orgPath, err := layerYAML(t,
		"security:\n  require_clean_worktree: false\n  allow_unverified_cli: true\ntest:\n  sandbox: none\n",
		"security:\n  require_clean_worktree: true\n  allow_unverified_cli: false\ntest:\n  sandbox: bwrap\n")
	if err == nil {
		t.Fatal("expected conflicts")
	}
	for _, want := range []string{"require_clean_worktree", "allow_unverified_cli", "test.sandbox", repoPath, orgPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestLoadLayered_AllowedModelsComeFromTheOrgOnly(t *testing.T) {
	cfg, _, orgPath, err := layerYAML(t, "", "allowed_models: [sonnet, opus]\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sonnet", "opus"}; !reflect.DeepEqual(cfg.AllowedModels, want) || cfg.AllowedModelsFrom != orgPath {
		t.Errorf("allowed_models = %v from %q", cfg.AllowedModels, cfg.AllowedModelsFrom)
	}

	repo := writeTempConfig(t, "version: 2\nallowed_models: [opus]\n")
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "allowed_models") {
		t.Errorf("a repository file must not set allowed_models, got %v", err)
	}
}

func TestLoadLayered_RecordsPathAndHash(t *testing.T) {
	repoPath := writeTempConfig(t, "version: 2\n")
	orgContent := "version: 1\nsecurity:\n  require_clean_worktree: true\n"
	orgPath := writeOrg(t, orgContent)
	_, src, err := LoadLayered(repoPath, orgPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(orgContent))
	if src.Path != orgPath || src.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("source = %+v, want %s %x", src, orgPath, sum)
	}
}

func TestLoadLayered_RelativeOrgPathIsRecordedAbsolute(t *testing.T) {
	repoPath := writeTempConfig(t, "version: 2\n")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "org.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	_, src, err := LoadLayered(repoPath, "org.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(src.Path) || filepath.Base(src.Path) != "org.yaml" {
		t.Errorf("path = %q, want an absolute path", src.Path)
	}
}

func TestLoadLayered_NoOrgEqualsLoad(t *testing.T) {
	repoPath := writeTempConfig(t, validV2(""))
	want, err := Load(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	got, src, err := LoadLayered(repoPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || src != (OrgSource{}) {
		t.Errorf("LoadLayered with no org differs from Load: %+v, %+v", got, src)
	}
}

func TestLoadLayered_RepoErrorStillReported(t *testing.T) {
	orgPath := writeOrg(t, "version: 1\n")
	if _, _, err := LoadLayered(filepath.Join(t.TempDir(), "missing.yaml"), orgPath); err == nil {
		t.Fatal("expected the repository read error")
	}
}

func TestLoadLayered_OrgFileErrors(t *testing.T) {
	dirPath := t.TempDir()
	unreadable := writeOrg(t, "version: 1\n")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })

	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"a directory":           {dirPath, "failed to read org config file"},
		"missing file":          {filepath.Join(dirPath, "nope.yaml"), "failed to read org config file"},
		"invalid yaml":          {writeOrg(t, "version: 1\nsecurity: [unclosed\n"), "failed to parse org config"},
		"unknown top-level key": {writeOrg(t, "version: 1\nauto_commit: true\n"), "auto_commit"},
		"thresholds":            {writeOrg(t, "version: 1\npolicy:\n  stages:\n    thresholds: {review: 0.7}\n"), "thresholds"},
		"unknown section key":   {writeOrg(t, "version: 1\nlimits:\n  max_steps: 3\n"), "max_steps"},
		"version 0":             {writeOrg(t, "security: {}\n"), "version must be 1, got 0"},
		"version 2":             {writeOrg(t, "version: 2\n"), "version must be 1, got 2"},
		"empty file":            {writeOrg(t, ""), "version must be 1, got 0"},
	} {
		t.Run(name, func(t *testing.T) {
			repoPath := writeTempConfig(t, "version: 2\n")
			cfg, src, err := LoadLayered(repoPath, tc.path)
			var ce *apperr.ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("expected a ConfigError, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.path) {
				t.Errorf("error lacks %q or the path %q:\n%v", tc.want, tc.path, err)
			}
			if !reflect.DeepEqual(cfg, Config{}) || src != (OrgSource{}) {
				t.Error("a config must not be returned without the org layer")
			}
		})
	}

	t.Run("unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads anything")
		}
		repoPath := writeTempConfig(t, "version: 2\n")
		if _, _, err := LoadLayered(repoPath, unreadable); err == nil || !strings.Contains(err.Error(), unreadable) {
			t.Fatalf("expected an error naming %s, got %v", unreadable, err)
		}
	})
}

func TestLoadOrg_RejectsValuesThatLowerAFloor(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"clean worktree false": {"security:\n  require_clean_worktree: false\n", "security.require_clean_worktree may only be true"},
		"unverified cli true":  {"security:\n  allow_unverified_cli: true\n", "security.allow_unverified_cli may only be false"},
		"sandbox none":         {"test:\n  sandbox: none\n", "test.sandbox may only be bwrap"},
		"bad mode":             {"security:\n  secret_scan:\n    mode: loud\n", "security.secret_scan.mode must be off, warn or stop"},
		"bad protected path":   {"security:\n  protected_paths: [\"//abs\"]\n", "security.protected_paths[0]"},
		"bad allow path":       {"security:\n  secret_scan:\n    allow_paths: [\"\"]\n", "security.secret_scan.allow_paths[0]"},
		"bad rule":             {"policy:\n  paths:\n    - match: \"a/**\"\n", "policy.paths[0] is empty"},
		"bad force complexity": {"policy:\n  stages:\n    force:\n      huge: [review]\n", `unknown complexity "huge"`},
		"bad force stage":      {"policy:\n  stages:\n    force:\n      complex: [nap]\n", `unknown stage "nap"`},
		"zero budget":          {"limits:\n  max_cost_usd: 0\n", "limits.max_cost_usd must be positive and finite"},
		"empty allowlist":      {"allowed_models: []\n", "allowed_models must name at least one model"},
		"empty model name":     {"allowed_models: [\"\"]\n", "allowed_models[0] must not be empty"},
		"duplicate model":      {"allowed_models: [opus, opus]\n", `allowed_models[1] "opus" is listed twice`},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeOrg(t, "version: 1\n"+tc.body)
			_, _, err := loadOrg(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %v, want %q naming %s", err, tc.want, path)
			}
		})
	}
}

func TestLoadOrg_AcceptsTheStrictSide(t *testing.T) {
	path := writeOrg(t, `version: 1
security:
  require_clean_worktree: true
  protected_paths: ["infra/**"]
  allow_unverified_cli: false
  secret_scan:
    mode: stop
    allow_paths: ["testdata/**"]
test:
  sandbox: bwrap
policy:
  paths:
    - match: "migrations/**"
      approval: manual
  stages:
    force:
      complex: [review]
limits:
  max_cost_usd: 5
allowed_models: [sonnet, opus]
`)
	org, sum, err := loadOrg(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum) != 64 || org.Version != 1 || org.Security.RequireCleanWorktree == nil || !*org.Security.RequireCleanWorktree {
		t.Errorf("org = %+v, sum %q", org, sum)
	}
}

func TestFindOrgConfig(t *testing.T) {
	dir := t.TempDir()
	etc := filepath.Join(dir, "org.yaml")
	getenv := func(v string) string {
		if v == OrgConfigEnv {
			return "/from/env.yaml"
		}
		return ""
	}
	none := func(string) string { return "" }

	if _, ok := FindOrgConfig(etc, none); ok {
		t.Error("neither file nor variable: want none")
	}
	if p, ok := FindOrgConfig(etc, getenv); !ok || p != "/from/env.yaml" {
		t.Errorf("/etc absent: got %q, %v, want the variable", p, ok)
	}
	if err := os.WriteFile(etc, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, ok := FindOrgConfig(etc, getenv); !ok || p != etc {
		t.Errorf("/etc present: got %q, %v, want %q even with the variable set", p, ok, etc)
	}
	if p, ok := FindOrgConfig(etc, none); !ok || p != etc {
		t.Errorf("/etc present, no variable: got %q, %v", p, ok)
	}
}

func TestFindOrgConfig_StatErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plain")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A path below a regular file fails with ENOTDIR, not "does not exist".
	etc := filepath.Join(file, "org.yaml")
	if p, ok := FindOrgConfig(etc, func(string) string { return "/from/env.yaml" }); !ok || p != etc {
		t.Errorf("got %q, %v, want %q so that loading fails instead of using the variable", p, ok, etc)
	}
}

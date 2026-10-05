package config

import (
	"strings"
	"testing"
	"time"
)

// validConfig returns a fully valid version 2 config, matching the example in
// specs/JEV integration/TASKS.md T02/T22.
func validConfig() Config {
	return Config{
		Version: CurrentVersion,
		Actions: ActionsConfig{
			Research:  ActionConfig{ProviderModel: ProviderModel{Provider: "gemini", Model: "pro"}},
			Analyze:   ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
			Plan:      ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
			Implement: ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "opus"}},
			Review:    ActionConfig{ProviderModel: ProviderModel{Provider: "codex", Model: "gpt"}},
		},
		Test:      TestConfig{Command: "go test ./..."},
		Limits:    LimitsConfig{MaxSteps: 12, MaxFixIterations: 2},
		Execution: ExecutionConfig{Timeout: 15 * time.Minute},
		Decision: DecisionConfig{
			Provider:   DecisionProviderJev,
			Model:      "jev-1.13.0",
			Timeout:    5 * time.Second,
			Complexity: ComplexityQuestionsConfig{Primary: "choice", Shadow: true},
			OnFailure:  OnFailureAsk,
			Jev: JevConfig{
				BaseURL:    "https://api.typesafe.ai",
				APIKeyEnv:  "JEV_API_KEY",
				MaxRetries: 1,
			},
		},
		Translation: TranslationConfig{
			ProviderModel: ProviderModel{Provider: "claude", Model: "haiku"},
			Timeout:       60 * time.Second,
		},
		Policy: PolicyConfig{
			ContextDepth: ContextDepthPolicyConfig{ProjectThreshold: float64Ptr(0.5)},
			Complexity:   ComplexityPolicyConfig{Complex: float64Ptr(0.40), Medium: float64Ptr(0.50)},
			Stages: StagePolicyConfig{
				Thresholds: map[string]float64{
					"research": 0.70, "analysis": 0.60, "planning": 0.65,
					"development": 0.60, "testing": 0.60, "review": 0.70,
				},
				Force: map[string][]string{"complex": {"analysis", "planning", "review"}},
			},
		},
		Context: ContextConfig{MaxFiles: 20, MaxBytes: 24576},
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Research = ActionConfig{}
	cfg.Test.Command = ""

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"research.provider is required", "research.model is required", "test.command is required"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to contain %q, got: %s", want, msg)
		}
	}
}

func TestValidate_UnsupportedProvider(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Provider = "openrouter"

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `review.provider "openrouter" is not a supported provider`) {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidate_InvalidLimits(t *testing.T) {
	cfg := validConfig()
	cfg.Limits.MaxSteps = 0
	cfg.Limits.MaxFixIterations = -1

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "max_steps must be positive") || !strings.Contains(msg, "max_fix_iterations must be positive") {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestValidate_NegativeActionTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Timeout = -1 * time.Minute

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "actions.review.timeout must not be negative, got -1m0s") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidate_InvalidVersion(t *testing.T) {
	cfg := validConfig()
	cfg.Version = 3

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported config version 3 (expected 2)") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidate_Version1ExplainsTheChanges(t *testing.T) {
	cfg := validConfig()
	cfg.Version = 1

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"unsupported config version 1 (expected 2)", "classifier", "decision", "actions.answer", "orchestrator.example.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %s", want, err.Error())
		}
	}
}

func TestValidate_AggregatesAllProblems(t *testing.T) {
	cfg := Config{}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	// Every missing action's provider/model should be reported, not just the first.
	for _, action := range []string{"research", "analyze", "plan", "implement", "review", "translation"} {
		if !strings.Contains(msg, action+".provider is required") {
			t.Errorf("expected aggregated error to mention %s.provider, got: %s", action, msg)
		}
	}
}

func TestValidate_ProtectedPaths(t *testing.T) {
	cfg := validConfig()
	cfg.Security.ProtectedPaths = []string{"go.mod", "/etc/hosts", "["}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected invalid protected_paths to fail")
	}
	for _, want := range []string{`security.protected_paths[1] "/etc/hosts"`, `security.protected_paths[2] "["`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `"go.mod"`) {
		t.Errorf("go.mod is a valid pattern:\n%v", err)
	}
}

func TestValidate_TestSandbox(t *testing.T) {
	valid := []TestConfig{
		{Command: "go test ./..."},
		{Command: "go test ./...", Sandbox: "none"},
		{Command: "go test ./...", Sandbox: "bwrap", ROBinds: []string{"~/go/pkg/mod"}, RWBinds: []string{"/var/cache/x"}},
	}
	for _, tc := range valid {
		cfg := validConfig()
		cfg.Test = tc
		if err := Validate(cfg); err != nil {
			t.Errorf("%+v: unexpected error: %v", tc, err)
		}
	}

	cases := []struct {
		test TestConfig
		want string
	}{
		{TestConfig{Command: "x", Sandbox: "docker"}, `test.sandbox must be none or bwrap, got "docker"`},
		{TestConfig{Command: "x", ROBinds: []string{"~/go"}}, "only apply with test.sandbox: bwrap"},
		{TestConfig{Command: "x", Sandbox: "bwrap", ROBinds: []string{"go/pkg"}}, `test.ro_binds[0] "go/pkg": must be absolute or start with ~/`},
		{TestConfig{Command: "x", Sandbox: "bwrap", RWBinds: []string{"~/../etc"}}, `test.rw_binds[0] "~/../etc": must not contain ..`},
		{TestConfig{Command: "x", Sandbox: "bwrap", RWBinds: []string{"~/"}}, "must not be the root or the home directory itself"},
		{TestConfig{Command: "x", Sandbox: "bwrap", ROBinds: []string{"/"}}, "must not be the root or the home directory itself"},
	}
	for _, tc := range cases {
		cfg := validConfig()
		cfg.Test = tc.test
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: expected %q, got %v", tc.test, tc.want, err)
		}
	}
}

func TestResolveBinds(t *testing.T) {
	ro, rw := TestConfig{ROBinds: []string{"~/go/pkg/mod/", "/opt/x"}, RWBinds: []string{"~/.cache/go-build"}}.ResolveBinds("/home/u")
	if strings.Join(ro, " ") != "/home/u/go/pkg/mod /opt/x" || strings.Join(rw, " ") != "/home/u/.cache/go-build" {
		t.Errorf("ro = %q, rw = %q", ro, rw)
	}
}

func TestValidate_Effort(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Plan.Provider, cfg.Actions.Plan.Effort = "claude", "high"
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid effort rejected: %v", err)
	}

	cfg.Actions.Plan.Effort = "turbo"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "plan.effort") {
		t.Errorf("bad level not reported: %v", err)
	}

	cfg.Actions.Plan.Effort, cfg.Actions.Plan.Provider = "high", "gemini"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "only supported with provider claude") {
		t.Errorf("effort on gemini not refused: %v", err)
	}
}

func TestPromptLimits_Defaults(t *testing.T) {
	var p PromptLimitsConfig
	if p.DiffBytes() != DefaultMaxDiffBytes || p.TestOutputBytes() != DefaultMaxTestOutputBytes {
		t.Errorf("absent keys: got %d / %d, want the defaults", p.DiffBytes(), p.TestOutputBytes())
	}
	diff, out := 65536, 8192
	p = PromptLimitsConfig{MaxDiffBytes: &diff, MaxTestOutputBytes: &out}
	if p.DiffBytes() != diff || p.TestOutputBytes() != out {
		t.Errorf("set keys: got %d / %d", p.DiffBytes(), p.TestOutputBytes())
	}
}

func TestValidate_PromptLimits(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	for name, tc := range map[string]struct {
		prompt PromptLimitsConfig
		want   string // "" = valid
	}{
		"absent":            {PromptLimitsConfig{}, ""},
		"bounds inclusive":  {PromptLimitsConfig{MaxDiffBytes: intPtr(MinMaxDiffBytes), MaxTestOutputBytes: intPtr(MaxMaxTestOutputBytes)}, ""},
		"diff zero":         {PromptLimitsConfig{MaxDiffBytes: intPtr(0)}, "limits.prompt.max_diff_bytes must be between 16384 and 4194304, got 0"},
		"diff too large":    {PromptLimitsConfig{MaxDiffBytes: intPtr(MaxMaxDiffBytes + 1)}, "limits.prompt.max_diff_bytes must be between"},
		"test output small": {PromptLimitsConfig{MaxTestOutputBytes: intPtr(MinMaxTestOutputBytes - 1)}, "limits.prompt.max_test_output_bytes must be between 4096 and 1048576"},
		"test output large": {PromptLimitsConfig{MaxTestOutputBytes: intPtr(MaxMaxTestOutputBytes + 1)}, "limits.prompt.max_test_output_bytes must be between"},
		"globs":             {PromptLimitsConfig{DiffSummarize: []string{"*.pb.go", "gen/*.ts"}}, ""},
		"bad glob":          {PromptLimitsConfig{DiffSummarize: []string{"*.go", "gen/[a"}}, `limits.prompt.diff_summarize[1] "gen/[a": syntax error in pattern`},
		"empty glob":        {PromptLimitsConfig{DiffSummarize: []string{" "}}, "limits.prompt.diff_summarize[0] must not be empty"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Limits.Prompt = tc.prompt
			err := Validate(cfg)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

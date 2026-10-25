package config

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

// validConfig returns a fully valid version 2 config, matching the example in
// specs/JEV integration/TASKS.md T02/T22.
func validConfig() Config {
	return Config{
		Version: CurrentVersion,
		Actions: ActionsConfig{
			Research:  ActionConfig{ProviderModel: ProviderModel{Provider: "gemini", Model: "pro"}},
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
			Complexity:   ComplexityPolicyConfig{Cuts: &ComplexityCutsConfig{Medium: float64Ptr(0.50), Complex: float64Ptr(1.40)}},
			Stages: StagePolicyConfig{
				Thresholds: map[string]float64{"research": 0.70, "development": 0.60},
				Force:      map[string][]string{"complex": {"planning", "review"}},
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

func TestValidate_MaxCostUSD(t *testing.T) {
	for name, tc := range map[string]struct {
		value   *float64
		wantErr bool
	}{
		"absent":            {nil, false},
		"positive":          {float64Ptr(2.5), false},
		"zero":              {float64Ptr(0), true},
		"negative":          {float64Ptr(-1), true},
		"not a number":      {float64Ptr(math.NaN()), true},
		"positive infinity": {float64Ptr(math.Inf(1)), true},
		"negative infinity": {float64Ptr(math.Inf(-1)), true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Limits.MaxCostUSD = tc.value
			err := Validate(cfg)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "limits.max_cost_usd must be positive and finite") {
				t.Fatalf("expected a max_cost_usd error, got: %v", err)
			}
		})
	}
}

func TestLimitsConfig_StopsWithoutProgress(t *testing.T) {
	yes, no := true, false
	for name, tc := range map[string]struct {
		limits LimitsConfig
		want   bool
	}{
		"unset":          {LimitsConfig{}, true},
		"true":           {LimitsConfig{StopWithoutProgress: &yes}, true},
		"explicit false": {LimitsConfig{StopWithoutProgress: &no}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.limits.StopsWithoutProgress(); got != tc.want {
				t.Errorf("StopsWithoutProgress = %v, want %v", got, tc.want)
			}
			cfg := validConfig()
			cfg.Limits.StopWithoutProgress = tc.limits.StopWithoutProgress
			if err := Validate(cfg); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
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
	for _, action := range []string{"research", "plan", "implement", "review", "translation"} {
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
		// The sandbox is the default: binds need no explicit bwrap.
		{Command: "go test ./...", ROBinds: []string{"~/go/pkg/mod"}},
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
		{TestConfig{Command: "x", Sandbox: "docker"}, `test.sandbox must be bwrap (the default) or none, got "docker"`},
		{TestConfig{Command: "x", Sandbox: "none", ROBinds: []string{"~/go"}}, "only apply under the sandbox"},
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

func TestValidate_EnvPass(t *testing.T) {
	for name, tc := range map[string]struct {
		pass []string
		want string // "" = valid
	}{
		"empty":               {nil, ""},
		"plain names":         {[]string{"GOPROXY", "DATABASE_URL", "GIT_AUTHOR_NAME", "KEYBOARD_LAYOUT", "PWD"}, ""},
		"token":               {[]string{"GITLAB_TOKEN"}, `test.env_pass[0] "GITLAB_TOKEN": looks like a credential (contains TOKEN)`},
		"glued api key":       {[]string{"OPENAIAPIKEY"}, "contains APIKEY"},
		"auth segment":        {[]string{"MY_AUTH_HEADER"}, "has the segment AUTH"},
		"ssh agent":           {[]string{"SSH_AUTH_SOCK"}, "has the segment AUTH"},
		"key segment":         {[]string{"STRIPE_KEY"}, "has the segment KEY"},
		"access key id":       {[]string{"AWS_ACCESS_KEY_ID"}, "has the segment KEY"},
		"pass segment":        {[]string{"DB_PASS"}, "has the segment PASS"},
		"lower case":          {[]string{"db_password"}, "contains PASSWORD"},
		"bad name":            {[]string{"db-url"}, `test.env_pass[0] "db-url": must be an exact environment variable name`},
		"wildcard":            {[]string{"GO*"}, "no wildcards"},
		"declared credential": {[]string{"JEV_API_KEY"}, "is a credential this config names"},
		"parent session":      {[]string{"CLAUDECODE"}, "belongs to the agent session"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Test.EnvPass = tc.pass
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

func TestTestConfig_SandboxedByDefault(t *testing.T) {
	for sandbox, want := range map[string]bool{"": true, "bwrap": true, "none": false} {
		if got := (TestConfig{Sandbox: sandbox}).Sandboxed(); got != want {
			t.Errorf("Sandboxed() with test.sandbox %q = %v, want %v", sandbox, got, want)
		}
	}
}

func TestValidate_TestBaseline(t *testing.T) {
	for _, ok := range []string{"", BaselineRequireGreen, BaselineOff} {
		cfg := validConfig()
		cfg.Test.Baseline = ok
		if err := Validate(cfg); err != nil {
			t.Errorf("test.baseline %q: %v", ok, err)
		}
	}
	cfg := validConfig()
	cfg.Test.Baseline = "warn"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), `test.baseline must be require_green (the default) or off, got "warn"`) {
		t.Errorf("got %v", err)
	}
	if !(TestConfig{}).PreRunTest() || (TestConfig{Baseline: BaselineOff}).PreRunTest() {
		t.Error("PreRunTest disagrees with test.baseline")
	}
}

func TestValidate_Verify(t *testing.T) {
	ok := validConfig()
	ok.Actions.Implement.Verify = true
	if err := Validate(ok); err != nil {
		t.Fatalf("verify on a sandboxed implement: %v", err)
	}

	three, zero, fiftyOne := 3, 0, 51
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"no sandbox", func(c *Config) { c.Test.Sandbox = SandboxNone }, "actions.implement.verify needs test.sandbox bwrap"},
		{"no test command", func(c *Config) { c.Test.Command = "" }, "actions.implement.verify needs test.command"},
		{"not claude", func(c *Config) { c.Actions.Implement.Provider = "gemini" }, "actions.implement.verify is only supported with provider claude or codex"},
		{"on review", func(c *Config) { c.Actions.Review.Verify = true }, "actions.review.verify: unknown key"},
		{"written on plan", func(c *Config) { c.Actions.Plan.verifySet = true }, "actions.plan.verify: unknown key"},
		{"zero calls", func(c *Config) { c.Limits.Verify.MaxCallsPerStep = &zero }, "limits.verify.max_calls_per_step must be between 1 and 50, got 0"},
		{"too many calls", func(c *Config) { c.Limits.Verify.MaxCallsPerStep = &fiftyOne }, "limits.verify.max_calls_per_step must be between 1 and 50, got 51"},
		{"negative timeout", func(c *Config) { c.Limits.Verify.CallTimeout = -time.Second }, "limits.verify.call_timeout must be a positive duration"},
		{"zero timeout written", func(c *Config) { c.Limits.Verify.callTimeoutSet = true }, "limits.verify.call_timeout must be a positive duration"},
		{"reserved server name", func(c *Config) {
			c.Extensions.MCP = map[string]MCPServerConfig{"orch": {Command: "x", Tools: []string{"t"}}}
		}, "extensions.mcp.orch: the name orch is reserved"},
		{"double underscore in a server name", func(c *Config) {
			c.Extensions.MCP = map[string]MCPServerConfig{"a__b": {Command: "x", Tools: []string{"t"}}}
		}, "extensions.mcp.a__b: the server name must not contain __"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Actions.Implement.Verify = true
			tc.mutate(&cfg)
			err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}

	cfg := validConfig()
	cfg.Limits.Verify.MaxCallsPerStep = &three
	cfg.Limits.Verify.CallTimeout = time.Minute
	if err := Validate(cfg); err != nil {
		t.Errorf("explicit limits.verify: %v", err)
	}
}

func TestVerifyLimits_Defaults(t *testing.T) {
	var v VerifyLimitsConfig
	if v.MaxCalls() != 8 || v.Timeout() != 10*time.Minute {
		t.Errorf("defaults = %d, %s; want 8, 10m", v.MaxCalls(), v.Timeout())
	}
	five := 5
	v = VerifyLimitsConfig{MaxCallsPerStep: &five, CallTimeout: time.Minute}
	if v.MaxCalls() != 5 || v.Timeout() != time.Minute {
		t.Errorf("explicit = %d, %s; want 5, 1m", v.MaxCalls(), v.Timeout())
	}
}

func nChecks(n int) []CheckConfig {
	var checks []CheckConfig
	for i := 0; i < n; i++ {
		checks = append(checks, CheckConfig{Name: fmt.Sprintf("check-%d", i), Command: "true"})
	}
	return checks
}

func TestValidate_Checks(t *testing.T) {
	ok := validConfig()
	ok.Checks = []CheckConfig{
		{Name: "build", Command: "go build ./..."},
		{Name: "vet", Command: "go vet ./..."},
		{Name: "a_b-9", Command: "true"},
		{Name: strings.Repeat("x", 32), Command: "true"},
	}
	if err := Validate(ok); err != nil {
		t.Errorf("valid checks: %v", err)
	}
	max := validConfig()
	max.Checks = nChecks(16)
	if err := Validate(max); err != nil {
		t.Errorf("16 checks: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"duplicate name", func(c *Config) { c.Checks = []CheckConfig{{"vet", "a"}, {"vet", "b"}} }, `checks[1].name "vet" is used by an earlier check`},
		{"uppercase", func(c *Config) { c.Checks = []CheckConfig{{"Build", "a"}} }, `checks[0].name "Build" must match`},
		{"leading dash", func(c *Config) { c.Checks = []CheckConfig{{"-x", "a"}} }, `checks[0].name "-x" must match`},
		{"33 characters", func(c *Config) { c.Checks = []CheckConfig{{strings.Repeat("a", 33), "a"}} }, "must match"},
		{"slash", func(c *Config) { c.Checks = []CheckConfig{{"a/b", "a"}} }, `checks[0].name "a/b" must match`},
		{"empty name", func(c *Config) { c.Checks = []CheckConfig{{"", "a"}} }, `checks[0].name "" must match`},
		{"17 checks", func(c *Config) { c.Checks = nChecks(17) }, "17 checks, at most 16"},
		{"empty command", func(c *Config) { c.Checks = []CheckConfig{{"vet", ""}} }, "checks[0].command is required"},
		{"blank command", func(c *Config) { c.Checks = []CheckConfig{{"vet", "  \t"}} }, "checks[0].command is required"},
		{"no test command", func(c *Config) {
			c.Checks = []CheckConfig{{"vet", "go vet ./..."}}
			c.Test.Command = ""
		}, "checks need test.command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want %q in %v", tc.want, err)
			}
		})
	}
}

// allowedConfig is validConfig under an allowlist that names every model it uses.
func allowedConfig() Config {
	cfg := validConfig()
	cfg.AllowedModels = []string{"pro", "sonnet", "opus", "gpt", "haiku"}
	cfg.AllowedModelsFrom = "/etc/orchestrator/org.yaml"
	return cfg
}

func TestValidate_AllowedModels(t *testing.T) {
	if err := Validate(allowedConfig()); err != nil {
		t.Fatalf("every model is listed: %v", err)
	}
	const org = "/etc/orchestrator/org.yaml"
	for name, tc := range map[string]struct {
		edit func(*Config)
		want string
	}{
		"base model": {func(c *Config) { c.Actions.Review.Model = "o3" },
			"actions.review: model o3 is not in allowed_models (" + org + ")"},
		"base model, the spec's example": {func(c *Config) { c.AllowedModels = []string{"pro", "sonnet", "gpt", "haiku"} },
			"actions.implement: model opus is not in allowed_models (" + org + ")"},
		"by_complexity": {func(c *Config) {
			c.Actions.Plan.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexityComplex: {Model: "mythos"}}
		}, "actions.plan.by_complexity.complex: model mythos is not in allowed_models (" + org + ")"},
		"by_complexity escalate": {func(c *Config) {
			c.Actions.Implement.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexityComplex: {Effort: "high", Escalate: &Escalation{Model: "mythos"}}}
		}, "actions.implement.by_complexity.complex.escalate: model mythos is not in allowed_models (" + org + ")"},
		"extra reviewer": {func(c *Config) { c.Actions.Review.Models = []string{"o3"} },
			"actions.review.models[0]: model o3 is not in allowed_models (" + org + ")"},
		"translator": {func(c *Config) { c.Translation.Model = "tiny" },
			"translation: model tiny is not in allowed_models (" + org + ")"},
		"LLM decision provider": {func(c *Config) {
			c.Decision.Provider, c.Decision.Model = "claude", "tiny"
		}, "decision: model tiny is not in allowed_models (" + org + ")"},
		"route without a model": {func(c *Config) { c.Actions.Plan.Model = "" },
			"actions.plan: no model is named; allowed_models (" + org + ") cannot check the CLI default"},
		"translator without a model": {func(c *Config) { c.Translation.Model = "" },
			"translation: no model is named; allowed_models (" + org + ") cannot check the CLI default"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := allowedConfig()
			tc.edit(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidate_AllowedModels_JevDecisionModelIsNotChecked(t *testing.T) {
	cfg := allowedConfig() // decision.provider is jev with the model jev-1.13.0, not on the list
	if err := Validate(cfg); err != nil {
		t.Fatalf("a Jev model is not a CLI model: %v", err)
	}
}

func TestValidate_AllowedModels_ByComplexityWithoutModelInheritsTheBase(t *testing.T) {
	cfg := allowedConfig()
	cfg.Actions.Implement.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexityComplex: {Effort: "high"}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("an effort-only entry inherits the listed base model: %v", err)
	}
}

// specs/claude-auth/SPEC.md §17.3, D8: a model variable in providers.claude.api_env changes the
// model a route reaches, so its value must be on the list; other variables are not checked.
func TestValidate_AllowedModels_APIEnvModelVariables(t *testing.T) {
	const org = "/etc/orchestrator/org.yaml"
	cfg := allowedConfig()
	cfg.Providers.Claude.APIEnv = map[string]string{"AWS_PROFILE": "claude-bedrock", "ANTHROPIC_DEFAULT_OPUS_MODEL": "opus"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("a listed alias and a non-model variable: %v", err)
	}
	for _, name := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL"} {
		t.Run(name, func(t *testing.T) {
			cfg := allowedConfig()
			cfg.Providers.Claude.APIEnv = map[string]string{name: "bedrock/eu.anthropic.claude-opus-5-5"}
			want := "providers.claude.api_env." + name + ": model bedrock/eu.anthropic.claude-opus-5-5 is not in allowed_models (" + org + ")"
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
	cfg = validConfig()
	cfg.Providers.Claude.APIEnv = map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "bedrock/eu.anthropic.claude-opus-5-5"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("without allowed_models nothing is checked: %v", err)
	}
}

func TestValidate_NoAllowlistChecksNothing(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Model = "anything"
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_SecretScan(t *testing.T) {
	for _, mode := range []string{"", SecretScanStop, SecretScanWarn, SecretScanOff} {
		cfg := validConfig()
		cfg.Security.SecretScan = SecretScanConfig{Mode: mode, AllowPaths: []string{"testdata/**", "fixtures/*.pem"}}
		if err := Validate(cfg); err != nil {
			t.Errorf("mode %q: %v", mode, err)
		}
	}
	cfg := validConfig()
	cfg.Security.SecretScan = SecretScanConfig{Mode: "log", AllowPaths: []string{"/abs/**", "bad[", ""}}
	err := Validate(cfg)
	for _, want := range []string{`security.secret_scan.mode must be stop (the default), warn or off, got "log"`,
		`allow_paths[0] "/abs/**": must be relative`, `allow_paths[1] "bad["`, `allow_paths[2] "": must not be empty`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
	if !(SecretScanConfig{}).Stops() || !(SecretScanConfig{}).Enabled() || (SecretScanConfig{Mode: SecretScanWarn}).Stops() || (SecretScanConfig{Mode: SecretScanOff}).Enabled() {
		t.Error("Stops/Enabled disagree with the mode")
	}
}

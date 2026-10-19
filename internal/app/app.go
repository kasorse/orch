// Package app wires config, provider, translation, the decision provider, context
// collection, classification, repository, testexec, action, persistence, and workflow
// together into the orchestrator's CLI commands
// (version, validate, run, resume, story plan, decisions report|calibrate, runs report). This is the composition root - no other package
// constructs the full dependency graph.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/decision"
	"orchestrator/internal/decision/jev"
	"orchestrator/internal/decision/llm"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/interaction"
	"orchestrator/internal/jira"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/claude"
	"orchestrator/internal/provider/codex"
	"orchestrator/internal/provider/gemini"
	"orchestrator/internal/report"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/tui"
	"orchestrator/internal/workflow"
)

const Version = "0.1.0"

const defaultConfigPath = "orchestrator.yaml"

const runsBaseDir = ".orchestrator/runs"

const usage = `usage: orchestrator [--config path] [--mode plan|manual|accept-edits|auto] [--claude-auth auto|api|login] [--verbose] ` +
	`<version|validate|run [--stage <name>]... [--allow-red-tests] [--inplace|--worktree] "<task>"|run --jira <KEY|URL> ["notes"]|resume <run-id>|story plan [--jira <KEY|URL>] [--repo <name>]... "<task>"|decisions report|decisions calibrate [--runs <dir>] [--min-runs N]|runs report [--dir <path>]|runs clean [--older-than <N>d] [--dry-run]>`

// Run parses args and dispatches to the requested subcommand. It never calls
// os.Exit - main.go is the sole exit point, mapping the returned error via
// exitcode.FromError.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cli, err := parseArgs(args)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	rest := cli.rest
	if len(rest) == 0 {
		return &apperr.ConfigError{Msg: usage}
	}
	storyPlanCmd := rest[0] == "story" && len(rest) > 1 && rest[1] == "plan"
	if cli.jiraRef != "" && rest[0] != "run" && !storyPlanCmd {
		return &apperr.ConfigError{Msg: "--jira is only valid with run and story plan"}
	}
	if len(cli.repos) > 0 && !storyPlanCmd {
		return &apperr.ConfigError{Msg: "--repo is only valid with story plan"}
	}
	if len(cli.stages) > 0 && rest[0] != "run" {
		return &apperr.ConfigError{Msg: "--stage is only valid with run"}
	}
	if cli.allowRedTests && rest[0] != "run" {
		return &apperr.ConfigError{Msg: "--allow-red-tests is only valid with run"}
	}
	if cli.isolation != "" && rest[0] != "run" {
		return &apperr.ConfigError{Msg: "--" + cli.isolation + " is only valid with run"}
	}
	if cli.claudeAuth != "" && rest[0] != "run" && rest[0] != "resume" && rest[0] != "validate" {
		return &apperr.ConfigError{Msg: "--claude-auth is only valid with run, resume and validate"}
	}

	switch rest[0] {
	case "version":
		fmt.Fprintf(stdout, "orchestrator version %s\n", Version)
		return nil
	case "validate":
		return runValidate(ctx, cli.configPath, cli.claudeAuth, stdout, stderr)
	case "run":
		// With --jira the ticket is the task, and any text is the operator's notes on it.
		if cli.jiraRef == "" && len(rest) < 2 {
			return &apperr.ConfigError{Msg: `usage: orchestrator run [--stage <name>]... "<task description>"   or   orchestrator run --jira <KEY|URL> ["notes"]`}
		}
		return runTask(ctx, cli.configPath, strings.Join(rest[1:], " "), cli.jiraRef, cli.stages, cli.allowRedTests, cli.isolation, cli.mode, cli.claudeAuth, cli.verbose, stdin, stdout, stderr)
	case "resume":
		if len(rest) != 2 {
			return &apperr.ConfigError{Msg: `usage: orchestrator resume <run-id>   (the full ID, or a unique prefix of at least 8 characters)`}
		}
		return resumeTask(ctx, cli.configPath, rest[1], cli.mode, cli.claudeAuth, cli.verbose, stdin, stdout, stderr)
	case "story":
		if !storyPlanCmd {
			return &apperr.ConfigError{Msg: storyPlanUsage}
		}
		// With --jira the ticket is the task, and any text is the operator's notes on it.
		request := strings.Join(rest[2:], " ")
		if cli.jiraRef == "" && strings.TrimSpace(request) == "" {
			return &apperr.ConfigError{Msg: storyPlanUsage}
		}
		return storyPlan(ctx, cli.configPath, request, cli.jiraRef, cli.repos, cli.verbose, stdout)
	case "decisions":
		if len(rest) == 2 && rest[1] == "report" {
			return decisionsReport(stdout)
		}
		args, err := decisionsCalibrateArgs(rest[1:])
		if err != nil {
			return err
		}
		return decisionsCalibrate(stdout, args)
	case "runs":
		if len(rest) > 1 && rest[1] == "clean" {
			cleanOpts, err := runsCleanArgs(rest[2:])
			if err != nil {
				return err
			}
			return runsClean(ctx, stdout, cleanOpts)
		}
		dir, err := runsReportArgs(rest[1:])
		if err != nil {
			return err
		}
		return runsReport(stdout, dir)
	case "mcp-verify":
		// Hidden: not in the usage text. parseArgs has run, but --dir is not a global flag,
		// so it is still in rest.
		dir, err := mcpVerifyArgs(rest[1:])
		if err != nil {
			return err
		}
		return runMCPVerify(ctx, dir, stdin, stdout, stderr)
	default:
		return &apperr.ConfigError{Msg: fmt.Sprintf("unknown command %q", rest[0])}
	}
}

// cliArgs is the command line after parseArgs.
type cliArgs struct {
	configPath string
	// mode is empty if --mode was not given - the caller then runs the interactive
	// mode-select prompt.
	mode domain.RunMode
	// verbose is --verbose (spec §11 Q5: the sole verbosity switch - no config key or env
	// var).
	verbose bool
	// jiraRef is --jira's value: an issue key or its URL (specs/jira/SPEC.md §6).
	jiraRef string
	// stages are the --stage values, parsed, in the order given (specs/stage-select/SPEC.md
	// §4); empty means the decision layer chooses.
	stages []domain.Stage
	// allowRedTests is --allow-red-tests: the task is to make failing tests pass, so a red
	// pre-run test does not stop the run (specs/hardening/SPEC.md §8.4).
	allowRedTests bool
	// isolation is "inplace" for --inplace or "worktree" for --worktree: this run's
	// isolation whatever execution.isolation says (specs/worktree/SPEC.md §6); empty if
	// neither was given.
	isolation string
	// claudeAuth is --claude-auth (auto, api or login); empty if not given
	// (specs/claude-auth/SPEC.md §6).
	claudeAuth string
	// repos are the --repo values, in the order given and without duplicates: the services of
	// story plan, instead of those the task names (specs/story/SPEC.md §7.1).
	repos []string
	// rest are the positional arguments.
	rest []string
}

// parseArgs pulls --config/--config=X, --mode/--mode=X, --claude-auth/--claude-auth=X, --jira/--jira=X, the repeatable
// --stage/--stage=X and --repo/--repo=X, --allow-red-tests, --inplace and --verbose/-v out of args, in any position, and returns them
// with the remaining positional arguments.
func parseArgs(args []string) (cliArgs, error) {
	cli := cliArgs{configPath: defaultConfigPath}
	// value returns the flag's value from "--flag=X" or the next argument.
	value := func(i *int, name string) (string, error) {
		if v, ok := strings.CutPrefix(args[*i], name+"="); ok {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config" || strings.HasPrefix(arg, "--config="):
			v, err := value(&i, "--config")
			if err != nil {
				return cliArgs{}, err
			}
			cli.configPath = v
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, err := value(&i, "--mode")
			if err != nil {
				return cliArgs{}, err
			}
			if cli.mode, err = domain.ParseRunMode(v); err != nil {
				return cliArgs{}, err
			}
		case arg == "--claude-auth" || strings.HasPrefix(arg, "--claude-auth="):
			if cli.claudeAuth != "" {
				return cliArgs{}, fmt.Errorf("--claude-auth given twice")
			}
			v, err := value(&i, "--claude-auth")
			if err != nil {
				return cliArgs{}, err
			}
			if cli.claudeAuth, err = config.ParseClaudeAuth(v); err != nil {
				return cliArgs{}, err
			}
		case arg == "--jira" || strings.HasPrefix(arg, "--jira="):
			if cli.jiraRef != "" {
				return cliArgs{}, fmt.Errorf("--jira given twice")
			}
			v, err := value(&i, "--jira")
			if err != nil {
				return cliArgs{}, err
			}
			if cli.jiraRef = strings.TrimSpace(v); cli.jiraRef == "" {
				return cliArgs{}, fmt.Errorf("--jira requires an issue key or URL")
			}
		case arg == "--stage" || strings.HasPrefix(arg, "--stage="):
			v, err := value(&i, "--stage")
			if err != nil {
				return cliArgs{}, err
			}
			parsed, err := parseStageFlags([]string{v})
			if err != nil {
				return cliArgs{}, err
			}
			if !slices.Contains(cli.stages, parsed[0]) {
				cli.stages = append(cli.stages, parsed[0])
			}
		case arg == "--repo" || strings.HasPrefix(arg, "--repo="):
			v, err := value(&i, "--repo")
			if err != nil {
				return cliArgs{}, err
			}
			if v = strings.TrimSpace(v); v == "" {
				return cliArgs{}, fmt.Errorf("--repo requires a service name")
			}
			if !slices.Contains(cli.repos, v) {
				cli.repos = append(cli.repos, v)
			}
		case arg == "--allow-red-tests":
			cli.allowRedTests = true
		case arg == "--inplace" || arg == "--worktree":
			mode := strings.TrimPrefix(arg, "--")
			if cli.isolation != "" && cli.isolation != mode {
				return cliArgs{}, errors.New("--inplace and --worktree cannot be combined")
			}
			cli.isolation = mode
		case arg == "--verbose" || arg == "-v":
			cli.verbose = true
		default:
			cli.rest = append(cli.rest, arg)
		}
	}
	return cli, nil
}

// sandboxProbeTimeout bounds the one bwrap run that checks the host can sandbox tests.
const sandboxProbeTimeout = 10 * time.Second

// loadAndValidateConfig runs the whole fail-fast chain (MVP.md §7): Load -> Validate ->
// (register the credential variables to scrub) -> ValidateDependencies (-> the sandbox probe, with test.sandbox: bwrap) ->
// ValidateAuthorities. ResolveClaudeAuth runs right after ValidateDependencies. The registry is only asked what its
// adapters can enforce; constructing it makes no calls. depOpts chooses the optional
// dependency checks from the loaded config (see config.DependencyOptions).
func loadAndValidateConfig(configPath, claudeAuthFlag string, registry *provider.Registry, executor execution.ProcessExecutor, reporter *logging.ConsoleReporter, depOpts func(config.Config) config.DependencyOptions) (config.Config, map[string]domain.ProviderCLI, config.OrgSource, domain.ClaudeAuth, error) {
	reporter.ValidatingConfig()
	fail := func(err error) (config.Config, map[string]domain.ProviderCLI, config.OrgSource, domain.ClaudeAuth, error) {
		return config.Config{}, nil, config.OrgSource{}, domain.ClaudeAuth{}, err
	}

	// The org layer is merged inside the Load step (specs/governance/SPEC.md §6.3), so every
	// check below sees the merged config.
	orgPath, _ := config.FindOrgConfig(orgConfigEtcPath, os.Getenv)
	cfg, org, err := config.LoadLayered(configPath, orgPath)
	if err != nil {
		return fail(err)
	}
	if err := config.Validate(cfg); err != nil {
		return fail(err)
	}
	// From here on no child process - the sandbox probe included - inherits a variable
	// the config names as a credential (specs/jira/SPEC.md §12).
	execution.ScrubCredentials(cfg.CredentialEnvNames()...)
	// What the test command may inherit beyond the default allowlist (test.env_pass).
	execution.AllowTestEnv(cfg.Test.EnvPass...)
	opts := depOpts(cfg)
	opts.Cost = registry
	if err := config.ValidateDependencies(cfg, config.ExecDependencyChecker{}, opts); err != nil {
		return fail(err)
	}
	// The credential mode of the claude CLI (specs/claude-auth/SPEC.md §4): decided here, so a
	// bad api setup stops everything before any call. The key itself is not read here.
	auth, notices, err := config.ResolveClaudeAuth(cfg, claudeAuthFlag, os.LookupEnv)
	if err != nil {
		return fail(err)
	}
	// The adapter learns the mode before anything runs claude - the version probe below
	// included, which gets the same environment as a call (SPEC §7).
	apiEnv := cfg.Providers.Claude.APIEnv
	ignored, err := applyClaudeAuth(registry, auth, apiEnv, os.LookupEnv)
	if err != nil {
		return fail(err)
	}
	envLine, envNotices := claudeEnvReport(&auth, apiEnv, ignored, os.LookupEnv)
	notices = append(notices, envNotices...)
	// A host that cannot sandbox (e.g. AppArmor blocking bwrap's user namespaces) must fail
	// here, not at the first test step after the AI calls.
	if cfg.Test.Sandboxed() {
		probeCtx, cancel := context.WithTimeout(context.Background(), sandboxProbeTimeout)
		err := testexec.Probe(probeCtx, executor)
		cancel()
		if err != nil {
			return fail(err)
		}
	}
	// Authority does not depend on the run mode (specs/permissions/SPEC.md §5.2), so this
	// single check, before the mode is even known, is complete.
	if err := config.ValidateAuthorities(cfg, registry); err != nil {
		return fail(err)
	}

	// The last check of the chain: it runs each routed CLI once (--version), which is
	// not an AI call, and only after everything that needs no process at all.
	clis, err := checkProviderCLIs(context.Background(), cfg, registry, reporter)
	if err != nil {
		return fail(err)
	}

	reporter.ConfigValid()
	reporter.ClaudeAuth(auth.Line(), envLine, notices)
	return cfg, clis, org, auth, nil
}

// applyClaudeAuth hands the resolved credential mode and providers.claude.api_env to the
// claude adapter, and in api mode the key, read here once from the variable the config names
// (specs/claude-auth/SPEC.md §12); it is never stored anywhere else. It returns the names of
// the variables the mode removes from claude's environment that are set: in api mode those
// that outrank the key, in login mode the api_env names (§17.4). A registry whose claude
// client is not the real adapter (a test double) has nothing to set.
func applyClaudeAuth(registry *provider.Registry, auth domain.ClaudeAuth, apiEnv map[string]string, lookup func(string) (string, bool)) ([]string, error) {
	client, err := registry.Resolve(string(provider.Claude))
	if err != nil {
		return nil, nil
	}
	adapter, ok := client.(*claude.Client)
	if !ok {
		return nil, nil
	}
	key := ""
	if auth.Mode == domain.ClaudeAuthAPI {
		raw, _ := lookup(auth.KeyEnv)
		if key = strings.TrimSpace(raw); key == "" {
			return nil, apperr.NewConfigError(fmt.Sprintf("claude auth mode api: $%s is not set or empty", auth.KeyEnv), nil)
		}
	}
	adapter.SetAuth(auth.Mode, key, apiEnv)
	return adapter.IgnoredEnv(), nil
}

// baseURLVar is where the claude CLI sends its requests; in api mode, where the key goes.
const baseURLVar = "ANTHROPIC_BASE_URL"

// claudeEnvReport records providers.claude.api_env in auth (names only, specs/claude-auth/
// SPEC.md §17.5) and returns the console line for it in api mode ("" otherwise) and the
// notices about claude's environment: the variables the mode ignores (ignored, from
// applyClaudeAuth) and, in api mode, where ANTHROPIC_BASE_URL sends the key (§13 #12). No
// value is printed except the host of ANTHROPIC_BASE_URL.
func claudeEnvReport(auth *domain.ClaudeAuth, apiEnv map[string]string, ignored []string, lookup func(string) (string, bool)) (line string, notices []string) {
	if auth.Mode != domain.ClaudeAuthAPI {
		if len(ignored) > 0 {
			auth.APIEnv = slices.Clone(ignored)
			notices = append(notices, "ignored in login mode: "+strings.Join(ignored, ", "))
		}
		return "", notices
	}
	if len(ignored) > 0 {
		notices = append(notices, "ignored in api mode: "+strings.Join(ignored, ", "))
	}
	if len(apiEnv) > 0 {
		auth.APIEnv = slices.Sorted(maps.Keys(apiEnv))
		shown := make([]string, len(auth.APIEnv))
		for i, name := range auth.APIEnv {
			shown[i] = name
			if host := urlHost(apiEnv[name]); name == baseURLVar && host != "" {
				shown[i] += " (" + host + ")"
			}
		}
		line = "claude env (api): " + strings.Join(shown, ", ")
	}
	source := "providers.claude.api_env"
	baseURL, set := apiEnv[baseURLVar]
	if !set {
		source = "inherited from the environment"
		baseURL, set = lookup(baseURLVar)
	}
	if set && strings.TrimSpace(baseURL) != "" {
		where := "the address it names"
		if host := urlHost(baseURL); host != "" {
			where = host
		}
		notices = append(notices, fmt.Sprintf("%s is set (%s): the API key goes to %s", baseURLVar, source, where))
	}
	return line, notices
}

// urlHost is the host (and port) of a URL, or "" when v has none; never the rest of it.
func urlHost(v string) string {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	return u.Host
}

// orgConfigEtcPath is where the machine-wide org file lives (specs/governance/SPEC.md §6.1);
// a variable so tests can point it elsewhere.
var orgConfigEtcPath = "/etc/orchestrator/org.yaml"

// SetOrgConfigEtcPath makes path the machine-wide org file location and returns a function that
// puts the previous one back. It exists for the integration tests, which run in another package
// and must never read the real /etc.
func SetOrgConfigEtcPath(path string) (restore func()) {
	previous := orgConfigEtcPath
	orgConfigEtcPath = path
	return func() { orgConfigEtcPath = previous }
}

// orgRef is the reference run.json records for org; nil when no org file applied.
func orgRef(org config.OrgSource) *domain.OrgConfigRef {
	if org.Path == "" {
		return nil
	}
	return &domain.OrgConfigRef{Path: org.Path, SHA256: org.SHA256}
}

// orgConfigChangedWarning is the resume warning for an org file that is not the one the run
// started under (specs/governance/SPEC.md §6.3).
const orgConfigChangedWarning = "org config changed since the run started (%s → %s); this resume runs under the current one"

// orgChangeWarning returns the warning for a resume whose org file differs from the run's
// start record, and "" when they are the same. Absent on either side counts as a change.
func orgChangeWarning(started, now *domain.OrgConfigRef) string {
	describe := func(r *domain.OrgConfigRef) string {
		if r == nil {
			return "none"
		}
		return r.SHA256
	}
	if describe(started) == describe(now) {
		return ""
	}
	return fmt.Sprintf(orgConfigChangedWarning, describe(started), describe(now))
}

// checkProviderCLIs asks every routed provider whose adapter can tell (provider.
// VersionVerifier) for its CLI version (specs/hardening/SPEC.md §7.2). A version outside
// the verified range is refused unless security.allow_unverified_cli is set, in which
// case it is warned about and recorded; a CLI that cannot be run is refused regardless.
func checkProviderCLIs(ctx context.Context, cfg config.Config, registry *provider.Registry, reporter *logging.ConsoleReporter) (map[string]domain.ProviderCLI, error) {
	found := map[string]domain.ProviderCLI{}
	for _, id := range config.ProvidersInUse(cfg) {
		client, err := registry.Resolve(id)
		if err != nil {
			continue
		}
		verifier, ok := client.(provider.VersionVerifier)
		if !ok {
			continue
		}
		version, verified, err := verifier.CLIVersion(ctx)
		if err != nil {
			return nil, apperr.NewDependencyError(fmt.Sprintf("cannot determine the version of %s: %v", id, err), err)
		}
		if !verified && !cfg.Security.AllowUnverifiedCLI {
			return nil, apperr.NewDependencyError(fmt.Sprintf(
				"%s %s has not been verified with this orchestrator's permission flags (verified: %s).\n"+
					"Run go test -tags security ./test/security/... on this version and extend the range, or set "+
					"security.allow_unverified_cli: true to run anyway.",
				id, version, verifier.VerifiedRange()), nil)
		}
		if !verified {
			reporter.ProviderCLIUnverified(id, version, verifier.VerifiedRange())
		}
		found[id] = domain.ProviderCLI{Version: version, Verified: verified}
	}
	return found, nil
}

// providerCLIRange is the verified range the provider's adapter declares, "" if none.
func providerCLIRange(registry *provider.Registry, id string) string {
	client, err := registry.Resolve(id)
	if err != nil {
		return ""
	}
	if v, ok := client.(provider.VersionVerifier); ok {
		return v.VerifiedRange()
	}
	return ""
}

// newRegistry registers every supported provider adapter with executor.
func newRegistry(executor execution.ProcessExecutor) *provider.Registry {
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(executor))
	registry.Register(provider.Codex, codex.New(executor))
	registry.Register(provider.Gemini, gemini.New(executor))
	return registry
}

// noJiraDependencies is the dependency check of a command that does not fetch a ticket:
// a jira section's variables are not its concern (specs/jira/SPEC.md §7.2).
func noJiraDependencies(config.Config) config.DependencyOptions { return config.DependencyOptions{} }

// runValidate checks the Jira variables whenever the section is present, so validate
// answers "would run --jira work here?", and asks Jev whether it serves decision.model.
func runValidate(ctx context.Context, configPath, claudeAuthFlag string, stdout, stderr io.Writer) error {
	executor := execution.NewOSProcessExecutor()
	reporter := logging.NewTerminalReporter(stdout)
	registry := newRegistry(executor)
	cfg, clis, org, _, err := loadAndValidateConfig(configPath, claudeAuthFlag, registry, executor, reporter,
		func(cfg config.Config) config.DependencyOptions {
			return config.DependencyOptions{Jira: cfg.Jira != nil}
		})
	if err != nil {
		return err
	}
	reporter.OrgConfig(org.Path)
	for _, id := range config.ProvidersInUse(cfg) {
		if cli, ok := clis[id]; ok {
			reporter.ProviderCLIVersion(id, cli.Version, providerCLIRange(registry, id), cli.Verified)
		}
	}
	checked, err := checkJevModel(ctx, cfg, stderr)
	if checked {
		reporter.JevModelServed(cfg.Decision.Model)
	}
	return err
}

// decisionsReport compares the two complexity questions over every decision.json under
// the runs directory (TASKS.md T18, reduced). It needs no configuration: it reads only
// what runs wrote, and calls nothing.
func decisionsReport(stdout io.Writer) error {
	store := persistence.NewStore(runsBaseDir)
	ids, err := store.RunIDs()
	if err != nil {
		return err
	}
	var entries []report.Entry
	var unreadable []report.Unreadable
	for _, id := range ids {
		trace, at, err := store.LoadDecision(id)
		var rse *apperr.RunStateError
		switch {
		case errors.As(err, &rse) && rse.Kind == apperr.RunNotFound:
			// The run stopped before a decision existed: nothing to compare.
		case err != nil:
			unreadable = append(unreadable, report.Unreadable{RunID: id, Err: err})
		default:
			entries = append(entries, report.Entry{RunID: id, At: at, Trace: trace})
		}
	}
	report.RenderDecisions(stdout, runsBaseDir, report.SummarizeDecisions(entries, unreadable))
	return nil
}

const decisionsUsage = "usage: orchestrator decisions report|calibrate [--runs <dir>] [--min-runs N] [--size-target S/M/C] [--simple-max-hard F]"

// defaultMinRuns is how many runs a probability bucket needs before calibrate says anything
// about it (specs/governance/SPEC.md §7.3).
const defaultMinRuns = 10

// calibrateArgs are the flags of `decisions calibrate`.
type calibrateArgs struct {
	dir           string
	minRuns       int
	target        report.SizeTarget
	simpleMaxHard float64
}

// decisionsCalibrateArgs parses what follows `decisions` for `calibrate`, each flag at most
// once and as `--flag value` or `--flag=value`: --runs <dir> (default: the runs directory of the
// current repository), --min-runs N (default 10, at least 1), --size-target S/M/C (default
// 50/35/15: three positive percentages summing to 100) and --simple-max-hard F (default 0.2,
// in (0, 1)).
func decisionsCalibrateArgs(args []string) (calibrateArgs, error) {
	bad := &apperr.ConfigError{Msg: decisionsUsage}
	if len(args) == 0 || args[0] != "calibrate" {
		return calibrateArgs{}, bad
	}
	out := calibrateArgs{dir: runsBaseDir, minRuns: defaultMinRuns, target: report.DefaultSizeTarget, simpleMaxHard: report.DefaultSimpleMaxHard}
	seen := map[string]bool{}
	for i := 1; i < len(args); i++ {
		flag, v, hasValue := strings.Cut(args[i], "=")
		if !hasValue {
			if i+1 >= len(args) {
				return calibrateArgs{}, bad
			}
			i++
			v = args[i]
		}
		if seen[flag] {
			return calibrateArgs{}, bad
		}
		seen[flag] = true
		ok := true
		switch flag {
		case "--runs":
			out.dir, ok = v, v != ""
		case "--min-runs":
			n, err := strconv.Atoi(v)
			out.minRuns, ok = n, err == nil && n >= 1
		case "--size-target":
			out.target, ok = parseSizeTarget(v)
		case "--simple-max-hard":
			f, err := strconv.ParseFloat(v, 64)
			out.simpleMaxHard, ok = f, err == nil && f > 0 && f < 1
		default:
			ok = false
		}
		if !ok {
			return calibrateArgs{}, bad
		}
	}
	return out, nil
}

// parseSizeTarget reads S/M/C: three positive integers summing to 100.
func parseSizeTarget(v string) (report.SizeTarget, bool) {
	parts := strings.Split(v, "/")
	if len(parts) != 3 {
		return report.SizeTarget{}, false
	}
	var t report.SizeTarget
	sum := 0
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return report.SizeTarget{}, false
		}
		t[i], sum = n, sum+n
	}
	return t, sum == 100
}

// decisionsCalibrate proposes testing and review thresholds from the runs found under dir
// (specs/governance/SPEC.md §7.3). It only reads: persistence.LoadRunDirs takes no lock and
// creates nothing, and no Store is built. It needs no configuration and calls nothing.
func decisionsCalibrate(stdout io.Writer, args calibrateArgs) error {
	dir, minRuns := args.dir, args.minRuns
	found, bad, err := persistence.LoadRunDirs(dir)
	if err != nil {
		return err
	}
	// The loader walks the root with its symlinks resolved.
	root := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		root = resolved
	}
	var entries []report.RunEntry
	for _, f := range found {
		entries = append(entries, report.RunEntry{RunID: f.RunID, At: f.At, Run: f.Run, Trace: f.Trace})
	}
	var unreadable []report.UnreadableRun
	for _, b := range bad {
		path := b.Dir
		if r, err := filepath.Rel(root, b.Dir); err == nil {
			path = r
		}
		unreadable = append(unreadable, report.UnreadableRun{Path: path, Err: b.Err.Error()})
	}
	c := report.Calibrate(entries, unreadable, minRuns)
	c.Shape = report.Shape(entries, args.target, minRuns, args.simpleMaxHard)
	c.SizeTarget, c.SimpleMaxHard = args.target, args.simpleMaxHard
	report.RenderCalibration(stdout, dir, c)
	return nil
}

const runsReportUsage = "usage: orchestrator runs report [--dir <path>]"

// runsReportArgs parses what follows `runs`: `report`, and --dir <path> or --dir=<path>
// (default: the runs directory of the current repository).
func runsReportArgs(args []string) (string, error) {
	if len(args) == 0 || args[0] != "report" {
		return "", &apperr.ConfigError{Msg: runsReportUsage}
	}
	dir := runsBaseDir
	given := false
	for i := 1; i < len(args); i++ {
		v, ok := strings.CutPrefix(args[i], "--dir=")
		switch {
		case ok:
		case args[i] == "--dir" && i+1 < len(args):
			i++
			v = args[i]
		default:
			return "", &apperr.ConfigError{Msg: runsReportUsage}
		}
		if given || v == "" {
			return "", &apperr.ConfigError{Msg: runsReportUsage}
		}
		dir, given = v, true
	}
	return dir, nil
}

// runsReport summarises every run found under dir, then compares the two complexity
// questions over their decision traces. It needs no configuration and calls nothing.
func runsReport(stdout io.Writer, dir string) error {
	found, bad, err := persistence.LoadRunDirs(dir)
	if err != nil {
		return err
	}
	// The loader walks the root with its symlinks resolved.
	root := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		root = resolved
	}
	rel := func(path string) string {
		if r, err := filepath.Rel(root, path); err == nil {
			return r
		}
		return path
	}
	var entries []report.RunEntry
	var traces []report.Entry
	for _, f := range found {
		entries = append(entries, report.RunEntry{RunID: f.RunID, At: f.At, Run: f.Run, Trace: f.Trace})
		if f.Trace != nil {
			traces = append(traces, report.Entry{RunID: f.RunID, At: f.At, Trace: *f.Trace})
		}
	}
	var unreadable []report.UnreadableRun
	for _, b := range bad {
		unreadable = append(unreadable, report.UnreadableRun{Path: rel(b.Dir), Err: b.Err.Error()})
	}
	// Unreadable runs are listed by the runs section, not again by the decisions one.
	report.RenderRuns(stdout, dir, report.SummarizeRuns(entries, unreadable), report.SummarizeDecisions(traces, nil))
	return nil
}

// runTask runs a new task. text is the task typed on the command line or, with jiraRef
// (run --jira), the operator's notes on the ticket, which may be empty.
func runTask(ctx context.Context, configPath, text, jiraRef string, stages []domain.Stage, allowRedTests bool, isolation string, mode domain.RunMode, claudeAuthFlag string, verbose bool, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := newReporter(stdout, verbose)
	defer reporter.Finish()
	// Warnings on stderr go to the same terminal; routed through the reporter they stop
	// the spinner and step around the sidebar.
	stderr = reporter.Wrap(stderr)

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	// Fail fast (MVP.md §7): the whole validation chain runs before any decision client
	// or translator exists and before any provider is called. With --jira it includes the
	// credential variables, and the reference is resolved before any request.
	cfg, clis, org, auth, err := loadAndValidateConfig(configPath, claudeAuthFlag, registry, osExec, reporter,
		func(config.Config) config.DependencyOptions { return config.DependencyOptions{Jira: jiraRef != ""} })
	if err != nil {
		return err
	}
	if isolation != "" {
		cfg.Execution.Isolation = isolation
	}
	reporter.TestCommand = cfg.Test.Command
	var jiraKey string
	if jiraRef != "" {
		if jiraKey, err = jira.ParseRef(jiraRef, cfg.Jira.BaseURL); err != nil {
			return err
		}
	}

	// execution.timeout starts here and covers translation, the pre-check, context
	// collection, classification and the workflow - but not the time the user spends on
	// a manual choice (see engineContext).
	started := time.Now()
	decideCtx, cancelDecide := context.WithTimeout(ctx, cfg.Execution.Timeout)
	defer cancelDecide()

	// Only when a provider decides: with --stage nothing is asked of Jev. A failure here
	// creates no run, as a failed fetch of the ticket does.
	if len(stages) == 0 {
		if _, err := checkJevModel(decideCtx, cfg, stderr); err != nil {
			return err
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to determine working directory", Err: err}
	}
	reporter.Cwd = cwd

	repo, err := repository.Open(decideCtx, cwd, osExec)
	if err != nil {
		return err
	}
	// Taken now, before the run directory exists: .orchestrator/runs/ may live inside the
	// worktree, and the run must not count its own files as uncommitted changes.
	dirty, err := repo.IsDirty(decideCtx)
	if err != nil {
		return err
	}

	var stream io.Writer
	if verbose {
		stream = reporter.StreamWriter()
	}

	task := domain.Task{Description: text, CreatedAt: time.Now()}
	var ticket domain.Ticket
	if jiraKey != "" {
		// The one Jira request. A failure creates no run: there is nothing to resume
		// without a task (specs/jira/SPEC.md J2).
		if ticket, task.Description, err = taskFromJira(decideCtx, cfg, jiraKey, text, reporter); err != nil {
			return err
		}
		task.Source = &domain.TaskSource{
			Kind: domain.TaskSourceJira, Key: ticket.Key, RequestedKey: ticket.RequestedKey,
			URL: ticket.URL, FetchedAt: ticket.FetchedAt, Notes: strings.TrimSpace(text) != "",
		}
	}

	store := persistence.NewStore(runsBaseDir)
	run, err := store.NewRun(task)
	if err != nil {
		return err
	}
	run.ProviderCLI = clis
	run.OrgConfig = orgRef(org)
	run.ClaudeAuth = &auth
	run.AllowRedTests = allowRedTests
	run.Options = runOptions(cfg)
	run.Options.Isolation = cfg.Execution.IsolationMode()
	// Held until the run returns, so a resume cannot take the run over while it is alive
	// (specs/resume/SPEC.md §11).
	unlock, err := store.Lock(run.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if task.Source != nil {
		if err := store.SaveTicket(run.ID, ticket); err != nil {
			return failRun(store, run, err)
		}
	}
	if reporter.Pretty {
		reporter.SetRepoInfo(repoInfo(decideCtx, repo))
	}
	reporter.RunHeader(run.ID, store.RunDir(run.ID))

	// One prompter for every question on stdin: it buffers input, so a second reader
	// would swallow answers meant for the first.
	prompter := reporter.PausingPrompter(interaction.NewTerminalPrompter(stdin, stdout))
	interactive := mode != domain.ModeAuto && isTerminal(stdin)

	var out decisionOutcome
	if len(stages) > 0 {
		out, err = decideManual(stages, task.Description, reporter)
	} else {
		out, err = decideWithProvider(decideCtx, ctx, cfg, registry, store, repo, cwd, run, task.Description,
			reporter, prompter, interactive, stream, stderr)
	}
	if err != nil {
		return failRun(store, run, err)
	}

	wf, err := workflow.Build(out.decision.Stages)
	if err != nil {
		return failRun(store, run, &apperr.WorkflowError{Msg: "failed to build the workflow", Err: err})
	}
	// Persisted before the dirty-tree refusal, so a refused run keeps its decided workflow
	// and can be resumed once the tree is clean (specs/resume/SPEC.md G10).
	run.Decision = &out.decision
	run.Workflow = &wf
	reporter.SetRun(run)
	trace := out.trace(wf)
	trace.Models = modelRoutes(cfg, out.decision, wf)
	if err := store.SaveDecision(run.ID, trace); err != nil {
		return err
	}
	if err := store.SaveProjectContext(run.ID, out.project); err != nil {
		return err
	}
	if err := store.SaveRun(run); err != nil {
		return err
	}
	reporter.Pause()
	if err := checkWorktree(decideCtx, repo, cfg, wf, dirty, stderr); err != nil {
		return failRun(store, run, err)
	}
	// The review screen shows the stages itself, editable; the decision block above it
	// then leaves its read-only stage table out.
	editable := mode != domain.ModeAuto && reporter.Pretty && reviewScreenAvailable(stdin) &&
		run.Decision.Source != domain.DecisionSourceManual
	reporter.ReviewForm = editable
	out.announce(wf, stderr)
	warnPathRules(stderr, run.Options.PathRules, wf)

	s := newSession(ctx, &cfg, configPath, registry, osExec, repo, store, run, reporter, prompter, stream, stdout)
	s.stdin = stdin
	return executeWorkflow(ctx, s, tailOptions{
		mode:     mode,
		dirty:    dirty,
		editable: editable,
		// An edit on the review screen replaces the decision before anything ran: the
		// workflow is rebuilt from it, the worktree rule re-checked (adding implement can
		// make a dirty tree a refusal), and run.json and decision.json rewritten, so a
		// resume continues exactly what was approved.
		onEdit: func(d domain.Decision) error {
			nwf, err := workflow.Build(d.Stages)
			if err != nil {
				return failRun(store, run, &apperr.WorkflowError{Msg: "failed to build the edited workflow", Err: err})
			}
			if err := checkWorktree(decideCtx, repo, cfg, nwf, dirty, stderr); err != nil {
				return failRun(store, run, err)
			}
			run.Decision, run.Workflow = &d, &nwf
			// The edit may have dropped a stage a rule needs, or added implement.
			warnPathRules(stderr, run.Options.PathRules, nwf)
			trace := out.trace(nwf)
			trace.Complexity, trace.Stages, trace.Edit = d.Complexity, d.Stages.Stages, d.Edit
			trace.Models = modelRoutes(cfg, d, nwf)
			if err := store.SaveDecision(run.ID, trace); err != nil {
				return err
			}
			reporter.SetRun(run)
			return store.SaveRun(run)
		},
		engineContext: func() (context.Context, context.CancelFunc) {
			return engineContext(ctx, decideCtx, cfg.Execution.Timeout, time.Since(started), out.manualChoice)
		},
	})
}

// checkWorktree applies the clean-worktree rule to a run that has not executed a step: a
// workflow that writes must start from a clean restore point (specs/permissions/SPEC.md
// §7.2). It is checked before any approval prompt, so nobody approves a run that cannot
// start. A dirty tree that is allowed only gets a warning.
//
// A worktree run (cfg.WorktreeRun, with run --inplace/--worktree already applied) writes in a fresh checkout
// of HEAD, so the operator's dirty tree is never a refusal and security.require_clean_worktree
// does not apply: it only gets the warning that the changes are not part of the run
// (specs/worktree/SPEC.md §6).
func checkWorktree(ctx context.Context, repo *repository.Repository, cfg config.Config, wf domain.Workflow, dirty bool, stderr io.Writer) error {
	if !dirty {
		return nil
	}
	if cfg.WorktreeRun(wf) {
		warnDirtyWorktreeRun(ctx, repo, stderr)
		return nil
	}
	if wf.Contains(domain.ActionImplement) && cfg.Security.CleanWorktreeRequired() {
		return &apperr.ConfigError{Msg: "the workflow includes implement, but the repository has uncommitted changes\n\n" +
			"Commit or stash them first, so the run starts from a clean restore point,\n" +
			"or set security.require_clean_worktree: false to run on top of them."}
	}
	logging.DirtyRepoWarning(stderr)
	return nil
}

// warnDirtyWorktreeRun prints the warning of a worktree run that starts beside a dirty tree.
// The run's own directory does not count, as in IsDirty. If git cannot say which commit or how
// many changes, the warning says so in general terms rather than failing the run.
func warnDirtyWorktreeRun(ctx context.Context, repo *repository.Repository, stderr io.Writer) {
	const tail = "(commit them first, or run with --inplace)."
	if repo != nil {
		head, herr := repo.HeadRef(ctx)
		files, ferr := repo.ChangedFiles(ctx)
		if herr == nil && ferr == nil {
			n := 0
			for _, f := range files {
				if f != ".orchestrator" && !strings.HasPrefix(f, ".orchestrator/") {
					n++
				}
			}
			if len(head) > 7 {
				head = head[:7]
			}
			fmt.Fprintf(stderr, "Warning: the run starts from HEAD %s; %d uncommitted changes in your tree are not part of it %s\n", head, n, tail)
			return
		}
	}
	fmt.Fprintf(stderr, "Warning: the run starts from HEAD; the uncommitted changes in your tree are not part of it %s\n", tail)
}

// session is what the part of a run after the decision needs. run and resume each build
// one, and both hand it to executeWorkflow.
type session struct {
	cfg      *config.Config
	repo     *repository.Repository
	store    *persistence.Store
	run      *domain.Run
	reporter *logging.ConsoleReporter
	prompter interaction.Prompter
	stdout   io.Writer
	// stdin feeds the review screen; it is drawn only when stdin is a terminal.
	stdin      io.Reader
	runner     *testexec.Runner
	dispatcher *action.Dispatcher
	// protected are the patterns the run must never change: security.protected_paths and
	// the config file. They make the run-start baseline; the step guard adds the runs
	// directory's resume inputs, which change with every step.
	protected []string
	// runsRel is the runs directory relative to the repository top level, or "".
	runsRel string
	// worktree is the run's worktree once the session switched to it (switchToWorktree);
	// nil in place.
	worktree *domain.RunWorktree
	// operator is the operator's repository once the session switched to a worktree, where
	// the end of the run checks for conflicts and removes the worktree; operatorFingerprint
	// is its git fingerprint right after the worktree was created, or on a resume right after
	// the worktree was verified (resumeInWorktree). nil and "" in place.
	operator            *repository.Repository
	operatorFingerprint string
}

// newSession builds the Dispatcher and resolves the guarded paths. Nothing here calls a
// provider or runs git status: resume builds it before the baseline check, to restore the
// Dispatcher's context from the artifacts.
func newSession(ctx context.Context, cfg *config.Config, configPath string, registry *provider.Registry, osExec execution.ProcessExecutor,
	repo *repository.Repository, store *persistence.Store, run *domain.Run, reporter *logging.ConsoleReporter,
	prompter interaction.Prompter, stream, stdout io.Writer) *session {
	runner := testexec.New(osExec)
	dispatcher := action.New(cfg, registry, repo, runner)
	dispatcher.Artifacts = store
	dispatcher.Logs = store
	dispatcher.Observer = reporter
	dispatcher.Stream = stream
	runsRel := worktreeRel(ctx, repo, runsBaseDir)
	dispatcher.RunsDir = runsRel
	return &session{
		cfg: cfg, repo: repo, store: store, run: run, reporter: reporter, prompter: prompter, stdout: stdout,
		runner: runner, dispatcher: dispatcher,
		protected: slices.Concat(cfg.Security.ProtectedPaths, configPattern(ctx, repo, configPath)),
		runsRel:   runsRel,
	}
}

// tailOptions is how run and resume differ in the shared tail.
type tailOptions struct {
	// mode is --mode; empty asks.
	mode domain.RunMode
	// editable lets the review screen change stages and complexity (a fresh run with a
	// decided workflow). onEdit applies such a change: rebuild, re-check, persist.
	editable bool
	onEdit   func(domain.Decision) error
	// defaultMode is what an empty answer to the mode prompt picks; empty has no default.
	defaultMode domain.RunMode
	// dirty is whether a run without executions starts on uncommitted changes, which
	// makes its restore point a snapshot commit.
	dirty bool
	// resume is set by `resume`: the record appended to run.Resumes once the operator
	// approved. Until then the persisted run is left exactly as it was found.
	resume *domain.Resume
	// engineContext starts the workflow's share of execution.timeout.
	engineContext func() (context.Context, context.CancelFunc)
}

// executeWorkflow is everything after the decision, shared by run and resume: the
// authority line, approval and mode, the restore point and run-start baseline (for a run
// that has not executed a step), the test sandbox, the step guard, the engine, the
// final answer and the commit offer.
func executeWorkflow(ctx context.Context, s *session, opts tailOptions) error {
	run, store, cfg := s.run, s.store, s.cfg
	removeStaleVerifyDirs(os.TempDir(), time.Now())
	s.reporter.Authority(*run.Workflow, cfg.Test.Command, cfg.Test.Sandboxed())
	if !cfg.Security.SecretScan.Enabled() {
		s.reporter.SecretScanOff()
	}
	if len(run.Executions) > 0 && run.PreRunRef != "" {
		s.reporter.RestorePoint(run.PreRunRef)
	}

	mode := opts.mode
	if mode != domain.ModeAuto {
		resolvedMode, approved, err := review(s, mode, opts)
		if err != nil {
			if opts.resume != nil {
				return err
			}
			return failRun(store, run, err)
		}
		if !approved {
			if opts.resume == nil {
				run.Status = domain.RunStopped
				run.FailureReason = "cancelled by user"
				_ = writeOutcome(store, run)
			}
			return fmt.Errorf("run cancelled by user: %w", context.Canceled)
		}
		mode = resolvedMode
		if mode == domain.ModePlan {
			// A plan-mode resume changes nothing: the run stays resumable as it was.
			if opts.resume == nil {
				run.Mode = mode
				if err := store.SaveRun(run); err != nil {
					return err
				}
			}
			s.reporter.PlanApproved()
			return nil
		}
	}
	run.Mode = mode
	if opts.resume != nil {
		record := *opts.resume
		record.At = time.Now()
		record.Mode = mode
		run.Resumes = append(run.Resumes, record)
		run.Status = domain.RunRunning
		run.FailureReason = ""
		// The previous attempt's outcome no longer describes a running run.
		run.Outcome = nil
	}
	if err := store.SaveRun(run); err != nil {
		return err
	}

	// The engine's share of execution.timeout starts here: worktree.setup runs inside it.
	runCtx, cancelRun := opts.engineContext()
	defer cancelRun()

	if len(run.Executions) == 0 {
		// Everything below reads s.repo - the restore point, the baseline, the sandbox, the
		// step guard, the secret baseline, the observer and the gate - so a worktree run
		// switches to its worktree first.
		if err := enterWorktree(ctx, s); err != nil {
			return failRun(store, run, err)
		}
		// worktree.setup comes before the restore point and the baseline, so they include what
		// it installed, and before the pre-run test, which may need it.
		if err := runSetup(runCtx, s); err != nil {
			return failRun(store, run, err)
		}
		if s.worktree != nil {
			// A fresh checkout of base_ref: the base is the restore point, with no snapshot.
			run.PreRunRef = s.worktree.BaseRef
			s.reporter.RestorePoint(run.PreRunRef)
		} else {
			recordRestorePoint(ctx, s.repo, run, opts.dirty, s.reporter)
		}
		// The guard over what the run must never change: the run-start baseline a resume
		// is checked against.
		if err := recordBaseline(ctx, s.repo, run, s.newRepoGuard(s.protected)); err != nil {
			return failRun(store, run, err)
		}
		if err := store.SaveRun(run); err != nil {
			return err
		}
	}
	// Pinned on every approved run and resume, so a run recorded before pinning existed
	// gets its ref too.
	pinRestorePoint(ctx, s.repo, run, s.reporter)

	if cfg.Test.Sandboxed() {
		sandbox, err := testSandbox(ctx, s.repo, cfg.Test, s.readOnlyFiles())
		if err != nil {
			return failRun(store, run, err)
		}
		s.runner.Sandbox = sandbox
	}
	// The step guard also covers the runs directory's resume inputs, which change with
	// every step and so cannot be part of the baseline.
	s.dispatcher.Guard = s.newRepoGuard(slices.Concat(s.protected, action.RunDirPatterns(s.runsRel)))

	if needsPreRunTest(*cfg, run) {
		if err := preRunTest(runCtx, s); err != nil {
			return failRun(store, run, err)
		}
	}
	// What already looks like a secret before the first step - a dirty start, fixtures the
	// pre-run test generated - is not the run's doing (specs/hardening/SPEC.md §9.4). A
	// resume of a run that has executed a step keeps the baseline it started with.
	if len(run.Executions) == 0 && cfg.Security.SecretScan.Enabled() {
		baseline, err := s.dispatcher.SecretBaseline(runCtx)
		if err != nil {
			return failRun(store, run, err)
		}
		run.SecretScanBaseline = baseline
		if err := store.SaveRun(run); err != nil {
			return err
		}
	}
	s.dispatcher.OnSecret = s.reporter.SecretFound
	s.dispatcher.OnWarning = s.reporter.Warning

	s.reporter.MaxFixIterations = cfg.Limits.MaxFixIterations

	observer := &runObserver{reporter: s.reporter, store: store}
	if s.reporter.Pretty {
		observer.repo = s.repo
	}
	engine := &workflow.Engine{
		Executor: s.dispatcher,
		Observer: observer,
		Persist:  store.SaveRun,
		Limits:   limitsView(*cfg),
	}

	if mode == domain.ModeManual {
		gate := interaction.NewTerminalGate(s.prompter, s.reporter.PausingWriter())
		gate.Changes = s.repo.StatusSummary
		engine.Gate = gate
	}

	// Whether the engine finished or not, a terminal status gets its outcome.
	runErr := engine.Run(runCtx, run)
	outcomeErr := writeOutcome(store, run)
	if runErr != nil {
		if s.worktree != nil {
			// A failed or stopped run keeps its worktree: a resume continues there.
			s.reporter.WorktreeKept(s.worktree.Path, "")
		}
		return runErr
	}
	if outcomeErr != nil {
		return outcomeErr
	}
	if answer := s.dispatcher.FinalAnswer(run); answer != "" {
		s.reporter.Answer(answer)
	}
	// A worktree run commits on its own branch without the question, checks it for conflicts
	// and removes the worktree (specs/worktree/SPEC.md §10). Only where auto_commit would not
	// commit is an operator at the terminal asked; --mode auto asks nothing.
	if s.worktree != nil {
		sha := offerCommit(ctx, s, opts.mode != domain.ModeAuto && isTerminal(s.stdin))
		finishWorktree(ctx, s, sha)
		// summary.md was rendered before the commit: it gains the worktree line's commit,
		// conflicts and removal.
		saveSummary(store, run)
		return nil
	}
	// auto_commit needs nobody at the terminal; otherwise only when the operator is there:
	// --mode auto asks nothing. auto_commit does not apply to --mode manual.
	if (s.cfg.AutoCommit && run.Mode != domain.ModeManual) || (opts.mode != domain.ModeAuto && isTerminal(s.stdin)) {
		offerCommit(ctx, s, true)
	}
	return nil
}

// needsPreRunTest reports whether the test command runs before the first step
// (specs/hardening/SPEC.md §8.1): the workflow implements and tests, test.baseline is not
// off, and no step has run yet - a fresh run, or a resume of one that never got that far.
func needsPreRunTest(cfg config.Config, run *domain.Run) bool {
	return cfg.Test.PreRunTest() && len(run.Executions) == 0 && run.Workflow != nil &&
		run.Workflow.Contains(domain.ActionImplement) && run.Workflow.Contains(domain.ActionTest)
}

// preRunTest runs the checks and the test command once before the first step and records
// the result. A red check or suite stops the run - implement would be sent to fix failures
// it did not cause - unless the operator started it with --allow-red-tests, which makes the
// failure part of the task (§8.4, specs/quality/SPEC.md §5.5). Only the exit codes decide.
func preRunTest(ctx context.Context, s *session) error {
	run := s.run
	s.reporter.PreRunTestStarted()
	result, checkLogs, err := s.dispatcher.PreRunTest(ctx, run)
	s.reporter.PreRunTestFinished(result, run.AllowRedTests)
	if result != nil {
		run.PreRunTest = result
		if !result.Success || err != nil {
			_ = s.store.AppendCallLog(run.ID, "pre-run-test", "stdout", result.Stdout)
			_ = s.store.AppendCallLog(run.ID, "pre-run-test", "stderr", result.Stderr)
			for _, c := range checkLogs {
				// The name is checked again: it becomes part of a file name.
				if !domain.CheckNameRE.MatchString(c.Name) {
					continue
				}
				_ = s.store.AppendCallLog(run.ID, "pre-run-test."+c.Name, "stdout", c.Stdout)
				_ = s.store.AppendCallLog(run.ID, "pre-run-test."+c.Name, "stderr", c.Stderr)
			}
		}
	}
	if err != nil {
		return err
	}
	if !result.Success && !run.AllowRedTests {
		advice := "Fix them first. If the task is to make them pass, run it again with --allow-red-tests; " +
			"to skip this check for every run, set test.baseline: off."
		if check := result.FailedCheck; check != "" {
			return &apperr.WorkflowError{Msg: fmt.Sprintf("check %q fails before any change (pre-run test: %q exited %d; output in %s)\n\n%s",
				check, result.Command, result.ExitCode, s.store.CallLogPath(run.ID, "pre-run-test."+check, "stdout"), advice)}
		}
		return &apperr.WorkflowError{Msg: fmt.Sprintf("tests fail before any change (pre-run test exited %d; output in %s)\n\n%s",
			result.ExitCode, s.store.CallLogPath(run.ID, "pre-run-test", "stdout"), advice)}
	}
	return s.store.SaveRun(run)
}

// runOptions are the config rules a new run follows from here on; a resume reads them back from
// run.json and never from the config (specs/quality/SPEC.md §7.3).
func runOptions(cfg config.Config) domain.RunOptions {
	return domain.RunOptions{ReviewFixMedium: cfg.Review.FixMedium, PathRules: cfg.PathRules()}
}

func limitsView(cfg config.Config) domain.LimitsView {
	view := domain.LimitsView{
		MaxSteps:            cfg.Limits.MaxSteps,
		MaxFixIterations:    cfg.Limits.MaxFixIterations,
		StopWithoutProgress: cfg.Limits.StopsWithoutProgress(),
	}
	if cfg.Limits.MaxCostUSD != nil {
		view.MaxCostUSD = *cfg.Limits.MaxCostUSD
	}
	return view
}

// recordRestorePoint sets run.PreRunRef before the first step: HEAD on a clean tree, or a
// snapshot commit of everything the run starts on top of, untracked files included.
// Failing to record it (e.g. a repository without commits) is reported, not fatal.
func recordRestorePoint(ctx context.Context, repo *repository.Repository, run *domain.Run, dirty bool, reporter *logging.ConsoleReporter) {
	var ref string
	var err error
	if dirty {
		ref, err = repo.SnapshotCommit(ctx)
	} else {
		ref, err = repo.HeadRef(ctx)
	}
	if err != nil {
		reporter.RestorePointUnavailable(err)
		return
	}
	run.PreRunRef = ref
	reporter.RestorePoint(ref)
}

// pinRestorePoint keeps git gc from pruning run.PreRunRef (repository.PinRestorePoint).
// Like recording it, failing to pin is reported, not fatal: the restore point is still
// there now, it is only not protected from a later gc.
func pinRestorePoint(ctx context.Context, repo *repository.Repository, run *domain.Run, reporter *logging.ConsoleReporter) {
	if run.PreRunRef == "" {
		return
	}
	if err := repo.PinRestorePoint(ctx, run.ID, run.PreRunRef); err != nil {
		reporter.RestorePointNotPinned(err)
	}
}

// recordBaseline sets run.HeadRef and run.ResumeBaseline before the first step
// (specs/resume/SPEC.md §9.1-§9.2). Unlike the restore point, a baseline that cannot be
// taken stops the run: without it the run could never be resumed safely, and the guard
// takes the same snapshot around every implement and test anyway.
func recordBaseline(ctx context.Context, repo *repository.Repository, run *domain.Run, guard *action.RepoGuard) error {
	if head, err := repo.HeadRef(ctx); err == nil {
		run.HeadRef = head
	}
	state, err := guard.Snapshot(ctx)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to record the protected state before the first step", Err: err}
	}
	baseline, err := json.Marshal(state)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to record the protected state before the first step", Err: err}
	}
	run.ResumeBaseline = baseline
	return nil
}

// testSandbox resolves test.sandbox: bwrap into the paths the sandbox binds: the
// repository root writable, its git directories and roFiles (a worktree's .git file)
// read-only over it, $HOME hidden, and the configured binds.
func testSandbox(ctx context.Context, repo *repository.Repository, t config.TestConfig, roFiles []string) (*testexec.Sandbox, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &apperr.ConfigError{Msg: "test.sandbox: cannot determine the home directory to hide", Err: err}
	}
	top, err := repo.TopLevel(ctx)
	if err != nil {
		return nil, err
	}
	gitDirs, err := repo.GitDirs(ctx)
	if err != nil {
		return nil, err
	}
	ro, rw := t.ResolveBinds(home)
	return &testexec.Sandbox{Home: home, RepoRoot: top, GitDirs: gitDirs, ReadOnlyFiles: roFiles, ReadOnly: ro, ReadWrite: rw}, nil
}

// configPattern returns the config file's path relative to the repository root when it
// lives inside the worktree, so implement cannot change the test command the next run
// executes; nil when the config is outside the worktree.
func configPattern(ctx context.Context, repo *repository.Repository, configPath string) []string {
	if rel := worktreeRel(ctx, repo, configPath); rel != "" {
		return []string{rel}
	}
	return nil
}

// worktreeRel returns p relative to the repository root, slash-separated, or "" when p is
// outside the worktree or cannot be resolved.
func worktreeRel(ctx context.Context, repo *repository.Repository, p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	// git reports the top level with symlinks resolved; so must p be, or a checkout
	// reached through a symlink would put every path "outside" the worktree.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	top, err := repo.TopLevel(ctx)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// newDecisionProvider builds the configured decision provider (TASKS.md T22): Jev over
// HTTP, its offline mock, or a CLI provider. The Jev API key is read from the
// environment here and handed only to the client's constructor.
func newDecisionProvider(cfg config.Config, registry *provider.Registry, store *persistence.Store, runID string, reporter *logging.ConsoleReporter, stream, stderr io.Writer) (decision.Provider, error) {
	d := cfg.Decision
	sources := d.Complexity.Sources()

	if d.UsesJev() {
		var client jev.Client
		if d.Jev.Mock {
			fmt.Fprintln(stderr, "Warning: decision.jev.mock is on - decisions come from an offline keyword heuristic, not from Jev.")
			client = jev.NewMockClient()
		} else {
			client = newJevHTTPClient(d)
		}
		p, err := jev.NewProvider(client, d.Model, sources)
		if err != nil {
			return nil, &apperr.WorkflowError{Msg: "failed to build the jev decision provider", Err: err}
		}
		return p, nil
	}

	client, err := registry.Resolve(d.Provider)
	if err != nil {
		return nil, &apperr.WorkflowError{Msg: "failed to resolve the decision provider", Err: err}
	}
	return &llm.Provider{
		Client: client, ProviderID: d.Provider, Model: d.Model, Timeout: d.Timeout, Sources: sources,
		RunID: runID, Logger: store, Observer: logging.CallReporter{ConsoleReporter: reporter, Label: "Deciding the workflow"}, Stream: stream,
	}, nil
}

// newJevHTTPClient builds the client of the real Jev API. The key is read from the
// environment here and handed only to the client's constructor.
func newJevHTTPClient(d config.DecisionConfig) jev.Client {
	return jev.NewHTTPClient(jev.Options{BaseURL: d.Jev.BaseURL, Timeout: d.Timeout, MaxRetries: d.Jev.MaxRetries}, os.Getenv(d.Jev.APIKeyEnv))
}

// checkJevModel runs jev.CheckModel against the real Jev API (TASKS.md T14), after the
// fail-fast chain and before anything is decided. The mock and the CLI providers have
// nothing to check: checked is false.
//
// An unserved model, a rejected key and a cancellation always stop. Any other failure to
// check - Jev unreachable, overloaded, or answering /v1/models in an unexpected shape -
// stops only with on_failure: fail; with ask it is a warning on stderr, because the
// decision rounds fall back to the manual choice anyway, and the shape of the models
// answer has not been verified against the real API yet.
func checkJevModel(ctx context.Context, cfg config.Config, stderr io.Writer) (checked bool, err error) {
	d := cfg.Decision
	if !d.UsesJev() || d.Jev.Mock {
		return false, nil
	}
	err = jev.CheckModel(ctx, newJevHTTPClient(d), d.Model)
	var derr *apperr.DecisionError
	if errors.As(err, &derr) && derr.Kind != apperr.DecisionAuth && derr.Kind != apperr.DecisionCancelled &&
		d.OnFailure == config.OnFailureAsk {
		logging.JevModelUnchecked(stderr, err)
		return false, nil
	}
	return err == nil, err
}

// engineContext is the workflow's share of execution.timeout. Without a manual choice
// it is just the deciding context, deadline unchanged. When the user chose by hand,
// their thinking time is given back: the workflow gets execution.timeout minus the time
// the decision phase itself took.
func engineContext(ctx, decideCtx context.Context, timeout, elapsed, userTime time.Duration) (context.Context, context.CancelFunc) {
	if userTime <= 0 {
		return decideCtx, func() {}
	}
	return context.WithTimeout(ctx, timeout-(elapsed-userTime))
}

// newReporter is the run/resume console reporter: pretty on an interactive terminal,
// repoInfo gathers the repository card for the pretty header: branch, the last few
// commits and how many files have uncommitted changes. Failures just leave parts empty.
func repoInfo(ctx context.Context, repo *repository.Repository) logging.RepoInfo {
	const shown = 4
	var info logging.RepoInfo
	info.Branch, _ = repo.Branch(ctx)
	commits, total, _ := repo.RecentCommits(ctx, shown)
	for _, c := range commits {
		info.Commits = append(info.Commits, logging.CommitInfo{Hash: c.Hash, Subject: c.Subject, When: c.When})
	}
	if total > len(commits) {
		info.MoreCommits = total - len(commits)
	}
	if changed, err := repo.ChangedFiles(ctx); err == nil {
		info.Changed = len(changed)
	}
	return info
}

// newReporter is the run/resume console reporter: pretty on an interactive terminal,
// the spec's plain text anywhere else.
func newReporter(stdout io.Writer, verbose bool) *logging.ConsoleReporter {
	r := logging.NewTerminalReporter(stdout)
	r.Verbose = verbose
	r.Version = Version
	return r
}

// isTerminal reports whether stdin can answer questions. A real stdin that is not a
// terminal (a pipe, /dev/null, a CI runner) cannot - then the manual choice fails at
// once instead of reading EOF. Any other reader is a caller-scripted input (tests).
func isTerminal(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// failRun records a failure that ended the run before the workflow started.
func failRun(store *persistence.Store, run *domain.Run, err error) error {
	run.Status = domain.RunFailed
	run.FailureReason = err.Error()
	_ = writeOutcome(store, run)
	return err
}

// writeOutcome records how a run that reached a terminal status ended and what it cost,
// saves the run, and renders its summary.md (MVP.md §43). It computes over the whole
// history, so a resume keeps the usage of the attempts before it. A run that is not
// terminal is left alone. Without a readable decision.json (none yet, or one the strict
// loader refuses) the outcome leaves the decision calls out rather than failing a run that
// already ended.
func writeOutcome(store *persistence.Store, run *domain.Run) error {
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunStopped:
	default:
		return nil
	}
	trace := loadTrace(store, run.ID)
	outcome := domain.ComputeOutcome(*run, trace)
	run.Outcome = &outcome
	if err := store.SaveRun(run); err != nil {
		return err
	}
	// summary.md is for a reader and never read back; a run that ended is not failed
	// over it.
	_ = store.SaveSummary(run.ID, report.RunSummary(*run, trace))
	return nil
}

// saveSummary renders summary.md again for a run whose record changed after writeOutcome,
// such as a worktree run's commit and removal. Like writeOutcome, it never fails the run.
func saveSummary(store *persistence.Store, run *domain.Run) {
	_ = store.SaveSummary(run.ID, report.RunSummary(*run, loadTrace(store, run.ID)))
}

// loadTrace is the run's decision.json, or nil when there is none or the strict loader
// refuses it.
func loadTrace(store *persistence.Store, runID string) *domain.DecisionTrace {
	if t, _, err := store.LoadDecision(runID); err == nil {
		return &t
	}
	return nil
}

// runObserver renders CLI progress via ConsoleReporter and, alongside that, persists
// each step's raw provider/test output to the run's log files.
type runObserver struct {
	reporter *logging.ConsoleReporter
	store    *persistence.Store
	// repo, when set (pretty renderer), lets the observer show what implement changed:
	// the diff between the trees the step recorded around itself.
	repo *repository.Repository
}

func (o *runObserver) StepStarted(run *domain.Run, index int, action domain.ActionType) {
	o.reporter.StepStarted(run, index, action)
}

func (o *runObserver) StepFinished(run *domain.Run, index int, exec domain.StepExecution, outcome workflow.StepOutcome) {
	if outcome.RawStdout != "" {
		_ = o.store.AppendLog(run.ID, index, exec.Action, "stdout", outcome.RawStdout)
	}
	if outcome.RawStderr != "" {
		_ = o.store.AppendLog(run.ID, index, exec.Action, "stderr", outcome.RawStderr)
	}
	for _, c := range outcome.CheckLogs {
		if c.Stdout != "" {
			_ = o.store.AppendCheckLog(run.ID, index, exec.Action, c.Name, "stdout", c.Stdout)
		}
		if c.Stderr != "" {
			_ = o.store.AppendCheckLog(run.ID, index, exec.Action, c.Name, "stderr", c.Stderr)
		}
	}
	for _, c := range outcome.VerifyLogs {
		if c.Stdout != "" {
			_ = o.store.AppendCheckLog(run.ID, index, exec.Action, c.Name, "stdout", c.Stdout)
		}
		if c.Stderr != "" {
			_ = o.store.AppendCheckLog(run.ID, index, exec.Action, c.Name, "stderr", c.Stderr)
		}
	}
	// Only after a completed implement: the step guard has already checked that nothing
	// protected (git config, hooks) changed, so running git here is safe.
	if o.repo != nil && exec.Action == domain.ActionImplement && exec.Status == domain.StepCompleted &&
		outcome.TreeBefore != "" && outcome.TreeAfter != "" && outcome.TreeBefore != outcome.TreeAfter {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if diff, err := o.repo.DiffTrees(ctx, outcome.TreeBefore, outcome.TreeAfter); err == nil {
			o.reporter.SetStepDiff(diff)
		}
		cancel()
	}
	o.reporter.StepFinished(run, index, exec, outcome)
}

func (o *runObserver) RunFinished(run *domain.Run) {
	o.reporter.RunFinished(run)
}

// review asks the operator to approve the workflow and resolves the mode. On an
// interactive terminal it is the review screen (logging.Review): stages and complexity
// editable on a fresh run, buttons instead of typed answers. Anywhere else - a pipe, a
// test, an unsupported terminal - it is the line prompt it always was.
func review(s *session, pinnedMode domain.RunMode, opts tailOptions) (domain.RunMode, bool, error) {
	run := s.run
	if s.stdin != nil && run.Decision != nil {
		res, err := s.reporter.Review(s.stdin, logging.ReviewInput{
			Decision:    *run.Decision,
			Policy:      s.cfg.DecisionPolicy(),
			AskMode:     pinnedMode == "",
			DefaultMode: firstMode(opts.defaultMode, domain.ModeAcceptEdits),
			Editable:    opts.editable && opts.onEdit != nil,
			Build: func(sel domain.StageSelection) ([]domain.ActionType, error) {
				wf, err := workflow.Build(sel)
				if err != nil {
					return nil, err
				}
				out := make([]domain.ActionType, len(wf.Steps))
				for i, st := range wf.Steps {
					out[i] = st.Action
				}
				return out, nil
			},
		})
		switch {
		case errors.Is(err, logging.ErrReviewUnavailable):
			// fall through to the line prompt
		case errors.Is(err, tui.ErrInterrupted):
			return "", false, nil
		case err != nil:
			return "", false, err
		default:
			if !res.Approved {
				return "", false, nil
			}
			if res.Decision.Edit != nil {
				if err := opts.onEdit(res.Decision); err != nil {
					return "", false, err
				}
			}
			mode := pinnedMode
			if mode == "" {
				mode = res.Mode
			}
			return mode, true, nil
		}
	}
	return interaction.ReviewAndSelectMode(s.prompter, s.stdout, pinnedMode, opts.defaultMode)
}

func firstMode(modes ...domain.RunMode) domain.RunMode {
	for _, m := range modes {
		if m != "" {
			return m
		}
	}
	return ""
}

// reviewScreenAvailable reports whether the review screen can take stdin over (a terminal
// raw mode works on), so the decision block only leaves its stage table out when the
// form that replaces it will really be shown.
func reviewScreenAvailable(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	return ok && tui.Supported(f)
}

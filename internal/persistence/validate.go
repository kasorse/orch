package persistence

import (
	"fmt"
	"math"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/secretscan"
	"orchestrator/internal/testreport"
)

// validateRun applies the structural checks of specs/resume/SPEC.md §6.3 to a decoded
// run.json. It returns the first violation, phrased for the user.
func validateRun(run *domain.Run, runID string) error {
	if run.ID != runID {
		return fmt.Errorf("id is %q, want %q (the directory name)", run.ID, runID)
	}
	if !run.Status.Valid() {
		return fmt.Errorf("unknown status %q", run.Status)
	}
	if run.Mode != "" && !run.Mode.Valid() {
		return fmt.Errorf("unknown mode %q", run.Mode)
	}
	if run.Task.Source != nil {
		if err := validateTaskSource(*run.Task.Source); err != nil {
			return fmt.Errorf("task.source: %w", err)
		}
	}
	if run.Workflow != nil {
		for i, step := range run.Workflow.Steps {
			if !step.Action.Valid() {
				return fmt.Errorf("workflow step %d: unknown action %q", i, step.Action)
			}
		}
	}

	if run.Decision != nil {
		if err := validateDecisionStages(run.Decision.Stages.Stages); err != nil {
			return fmt.Errorf("decision: %w", err)
		}
	}

	for i, r := range run.Options.PathRules {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("options.path_rules[%d]: %w", i, err)
		}
	}

	for i, exec := range run.Executions {
		if err := validateExecution(exec, i); err != nil {
			return fmt.Errorf("executions[%d]: %w", i, err)
		}
	}

	if err := validateProviderCLI(run.ProviderCLI); err != nil {
		return fmt.Errorf("provider_cli: %w", err)
	}
	if run.OrgConfig != nil {
		if err := run.OrgConfig.Validate(); err != nil {
			return fmt.Errorf("org_config: %w", err)
		}
	}
	if err := validateSecretScan(run); err != nil {
		return err
	}
	if t := run.PreRunTest; t != nil {
		if t.Success != (t.ExitCode == 0) || t.Duration < 0 {
			return fmt.Errorf("pre_run_test: inconsistent result (exit code %d, success %v, duration %s)", t.ExitCode, t.Success, t.Duration)
		}
		if err := validateChecks(*t); err != nil {
			return fmt.Errorf("pre_run_test: %w", err)
		}
		if err := validateReport(*t); err != nil {
			return fmt.Errorf("pre_run_test: %w", err)
		}
	}
	if o := run.Outcome; o != nil {
		if err := validateOutcome(o, run.Status); err != nil {
			return fmt.Errorf("outcome: %w", err)
		}
	}
	if run.Commit != "" {
		if !isCommitSHA(run.Commit) {
			return fmt.Errorf("commit %q is not a full commit SHA", run.Commit)
		}
		if run.Status != domain.RunCompleted {
			return fmt.Errorf("commit recorded on a %s run; only a completed run is committed", run.Status)
		}
	}
	for i, r := range run.Resumes {
		if err := validateProviderCLI(r.ProviderCLI); err != nil {
			return fmt.Errorf("resumes[%d].provider_cli: %w", i, err)
		}
		if r.OrgConfig != nil {
			if err := r.OrgConfig.Validate(); err != nil {
				return fmt.Errorf("resumes[%d].org_config: %w", i, err)
			}
		}
		if !r.FromStatus.Valid() {
			return fmt.Errorf("resumes[%d]: unknown status %q", i, r.FromStatus)
		}
		if !r.Entry.Valid() {
			return fmt.Errorf("resumes[%d]: unknown action %q", i, r.Entry)
		}
		if r.Mode != "" && !r.Mode.Valid() {
			return fmt.Errorf("resumes[%d]: unknown mode %q", i, r.Mode)
		}
	}
	return nil
}

func validateExecution(exec domain.StepExecution, i int) error {
	if exec.Index != i {
		return fmt.Errorf("index is %d, want %d", exec.Index, i)
	}
	if !exec.Action.Valid() {
		return fmt.Errorf("unknown action %q", exec.Action)
	}
	// The engine appends an execution only once it has finished, so a persisted entry
	// is never pending or running.
	if exec.Status != domain.StepCompleted && exec.Status != domain.StepFailed {
		return fmt.Errorf("status is %q, want completed or failed", exec.Status)
	}
	if exec.Provider != nil && exec.Provider.Authority != "" && !exec.Provider.Authority.Valid() {
		return fmt.Errorf("unknown authority %q", exec.Provider.Authority)
	}
	if exec.Provider != nil && exec.Provider.ModelSource != "" && !domain.ValidModelSource(exec.Provider.ModelSource) {
		return fmt.Errorf("unknown model_source %q", exec.Provider.ModelSource)
	}
	if exec.Attempt < 0 {
		return fmt.Errorf("attempt is %d", exec.Attempt)
	}
	if exec.Usage != nil {
		if err := exec.Usage.Validate(); err != nil {
			return fmt.Errorf("usage: %w", err)
		}
	}
	if exec.Call != nil {
		if err := exec.Call.Validate(); err != nil {
			return fmt.Errorf("call: %w", err)
		}
	}
	if exec.Diff != nil {
		if exec.Action != domain.ActionImplement {
			return fmt.Errorf("diff recorded on a %s step", exec.Action)
		}
		if err := exec.Diff.Validate(); err != nil {
			return fmt.Errorf("diff: %w", err)
		}
	}
	if exec.TreeAfter != "" {
		if exec.Action != domain.ActionImplement {
			return fmt.Errorf("tree_after recorded on a %s step", exec.Action)
		}
		if !isCommitSHA(exec.TreeAfter) {
			return fmt.Errorf("tree_after %q is not a git tree id", exec.TreeAfter)
		}
	}
	if err := validateVerifyCalls(exec); err != nil {
		return fmt.Errorf("verify_calls: %w", err)
	}
	if len(exec.PathRules) > 0 && exec.Action != domain.ActionImplement {
		return fmt.Errorf("path_rules recorded on a %s step", exec.Action)
	}
	for i, m := range exec.PathRules {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("path_rules[%d]: %w", i, err)
		}
	}
	if exec.TestResult != nil {
		if err := validateChecks(*exec.TestResult); err != nil {
			return fmt.Errorf("test_result: %w", err)
		}
		if err := validateReport(*exec.TestResult); err != nil {
			return fmt.Errorf("test_result: %w", err)
		}
	}
	if exec.ReviewResult != nil {
		if exec.Action != domain.ActionReview {
			return fmt.Errorf("review_result recorded on step action %s", exec.Action)
		}
		if err := exec.ReviewResult.ValidateCriteria(); err != nil {
			return fmt.Errorf("review_result.criteria: %w", err)
		}
		// A run.json is read back without ReviewResult.Validate, which only runs where
		// a review is parsed, so the reviewers (nested, model-answered, and the part that
		// breaks a resume if nested again) are checked here on their own.
		if err := exec.ReviewResult.ValidateReviewers(); err != nil {
			return fmt.Errorf("review_result.reviewers: %w", err)
		}
	}
	// The path is only compared, never used: readers derive it from index and action.
	if want := ArtifactRelPath(i, exec.Action); exec.ArtifactPath != "" && exec.ArtifactPath != want {
		return fmt.Errorf("artifact_path is %q, want %q", exec.ArtifactPath, want)
	}
	return nil
}

// validateVerifyCalls checks the verify calls an implement step recorded (specs/quality/
// SPEC.md §6.8, §12); each entry through domain.VerifyCall.Validate.
func validateVerifyCalls(exec domain.StepExecution) error {
	calls := exec.VerifyCalls
	if len(calls) == 0 {
		return nil
	}
	if exec.Action != domain.ActionImplement {
		return fmt.Errorf("recorded on a %s step", exec.Action)
	}
	if len(calls) > domain.MaxVerifyCalls {
		return fmt.Errorf("%d entries, at most %d", len(calls), domain.MaxVerifyCalls)
	}
	for i, c := range calls {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("[%d]: %w", i, err)
		}
	}
	return nil
}

// validateChecks checks the checks a test result lists and its failed_check
// (specs/quality/SPEC.md §5.3). A result written before checks existed has neither. The
// names end up in log file names, so each must have the shape a config allows.
//
// The sequence stops at the first non-zero exit, so a failed check is the last one listed and
// every earlier one passed; without a failed check every listed one passed, and the top-level
// result can only be red through test.command.
func validateChecks(t domain.TestResult) error {
	if len(t.Checks) > domain.MaxChecks {
		return fmt.Errorf("checks: %d entries, at most %d", len(t.Checks), domain.MaxChecks)
	}
	seen := map[string]bool{}
	for i, c := range t.Checks {
		switch {
		case !domain.CheckNameRE.MatchString(c.Name):
			return fmt.Errorf("checks[%d]: name %q is not a check name", i, c.Name)
		case seen[c.Name]:
			return fmt.Errorf("checks[%d]: name %q appears more than once", i, c.Name)
		case c.Success != (c.ExitCode == 0) || c.Duration < 0:
			return fmt.Errorf("checks[%d]: inconsistent result (exit code %d, success %v, duration %s)", i, c.ExitCode, c.Success, c.Duration)
		}
		seen[c.Name] = true
		if last := i == len(t.Checks)-1; !c.Success && !(last && t.FailedCheck == c.Name) {
			return fmt.Errorf("checks[%d]: %q failed, but the sequence goes on after it or failed_check does not name it", i, c.Name)
		}
	}
	if t.FailedCheck == "" {
		return nil
	}
	if !domain.CheckNameRE.MatchString(t.FailedCheck) {
		return fmt.Errorf("failed_check %q is not a check name", t.FailedCheck)
	}
	if n := len(t.Checks); n == 0 || t.Checks[n-1].Name != t.FailedCheck || t.Checks[n-1].Success {
		return fmt.Errorf("failed_check %q is not the last entry of checks, or that entry did not fail", t.FailedCheck)
	}
	if t.Success {
		return fmt.Errorf("failed_check %q on a successful result", t.FailedCheck)
	}
	return nil
}

// validateReport checks the failing tests a test result lists (specs/quality/SPEC.md §8.2,
// §12). They are only recorded for a failed run, in a known format, at most testreport.MaxNames
// of them and each a single line of at most testreport.MaxNameBytes. A result written before
// the fields existed has neither. The names reach a prompt, so a run directory edited by
// hand cannot smuggle a line break or an oversized name into one.
func validateReport(t domain.TestResult) error {
	if t.ReportFormat == "" && len(t.FailedTests) == 0 {
		return nil
	}
	switch {
	case t.Success:
		return fmt.Errorf("failed_tests or report_format on a successful result")
	case !testreport.KnownFormat(t.ReportFormat):
		return fmt.Errorf("report_format %q is not a known format", t.ReportFormat)
	case len(t.FailedTests) > testreport.MaxNames:
		return fmt.Errorf("failed_tests: %d entries, at most %d", len(t.FailedTests), testreport.MaxNames)
	}
	for i, n := range t.FailedTests {
		switch {
		case n == "" || len(n) > testreport.MaxNameBytes:
			return fmt.Errorf("failed_tests[%d]: %d bytes, want 1 to %d", i, len(n), testreport.MaxNameBytes)
		case strings.ContainsFunc(n, unicode.IsControl):
			return fmt.Errorf("failed_tests[%d]: has a control character", i)
		}
	}
	return nil
}

// validateTaskSource checks where a task came from (specs/jira/SPEC.md §11.1). The fields
// are only displayed, never used to make a request, but the run directory is
// model-writable, so they are validated like everything else read back.
func validateTaskSource(src domain.TaskSource) error {
	if src.Kind != domain.TaskSourceJira {
		return fmt.Errorf("unknown kind %q", src.Kind)
	}
	if !domain.ValidTicketKey(src.Key) {
		return fmt.Errorf("key %q is not an issue key", src.Key)
	}
	if src.RequestedKey != "" && !domain.ValidTicketKey(src.RequestedKey) {
		return fmt.Errorf("requested_key %q is not an issue key", src.RequestedKey)
	}
	if u, err := url.Parse(src.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q is not an absolute http(s) URL", src.URL)
	}
	if src.FetchedAt.IsZero() {
		return fmt.Errorf("fetched_at is not set")
	}
	return nil
}

// validateProviderCLI checks a recorded provider CLI map: known providers, printable
// versions of at most 64 bytes.
func validateProviderCLI(m map[string]domain.ProviderCLI) error {
	for id, cli := range m {
		if !slices.Contains(provider.SupportedProviders(), provider.ID(id)) {
			return fmt.Errorf("unknown provider %q", id)
		}
		if len(cli.Version) > 64 {
			return fmt.Errorf("%s: version is %d bytes, at most 64", id, len(cli.Version))
		}
		for _, r := range cli.Version {
			if !unicode.IsPrint(r) {
				return fmt.Errorf("%s: version has a non-printable character", id)
			}
		}
	}
	return nil
}

// maxSecretBaseline bounds secret_scan_baseline: a run.json is not a place for an unbounded
// list, and no real working tree starts with this many likely secrets.
const maxSecretBaseline = 10000

// validateSecretScan checks secret_scan_baseline and secret_findings
// (specs/hardening/SPEC.md §11): fingerprints are hex sha256, a finding names a known rule,
// a clean relative path, a line, and a step that exists or is the one being run.
func validateSecretScan(run *domain.Run) error {
	if len(run.SecretScanBaseline) > maxSecretBaseline {
		return fmt.Errorf("secret_scan_baseline: %d entries, at most %d", len(run.SecretScanBaseline), maxSecretBaseline)
	}
	for i, f := range run.SecretScanBaseline {
		if !isSHA256Hex(f) {
			return fmt.Errorf("secret_scan_baseline[%d]: not a fingerprint", i)
		}
	}
	rules := secretscan.RuleIDs()
	for i, f := range run.SecretFindings {
		switch {
		case !slices.Contains(rules, f.Rule):
			return fmt.Errorf("secret_findings[%d]: unknown rule %q", i, f.Rule)
		case !isSHA256Hex(f.Fingerprint):
			return fmt.Errorf("secret_findings[%d]: not a fingerprint", i)
		case f.Line < 1 || f.StepIndex < 0 || f.StepIndex > len(run.Executions):
			return fmt.Errorf("secret_findings[%d]: line %d / step %d out of range", i, f.Line, f.StepIndex)
		case f.File == "" || path.IsAbs(f.File) || path.Clean(f.File) != f.File || f.File == ".." || strings.HasPrefix(f.File, "../"):
			return fmt.Errorf("secret_findings[%d]: file %q is not a clean repository-relative path", i, f.File)
		}
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// validateOutcome checks the outcome run.json records: it describes the run's own status,
// which is a terminal one, and its counters are not negative. Nothing saves a run whose
// outcome is another status's — a resume drops the previous attempt's outcome before it
// saves the run as running — so a mismatch is a corrupt run.json.
func validateOutcome(o *domain.Outcome, runStatus domain.RunStatus) error {
	switch o.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunStopped:
	default:
		return fmt.Errorf("status %q is not a terminal status", o.Status)
	}
	if o.Status != runStatus {
		return fmt.Errorf("status %q is not the run's status %q", o.Status, runStatus)
	}
	if o.FixIterations < 0 || o.DurationMS < 0 || o.ActiveMS < 0 || o.VerifyCalls < 0 {
		return fmt.Errorf("negative counter")
	}
	if v := o.ReviewVerdict; v != "" && v != domain.ReviewPass && v != domain.ReviewFail {
		return fmt.Errorf("unsupported review verdict %q", v)
	}
	if err := o.Usage.Validate(); err != nil {
		return fmt.Errorf("usage: %w", err)
	}
	if o.Diff != nil {
		if err := o.Diff.Validate(); err != nil {
			return fmt.Errorf("diff: %w", err)
		}
	}
	d := o.DecisionOverhead
	if d.MS < 0 || d.Calls < 0 || d.CostUSD < 0 || math.IsNaN(d.CostUSD) || math.IsInf(d.CostUSD, 0) {
		return fmt.Errorf("decision_overhead: negative or non-finite value")
	}
	return nil
}

// validateDecisionUsage checks the usage decision.json records for each decision call.
// validateDecisionModels checks decision.json's models: each key an action and each source
// one a run can record.
func validateDecisionModels(models map[string]domain.ModelRoute) error {
	for action, r := range models {
		if !domain.ActionType(action).Valid() {
			return fmt.Errorf("models: unknown action %q", action)
		}
		if !domain.ValidModelSource(r.Source) {
			return fmt.Errorf("models.%s: unknown source %q", action, r.Source)
		}
	}
	return nil
}

// validatePolicyHash checks decision.json's policy_hash: absent (a manual decision, a trace
// written before the field), or the 64 lowercase hex digits domain.PolicyHash writes.
func validatePolicyHash(h string) error {
	if h != "" && !isSHA256Hex(h) {
		return fmt.Errorf("policy_hash %q is not 64 lowercase hex digits", h)
	}
	return nil
}

// validateDecisionStages checks decision.json's stages[].explored: a stage exploration turned
// on is enabled and not forced. The review screen clears the mark when it turns one off.
func validateDecisionStages(stages []domain.StageOutcome) error {
	for i, so := range stages {
		if so.Explored && (!so.Enabled || so.Forced) {
			return fmt.Errorf("stages[%d] (%s): explored needs the stage enabled and not forced", i, so.Stage)
		}
	}
	return nil
}

func validateDecisionUsage(u *domain.DecisionUsage) error {
	if u == nil {
		return nil
	}
	calls := []struct {
		name  string
		usage *domain.Usage
	}{{"translation", u.Translation}, {"context_depth", u.ContextDepth}, {"classification", u.Classification}}
	for _, c := range calls {
		if c.usage == nil {
			continue
		}
		if err := c.usage.Validate(); err != nil {
			return fmt.Errorf("usage.%s: %w", c.name, err)
		}
	}
	return nil
}

// isCommitSHA reports whether s is a full SHA-1 or SHA-256 object name in lowercase hex.
func isCommitSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

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

	for i, exec := range run.Executions {
		if err := validateExecution(exec, i); err != nil {
			return fmt.Errorf("executions[%d]: %w", i, err)
		}
	}

	if err := validateProviderCLI(run.ProviderCLI); err != nil {
		return fmt.Errorf("provider_cli: %w", err)
	}
	if err := validateSecretScan(run); err != nil {
		return err
	}
	if t := run.PreRunTest; t != nil && (t.Success != (t.ExitCode == 0) || t.Duration < 0) {
		return fmt.Errorf("pre_run_test: inconsistent result (exit code %d, success %v, duration %s)", t.ExitCode, t.Success, t.Duration)
	}
	if o := run.Outcome; o != nil {
		if err := validateOutcome(o, run.Status); err != nil {
			return fmt.Errorf("outcome: %w", err)
		}
	}
	for i, r := range run.Resumes {
		if err := validateProviderCLI(r.ProviderCLI); err != nil {
			return fmt.Errorf("resumes[%d].provider_cli: %w", i, err)
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
	if exec.Attempt < 0 {
		return fmt.Errorf("attempt is %d", exec.Attempt)
	}
	if exec.Usage != nil {
		if err := exec.Usage.Validate(); err != nil {
			return fmt.Errorf("usage: %w", err)
		}
	}
	// The path is only compared, never used: readers derive it from index and action.
	if want := ArtifactRelPath(i, exec.Action); exec.ArtifactPath != "" && exec.ArtifactPath != want {
		return fmt.Errorf("artifact_path is %q, want %q", exec.ArtifactPath, want)
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
	if o.FixIterations < 0 || o.DurationMS < 0 {
		return fmt.Errorf("negative counter")
	}
	if v := o.ReviewVerdict; v != "" && v != domain.ReviewPass && v != domain.ReviewFail {
		return fmt.Errorf("unsupported review verdict %q", v)
	}
	if err := o.Usage.Validate(); err != nil {
		return fmt.Errorf("usage: %w", err)
	}
	d := o.DecisionOverhead
	if d.MS < 0 || d.Calls < 0 || d.CostUSD < 0 || math.IsNaN(d.CostUSD) || math.IsInf(d.CostUSD, 0) {
		return fmt.Errorf("decision_overhead: negative or non-finite value")
	}
	return nil
}

// validateDecisionUsage checks the usage decision.json records for each decision call.
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

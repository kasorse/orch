package action

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/secretscan"
)

// locatedFinding is a secretscan finding with the file it is in.
type locatedFinding struct {
	file string
	secretscan.Finding
}

// maxListedFindings caps the findings a stop message lists.
const maxListedFindings = 20

// scanAdded scans what the working tree adds against HEAD (repository.AddedContent) and
// drops findings under security.secret_scan.allow_paths.
func (d *Dispatcher) scanAdded(ctx context.Context) ([]locatedFinding, error) {
	var exclude []string
	if d.RunsDir != "" {
		exclude = append(exclude, d.RunsDir)
	}
	files, err := d.Repo.AddedContent(ctx, exclude)
	if err != nil {
		return nil, &apperr.WorkflowError{Msg: "failed to read what the run added, for the secret scan", Err: err}
	}
	allow := d.Config.Security.SecretScan.AllowPaths
	var found []locatedFinding
	for _, f := range files {
		if f.Skipped || slices.ContainsFunc(allow, func(p string) bool { return domain.MatchProtected(p, f.Path) }) {
			continue
		}
		lines := make([]secretscan.NumberedLine, len(f.Lines))
		for i, l := range f.Lines {
			lines[i] = secretscan.NumberedLine{Number: l.Number, Text: l.Text}
		}
		for _, finding := range secretscan.ScanLines(f.Path, lines) {
			found = append(found, locatedFinding{file: f.Path, Finding: finding})
		}
	}
	return found, nil
}

// SecretBaseline returns the fingerprints of what already looks like a secret in the
// working tree, sorted and unique, for run.SecretScanBaseline (specs/hardening/SPEC.md
// §9.4). Only findings outside it count later.
func (d *Dispatcher) SecretBaseline(ctx context.Context) ([]string, error) {
	found, err := d.scanAdded(ctx)
	if err != nil {
		return nil, err
	}
	prints := make([]string, 0, len(found))
	for _, f := range found {
		prints = append(prints, f.Fingerprint)
	}
	slices.Sort(prints)
	return slices.Compact(prints), nil
}

// scanSecrets runs after step's guard check (implement, test): a finding that is neither in
// the run-start baseline nor already recorded stops the run (mode stop) or is recorded and
// reported (mode warn). The value never appears in the error, the record or the output.
func (d *Dispatcher) scanSecrets(ctx context.Context, run *domain.Run, step domain.ActionType) error {
	cfg := d.Config.Security.SecretScan
	if !cfg.Enabled() {
		return nil
	}
	found, err := d.scanAdded(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, f := range run.SecretScanBaseline {
		known[f] = true
	}
	for _, f := range run.SecretFindings {
		known[f.Fingerprint] = true
	}
	var fresh []locatedFinding
	for _, f := range found {
		if !known[f.Fingerprint] {
			known[f.Fingerprint] = true
			fresh = append(fresh, f)
		}
	}
	if len(fresh) == 0 {
		return nil
	}

	if cfg.Stops() {
		return &apperr.WorkflowError{Msg: secretViolation(step, fresh, run.PreRunRef)}
	}
	for _, f := range fresh {
		run.SecretFindings = append(run.SecretFindings, domain.SecretFinding{
			StepIndex: len(run.Executions), File: f.file, Line: f.Line, Rule: f.Rule, Fingerprint: f.Fingerprint,
		})
		if d.OnSecret != nil {
			d.OnSecret(step, f.file, f.Line, f.Rule)
		}
	}
	return nil
}

// secretViolation explains why the run stopped, in the guard's words.
func secretViolation(step domain.ActionType, found []locatedFinding, restoreRef string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s added what looks like a secret; the run stops before anything sends it on:\n", step)
	for i, f := range found {
		if i == maxListedFindings {
			fmt.Fprintf(&b, "  ... and %d more\n", len(found)-maxListedFindings)
			break
		}
		fmt.Fprintf(&b, "  %s:%d (%s)\n", f.file, f.Line, f.Rule)
	}
	b.WriteString("\nThe changes are left in the working tree for inspection.")
	if restoreRef != "" {
		fmt.Fprintf(&b, " Restore point: %s", restoreRef)
	}
	b.WriteString("\nIf a finding is a fixture and not a secret, list its path in security.secret_scan.allow_paths; " +
		"to go on and only record findings, set security.secret_scan.mode: warn.")
	return b.String()
}

// Package persistence stores run state on the local filesystem under
// <baseDir>/<run-id>/{run.json,decision.json,project_context.json,workflow.json,ticket.json,summary.md,report.md,artifacts/,logs/} so a
// completed or failed run can later be inspected. No database is required (MVP.md §21).
package persistence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

type Store struct {
	baseDir string
}

func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

func (s *Store) runDir(runID string) string {
	return filepath.Join(s.baseDir, runID)
}

// RunDir returns the run directory's path (relative to the working directory the
// orchestrator was invoked from), for CLI progress output (the run header).
func (s *Store) RunDir(runID string) string {
	return s.runDir(runID)
}

// LogPath returns the path AppendLog writes stream to for one step, without writing
// anything - used to populate apperr.ProviderError.LogPath so a failure message can point
// at the file AppendLog (separately) persists.
func (s *Store) LogPath(runID string, stepIndex int, actionType domain.ActionType, stream string) string {
	return filepath.Join(s.runDir(runID), "logs", fmt.Sprintf("%03d-%s.%s.log", stepIndex, actionType, stream))
}

// NewRun allocates a fresh run ID, creates its directory structure, writes the initial
// run.json, and returns the pending Run.
func (s *Store) NewRun(task domain.Task) (*domain.Run, error) {
	id, err := newUUIDv4()
	if err != nil {
		return nil, err
	}

	dir := s.runDir(id)
	for _, sub := range []string{"artifacts", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create run directory: %w", err)
		}
	}

	now := time.Now()
	run := &domain.Run{
		ID:         id,
		Task:       task,
		Status:     domain.RunPending,
		Executions: []domain.StepExecution{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.SaveRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

// SaveRun rewrites run.json, plus workflow.json once that field is
// set on run. All writes are write-tmp-then-rename so a crash mid-write never leaves a
// truncated file behind.
func (s *Store) SaveRun(run *domain.Run) error {
	run.UpdatedAt = time.Now()

	if err := s.writeJSON(run.ID, "run.json", run); err != nil {
		return fmt.Errorf("save run: %w", err)
	}
	if run.Workflow != nil {
		if err := s.writeJSON(run.ID, "workflow.json", run.Workflow); err != nil {
			return fmt.Errorf("save workflow: %w", err)
		}
	}
	return nil
}

// SaveDecision writes decision.json: the full decision trace (TASKS.md T11).
func (s *Store) SaveDecision(runID string, trace domain.DecisionTrace) error {
	if err := s.writeJSON(runID, "decision.json", trace); err != nil {
		return fmt.Errorf("save decision: %w", err)
	}
	return nil
}

// SaveProjectContext writes project_context.json: exactly the context sent to the
// decision provider. Nothing is written when none was sent (depth none, or a failed
// collection), so the file's presence alone says context was used.
func (s *Store) SaveProjectContext(runID string, pc *domain.ProjectContext) error {
	if pc == nil {
		return nil
	}
	if err := s.writeJSON(runID, "project_context.json", pc); err != nil {
		return fmt.Errorf("save project context: %w", err)
	}
	return nil
}

// SaveTicket writes ticket.json: the validated Jira issue a task was rendered from
// (specs/jira/SPEC.md §11.2). It is an audit record only - nothing, resume included, ever
// reads it back, so it is not a resume input and is not in action.RunDirPatterns.
func (s *Store) SaveTicket(runID string, t domain.Ticket) error {
	if err := s.writeJSON(runID, "ticket.json", t); err != nil {
		return fmt.Errorf("save ticket: %w", err)
	}
	return nil
}

// SaveSummary writes summary.md: the run's report for a reader (MVP.md §43), rendered from
// run.json and decision.json. Like ticket.json it is never read back, so it is not a resume
// input and is not in action.RunDirPatterns.
func (s *Store) SaveSummary(runID, markdown string) error {
	if !isRunID(runID) {
		return fmt.Errorf("save summary: %q is not a run ID", runID)
	}
	if err := writeFileAtomic(filepath.Join(s.runDir(runID), "summary.md"), []byte(markdown), 0o644); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}
	return nil
}

// ReportPath is where SaveReport writes report.md, beside summary.md.
func (s *Store) ReportPath(runID string) string {
	return filepath.Join(s.runDir(runID), "report.md")
}

// SaveReport writes report.md: the run's report for a reviewer (specs/delivery/SPEC.md §5),
// rendered from run.json and decision.json. Like summary.md it is never read back, so it is
// not a resume input and is not in action.RunDirPatterns.
func (s *Store) SaveReport(runID, markdown string) error {
	if !isRunID(runID) {
		return fmt.Errorf("save report: %q is not a run ID", runID)
	}
	if err := writeFileAtomic(s.ReportPath(runID), []byte(markdown), 0o644); err != nil {
		return fmt.Errorf("save report: %w", err)
	}
	return nil
}

// SaveArtifact writes one action's structured output and returns its path relative to the
// run directory, satisfying action.ArtifactWriter. A non-empty prompt - what the step sent
// to its model - is added as the object's last field, "prompt", for the operator to read;
// ReadArtifact removes it, so nothing ever decides on it.
func (s *Store) SaveArtifact(runID string, stepIndex int, actionType domain.ActionType, artifact any, prompt string) (string, error) {
	relPath := ArtifactRelPath(stepIndex, actionType)
	data, err := marshalArtifact(artifact, prompt)
	if err == nil {
		err = writeFileAtomic(filepath.Join(s.runDir(runID), relPath), data, 0o644)
	}
	if err != nil {
		return "", fmt.Errorf("save artifact: %w", err)
	}
	return relPath, nil
}

// ClarifyArtifactRelPath is where SaveClarifyArtifact writes the artifact of the clarify call
// number n (from 1), relative to the run directory. A call outside the workflow's steps has no
// step index, as with its logs (AppendCallLog).
func ClarifyArtifactRelPath(n int) string {
	return filepath.Join("artifacts", fmt.Sprintf("clarify-%d.json", n))
}

// SaveClarifyArtifact writes artifacts/clarify-<n>.json: the clarify call's parsed answer and what
// it was given, with the prompt as sent as the last field. It is a record for the operator; nothing
// reads it back, so it is not a resume input.
func (s *Store) SaveClarifyArtifact(runID string, n int, artifact any, prompt string) (string, error) {
	if !isRunID(runID) {
		return "", fmt.Errorf("save clarify artifact: %q is not a run ID", runID)
	}
	relPath := ClarifyArtifactRelPath(n)
	data, err := marshalArtifact(artifact, prompt)
	if err == nil {
		err = writeFileAtomic(filepath.Join(s.runDir(runID), relPath), data, 0o644)
	}
	if err != nil {
		return "", fmt.Errorf("save clarify artifact: %w", err)
	}
	return relPath, nil
}

// artifactPromptKey is the field SaveArtifact adds and ReadArtifact removes.
const artifactPromptKey = "prompt"

// marshalArtifact renders artifact as indented JSON with prompt, when there is one, as the
// object's last field. Nothing is HTML-escaped, so a prompt's "<<<TASK-..." delimiters
// read as they were sent.
func marshalArtifact(artifact any, prompt string) ([]byte, error) {
	data, err := marshalUnescaped(artifact)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	if prompt != "" {
		if len(data) < 2 || data[0] != '{' || data[len(data)-1] != '}' {
			return nil, fmt.Errorf("artifact of type %T is not a JSON object", artifact)
		}
		value, err := marshalUnescaped(prompt)
		if err != nil {
			return nil, fmt.Errorf("marshal prompt: %w", err)
		}
		obj := append([]byte{}, data[:len(data)-1]...)
		if len(data) > 2 {
			obj = append(obj, ',')
		}
		obj = append(obj, `"`+artifactPromptKey+`":`...)
		data = append(append(obj, value...), '}')
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return nil, fmt.Errorf("indent: %w", err)
	}
	return out.Bytes(), nil
}

// marshalUnescaped is json.Marshal without HTML escaping and without the trailing newline.
func marshalUnescaped(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// withoutPrompt removes the field withPrompt added. Anything that is not a JSON object
// with that field is returned unchanged, for the caller's strict decoding to judge.
func withoutPrompt(data []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return data
	}
	if _, ok := fields[artifactPromptKey]; !ok {
		return data
	}
	delete(fields, artifactPromptKey)
	out, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	return bytes.TrimSpace(out)
}

// ArtifactRelPath is where SaveArtifact writes a step's artifact, relative to the run
// directory. A resume derives artifact paths from it and never trusts a path read back
// from run.json (specs/resume/SPEC.md §7.3).
func ArtifactRelPath(stepIndex int, actionType domain.ActionType) string {
	return filepath.Join("artifacts", fmt.Sprintf("%03d-%s.json", stepIndex, actionType))
}

// ReadArtifact reads a step's artifact back for a resume, from the path derived from index
// and action, satisfying action.ArtifactReader. The prompt SaveArtifact added is removed. A
// missing artifact is an error matching fs.ErrNotExist.
func (s *Store) ReadArtifact(runID string, stepIndex int, actionType domain.ActionType) ([]byte, error) {
	if !isRunID(runID) {
		return nil, fmt.Errorf("read artifact: %q is not a run ID", runID)
	}
	data, err := os.ReadFile(filepath.Join(s.runDir(runID), ArtifactRelPath(stepIndex, actionType)))
	if err != nil {
		return nil, err
	}
	return withoutPrompt(data), nil
}

// AppendLog appends raw process output (stream is "stdout" or "stderr") to the log file
// for one step, creating it on first write.
func (s *Store) AppendLog(runID string, stepIndex int, actionType domain.ActionType, stream, content string) error {
	relPath := filepath.Join("logs", fmt.Sprintf("%03d-%s.%s.log", stepIndex, actionType, stream))
	full := filepath.Join(s.runDir(runID), relPath)

	f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append log: %w", err)
	}
	return nil
}

// AppendCheckLog appends the raw output of one check (stream is "stdout" or "stderr") of a
// test step to logs/<NNN>-test.<check>.<stream>.log (specs/quality/SPEC.md §5.3). The name
// becomes part of a path, so it is checked against domain.CheckNameRE here and not trusted
// to have been validated.
func (s *Store) AppendCheckLog(runID string, stepIndex int, actionType domain.ActionType, check, stream, content string) error {
	if !domain.CheckNameRE.MatchString(check) {
		return fmt.Errorf("check log: %q is not a check name", check)
	}
	return s.AppendLog(runID, stepIndex, actionType+"."+domain.ActionType(check), stream, content)
}

// CallLogPath returns where AppendCallLog writes stream for the named call, without
// writing anything - used to populate apperr.ProviderError.LogPath.
func (s *Store) CallLogPath(runID, name, stream string) string {
	return filepath.Join(s.runDir(runID), "logs", fmt.Sprintf("%s.%s.log", name, stream))
}

// AppendCallLog appends raw process output (stream is "stdout" or "stderr") of a
// provider call made outside the workflow's steps - translation, a decision round - to
// logs/<name>.<stream>.log, creating it on first write. These names carry no step
// index, so they never collide with AppendLog's logs/<NNN>-<action>.*.log paths.
func (s *Store) AppendCallLog(runID, name, stream, content string) error {
	f, err := os.OpenFile(s.CallLogPath(runID, name, stream), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append log: %w", err)
	}
	return nil
}

func (s *Store) writeJSON(runID, relPath string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return writeFileAtomic(filepath.Join(s.runDir(runID), relPath), data, 0o644)
}

// writeFileAtomic writes data to a temp file in the same directory as path, then renames
// it into place, so readers never observe a partially-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

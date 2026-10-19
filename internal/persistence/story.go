package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/story"
)

// A story's own directory, <home>/.orchestrator/stories/<slug>/ (specs/story/SPEC.md §14):
// story.json, ticket.json, artifacts/ and logs/. story plan creates it as .plan-<id>, and
// renames it to the slug once the spec is written; a plan that does not reach a spec keeps
// the .plan-<id> name, for the operator to read.

// StoryPlanPrefix starts the name of a story directory that has no spec (yet).
const StoryPlanPrefix = ".plan-"

// storyCallRE is the name of one of a story's planning calls: survey-<service> or specify. It
// names the call's artifact and logs, so it is checked wherever it becomes part of a path.
var storyCallRE = regexp.MustCompile(`^(survey-[A-Za-z0-9][A-Za-z0-9._-]{0,63}|specify)$`)

// StoryPlan is the directory of a story being planned.
type StoryPlan struct {
	base string
	dir  string
}

// NewStoryPlan creates base/.plan-<id>/ with artifacts/ and logs/, base being
// <home>/.orchestrator/stories. The id is a random UUID, so two plans never share a
// directory and its name cannot be guessed.
func NewStoryPlan(base string) (*StoryPlan, error) {
	id, err := newUUIDv4()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create the stories directory: %w", err)
	}
	dir := filepath.Join(base, StoryPlanPrefix+id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create the story directory: %w", err)
	}
	for _, sub := range []string{"artifacts", "logs"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create the story directory: %w", err)
		}
	}
	return &StoryPlan{base: base, dir: dir}, nil
}

// Dir is the story directory's current path: .plan-<id> until Rename.
func (p *StoryPlan) Dir() string { return p.dir }

// SaveTicket writes ticket.json: the Jira issue the story was planned from, an audit record.
func (p *StoryPlan) SaveTicket(t domain.Ticket) error {
	if err := p.writeJSON("ticket.json", t); err != nil {
		return fmt.Errorf("save ticket: %w", err)
	}
	return nil
}

// PlanningArtifact is artifacts/<call>.json: what one planning call was sent and what it
// answered. Answer is the validated answer, nil when the call failed or its answer was refused;
// Output is the raw answer either way, and Error why it was refused. Prompt is last, as in a
// run's artifacts.
type PlanningArtifact struct {
	Answer any           `json:"answer"`
	Error  string        `json:"error,omitempty"`
	Usage  *domain.Usage `json:"usage"`
	Output string        `json:"output"`
	Prompt string        `json:"prompt"`
}

// SaveArtifact writes artifacts/<name>.json, name being survey-<service> or specify.
func (p *StoryPlan) SaveArtifact(name string, a PlanningArtifact) error {
	if !storyCallRE.MatchString(name) {
		return fmt.Errorf("save artifact: %q is not a planning call", name)
	}
	data, err := marshalArtifact(a, "")
	if err == nil {
		err = writeFileAtomic(filepath.Join(p.dir, "artifacts", name+".json"), data, 0o644)
	}
	if err != nil {
		return fmt.Errorf("save artifact: %w", err)
	}
	return nil
}

// CallLogPath is where AppendCallLog writes stream for the named call, without writing
// anything (action.PlanningLogger).
func (p *StoryPlan) CallLogPath(name, stream string) string {
	return filepath.Join(p.dir, "logs", name+"."+stream+".log")
}

// AppendCallLog appends a planning call's raw output to logs/<name>.<stream>.log
// (action.PlanningLogger). name and stream are checked: they become part of a path.
func (p *StoryPlan) AppendCallLog(name, stream, content string) error {
	if !storyCallRE.MatchString(name) || (stream != "stdout" && stream != "stderr") {
		return fmt.Errorf("call log: %q, %q is not a planning call's stream", name, stream)
	}
	f, err := os.OpenFile(p.CallLogPath(name, stream), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append log: %w", err)
	}
	return nil
}

// SlugTaken reports whether a story directory named slug exists already, so the caller can
// refuse before it writes a spec that Rename would then not be able to own.
func (p *StoryPlan) SlugTaken(slug string) bool {
	_, err := os.Lstat(filepath.Join(p.base, slug))
	return err == nil
}

// Rename gives the story directory its slug. An existing directory of that name is refused,
// never replaced.
func (p *StoryPlan) Rename(slug string) error {
	if !story.ValidSlug(slug) {
		return fmt.Errorf("rename the story directory: %q is not a slug", slug)
	}
	target := filepath.Join(p.base, slug)
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("rename the story directory: %s exists already", target)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rename the story directory: %w", err)
	}
	if err := os.Rename(p.dir, target); err != nil {
		return fmt.Errorf("rename the story directory: %w", err)
	}
	p.dir = target
	return nil
}

// StoryRecord is story.json as story plan writes it: the part of SPEC §14's record that is
// known before the story first runs. story run adds the rest; LoadStoryRecord refuses any
// key it does not know, so a new field is added here, with its check, on purpose.
type StoryRecord struct {
	Slug string `json:"slug"`
	// SpecDir is the spec's directory relative to the story home, slash-separated
	// (specs/refunds).
	SpecDir string `json:"spec_dir"`
	// JiraParent is the issue the story was planned from; "" without --jira.
	JiraParent string    `json:"jira_parent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// Services are the story's services by name, each the absolute path of its checkout.
	Services map[string]string `json:"services"`
	Status   story.Status      `json:"status"`
	// Calls are the planning calls' usage by call (survey-<service>, specify); nil when
	// the CLI reported none.
	Calls map[string]*domain.Usage `json:"calls,omitempty"`
}

// Validate checks every field, as LoadStoryRecord does on what it reads.
func (r StoryRecord) Validate() error {
	if !story.ValidSlug(r.Slug) {
		return fmt.Errorf("slug %q is not a slug", r.Slug)
	}
	if !validSpecDir(r.SpecDir, r.Slug) {
		return fmt.Errorf("spec_dir %q is not a clean relative directory named %s", r.SpecDir, r.Slug)
	}
	if r.JiraParent != "" && !domain.ValidTicketKey(r.JiraParent) {
		return fmt.Errorf("jira_parent %q is not an issue key", r.JiraParent)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("created_at is missing")
	}
	if len(r.Services) == 0 {
		return fmt.Errorf("services is empty")
	}
	for name, dir := range r.Services {
		if !story.ValidRepo(name) {
			return fmt.Errorf("services: %q is not a service name", name)
		}
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
			return fmt.Errorf("services.%s: %q is not a clean absolute path", name, dir)
		}
	}
	switch r.Status {
	case story.StatusPlanned, story.StatusRunning, story.StatusCompleted, story.StatusBlocked, story.StatusFailed, story.StatusStopped:
	default:
		return fmt.Errorf("status %q is not a story status", r.Status)
	}
	for name, u := range r.Calls {
		if !storyCallRE.MatchString(name) {
			return fmt.Errorf("calls: %q is not a planning call", name)
		}
		if u == nil {
			continue
		}
		if err := u.Validate(); err != nil {
			return fmt.Errorf("calls.%s: %w", name, err)
		}
	}
	return nil
}

// validSpecDir reports whether dir is a clean, relative, slash-separated path inside the story
// home whose last element is slug.
func validSpecDir(dir, slug string) bool {
	switch {
	case dir == "" || path.IsAbs(dir) || strings.Contains(dir, `\`) || path.Clean(dir) != dir:
		return false
	case dir == ".." || strings.HasPrefix(dir, "../"):
		return false
	}
	return path.Base(dir) == slug
}

// SaveRecord writes story.json, after checking it as LoadStoryRecord would.
func (p *StoryPlan) SaveRecord(r StoryRecord) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("save story.json: %w", err)
	}
	if err := p.writeJSON("story.json", r); err != nil {
		return fmt.Errorf("save story.json: %w", err)
	}
	return nil
}

// LoadStoryRecord reads story.json strictly: one JSON object with no unknown key and nothing
// after it, every field checked by StoryRecord.Validate.
func LoadStoryRecord(file string) (StoryRecord, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return StoryRecord{}, err
	}
	var r StoryRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return StoryRecord{}, fmt.Errorf("%s: %w", file, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return StoryRecord{}, fmt.Errorf("%s: data after the JSON object", file)
	}
	if err := r.Validate(); err != nil {
		return StoryRecord{}, fmt.Errorf("%s: %w", file, err)
	}
	return r, nil
}

func (p *StoryPlan) writeJSON(name string, v any) error {
	data, err := marshalArtifact(v, "")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(p.dir, name), data, 0o644)
}

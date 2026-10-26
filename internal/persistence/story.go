package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"orchestrator/internal/apperr"
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

// StoryRecord is story.json (SPEC §14). story plan writes the part that is known before the
// story first runs; story run adds approved_at, bases and the state of every task. All the
// later fields are optional, so a plan-time record loads. LoadStoryRecord refuses any key it
// does not know, so a new field is added here, with its check, on purpose.
type StoryRecord struct {
	Slug string `json:"slug"`
	// SpecDir is the spec's directory relative to the story home, slash-separated
	// (specs/refunds).
	SpecDir string `json:"spec_dir"`
	// JiraParent is the issue the story was planned from; "" without --jira.
	JiraParent string    `json:"jira_parent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// ApprovedAt is when the first story run was approved (the plan printed, one yes or
	// --mode auto); nil before.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	// Services are the story's services by name, each the absolute path of its checkout.
	Services map[string]string `json:"services"`
	// Bases are the services' story bases, recorded at the first start: the branch checked out
	// in the service and its commit (SPEC §9.2).
	Bases  map[string]StoryBase `json:"bases,omitempty"`
	Status story.Status         `json:"status"`
	// Tasks is the state of every task that has one, by task ID. A task without an entry is
	// pending and was never dispatched.
	Tasks map[string]StoryTask `json:"tasks,omitempty"`
	// Calls are the planning calls' usage by call (survey-<service>, specify); nil when
	// the CLI reported none.
	Calls map[string]*domain.Usage `json:"calls,omitempty"`
}

// StoryBase is a service's story base: the branch that was checked out when the story first
// started and the commit it pointed at.
type StoryBase struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
	// SpecBranch is the story's own branch in the service, story/<slug>/spec, created at SHA
	// by the first story run (a record from before it has none, and gets it when it continues).
	// The tasks with several parents are merged on it.
	SpecBranch string `json:"spec_branch,omitempty"`
}

// StorySpecBranch is the story's own branch, where the parents of a task are merged:
// story/<slug>/spec. It cannot clash with a task's branch, spec not being a task ID.
func StorySpecBranch(slug string) string {
	return "story/" + slug + "/spec"
}

// MaxConflictFiles is how many conflicting paths a conflict task records.
const MaxConflictFiles = 50

// StoryConflict is why a task is in the conflict status: its parents (task IDs) do not merge
// cleanly on the spec branch, and Files are the paths that conflict.
type StoryConflict struct {
	Parents []string `json:"parents"`
	Files   []string `json:"files"`
}

// validConflictFile reports whether p is a clean, relative, slash-separated repository path.
func validConflictFile(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00\n\r") || path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	return p != ".." && !strings.HasPrefix(p, "../")
}

// ConflictFiles keeps the paths of files a conflict task can record: clean relative ones, at most
// MaxConflictFiles of them.
func ConflictFiles(files []string) []string {
	kept := []string{}
	for _, f := range files {
		if len(kept) == MaxConflictFiles {
			break
		}
		if validConflictFile(f) {
			kept = append(kept, f)
		}
	}
	return kept
}

// StoryTask is what story run knows about one task. Everything but Status is empty while the
// task has never been dispatched.
type StoryTask struct {
	Status story.TaskStatus `json:"status"`
	// FileSHA256 is story.FrozenHash of the task file when it was dispatched: from then on an
	// edit above "## As built" is refused (SPEC A6).
	FileSHA256 string `json:"file_sha256,omitempty"`
	// Attempt is 1 for the first run of the task and n for the retry on <branch>.<n>.
	Attempt int `json:"attempt,omitempty"`
	// RunID is the child run of this attempt in the task's repository; empty until a run was
	// found for the dispatch.
	RunID string `json:"run_id,omitempty"`
	Repo  string `json:"repo,omitempty"`
	// Branch and BaseRef are what the child was asked to run on and from; Commit is the commit
	// the task ended with, and what a dependent in the same repository is stacked on.
	Branch  string `json:"branch,omitempty"`
	BaseRef string `json:"base_ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// Jira is the task's issue key; JiraIntent marks a child issue whose creation may have
	// reached Jira without its key being recorded (SPEC §12.2).
	Jira       string `json:"jira,omitempty"`
	JiraIntent bool   `json:"jira_intent,omitempty"`
	// CostUSD is what the current attempt's run reported (a resumed run's outcome already holds
	// the cost of the attempts before it, so it replaces the value); AbandonedCostUSD is the
	// cost of the runs that --retry left behind.
	CostUSD          float64 `json:"cost_usd,omitempty"`
	AbandonedCostUSD float64 `json:"abandoned_cost_usd,omitempty"`
	// Conflict is present only in the conflict status: the task was not dispatched because its
	// parents do not merge.
	Conflict *StoryConflict `json:"conflict,omitempty"`
}

// StoryTaskBranch is the branch of attempt n of task id of story slug: story/<slug>/<ID> for
// the first, story/<slug>/<ID>.<n> for the retry (SPEC §9.3).
func StoryTaskBranch(slug, id string, attempt int) string {
	if attempt <= 1 {
		return "story/" + slug + "/" + id
	}
	return fmt.Sprintf("story/%s/%s.%d", slug, id, attempt)
}

// TotalCostUSD is the cost recorded so far: the planning calls and every task's runs. A call
// without usage, or a partial one, counts what it reported; the budget check is made against
// this number.
func (r StoryRecord) TotalCostUSD() float64 {
	total := 0.0
	for _, u := range r.Calls {
		if u != nil {
			total += u.CostUSD
		}
	}
	for _, t := range r.Tasks {
		total += t.CostUSD + t.AbandonedCostUSD
	}
	return total
}

// validStoryTaskStatus reports whether s is one of the task statuses.
func validStoryTaskStatus(s story.TaskStatus) bool {
	switch s {
	case story.TaskPending, story.TaskRunning, story.TaskDone, story.TaskFailed, story.TaskBlocked, story.TaskStopped, story.TaskConflict:
		return true
	}
	return false
}

func orPendingStatus(s story.TaskStatus) story.TaskStatus {
	if s == "" {
		return story.TaskPending
	}
	return s
}

// validate checks the conflict of a task in the conflict status, repo being the task's repository.
func (c *StoryConflict) validate(repo string) error {
	switch {
	case c == nil:
		return fmt.Errorf("a conflict task has no conflict")
	case repo == "":
		return fmt.Errorf("a conflict task has no repo")
	case len(c.Parents) == 0:
		return fmt.Errorf("conflict.parents is empty")
	case len(c.Files) > MaxConflictFiles:
		return fmt.Errorf("conflict.files has %d paths, at most %d are recorded", len(c.Files), MaxConflictFiles)
	}
	seen := map[string]bool{}
	for _, p := range c.Parents {
		if !story.ValidID(p) || seen[p] {
			return fmt.Errorf("conflict.parents: %q is not a task ID, or is listed twice", p)
		}
		seen[p] = true
	}
	for _, f := range c.Files {
		if !validConflictFile(f) {
			return fmt.Errorf("conflict.files: %q is not a clean repository-relative path", f)
		}
	}
	return nil
}

// validate checks one task's state; slug is the story's and services its services.
func (t StoryTask) validate(slug, id string, services map[string]string) error {
	if !validStoryTaskStatus(t.Status) {
		return fmt.Errorf("status %q is not a task status", t.Status)
	}
	if t.FileSHA256 != "" && !isSHA256Hex(t.FileSHA256) {
		return fmt.Errorf("file_sha256 %q is not a sha256", t.FileSHA256)
	}
	if t.Attempt < 0 || t.Attempt > 999999999 {
		return fmt.Errorf("attempt is %d", t.Attempt)
	}
	if t.RunID != "" && !isRunID(t.RunID) {
		return fmt.Errorf("run_id %q is not a run ID", t.RunID)
	}
	if t.Repo != "" {
		if _, ok := services[t.Repo]; !ok || !story.ValidRepo(t.Repo) {
			return fmt.Errorf("repo %q is not a service of the story", t.Repo)
		}
	}
	if t.Branch != "" {
		if t.Attempt < 1 || t.Branch != StoryTaskBranch(slug, id, t.Attempt) || !story.ValidBranch(t.Branch, slug, id) {
			return fmt.Errorf("branch %q is not %s", t.Branch, StoryTaskBranch(slug, id, max(t.Attempt, 1)))
		}
	}
	if t.BaseRef != "" && !story.ValidBaseRef(t.BaseRef) {
		return fmt.Errorf("base_ref %q is not a full commit SHA", t.BaseRef)
	}
	if t.Commit != "" && !story.ValidBaseRef(t.Commit) {
		return fmt.Errorf("commit %q is not a full commit SHA", t.Commit)
	}
	if t.Jira != "" && !story.ValidJiraKey(t.Jira) {
		return fmt.Errorf("jira %q is not a Jira key", t.Jira)
	}
	for name, cost := range map[string]float64{"cost_usd": t.CostUSD, "abandoned_cost_usd": t.AbandonedCostUSD} {
		if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
			return fmt.Errorf("%s is %v", name, cost)
		}
	}
	if t.Status == story.TaskConflict {
		if err := t.Conflict.validate(t.Repo); err != nil {
			return err
		}
	} else if t.Conflict != nil {
		return fmt.Errorf("a %s task has a conflict", orPendingStatus(t.Status))
	}
	if t.Status != story.TaskPending && t.Status != story.TaskConflict {
		switch {
		case t.Attempt < 1:
			return fmt.Errorf("a %s task has no attempt", t.Status)
		case t.FileSHA256 == "" || t.Repo == "" || t.Branch == "" || t.BaseRef == "":
			return fmt.Errorf("a %s task is missing file_sha256, repo, branch or base_ref", t.Status)
		}
	}
	if t.Status == story.TaskDone && t.Commit == "" {
		return fmt.Errorf("a done task has no commit")
	}
	return nil
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
	for name, b := range r.Bases {
		if _, ok := r.Services[name]; !ok || !story.ValidRepo(name) {
			return fmt.Errorf("bases: %q is not a service of the story", name)
		}
		if !story.ValidBaseBranch(b.Branch) {
			return fmt.Errorf("bases.%s: branch %q is not a branch name", name, b.Branch)
		}
		if !story.ValidBaseRef(b.SHA) {
			return fmt.Errorf("bases.%s: sha %q is not a full commit SHA", name, b.SHA)
		}
		if b.SpecBranch != "" && b.SpecBranch != StorySpecBranch(r.Slug) {
			return fmt.Errorf("bases.%s: spec_branch %q is not %s", name, b.SpecBranch, StorySpecBranch(r.Slug))
		}
	}
	dispatched := false
	for id, t := range r.Tasks {
		if !story.ValidID(id) {
			return fmt.Errorf("tasks: %q is not a task ID", id)
		}
		if err := t.validate(r.Slug, id, r.Services); err != nil {
			return fmt.Errorf("tasks.%s: %w", id, err)
		}
		if t.Status != story.TaskPending {
			dispatched = true
		}
	}
	if dispatched && r.ApprovedAt == nil {
		return fmt.Errorf("a task was dispatched, but approved_at is missing")
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
	return SaveStoryRecord(p.dir, r)
}

// StoryRecordFile is the name of a story's record in its directory.
const StoryRecordFile = "story.json"

// StoryDir is the directory of story slug: <home>/.orchestrator/stories/<slug>. The path is
// derived, never read; the slug is checked because it becomes part of it.
func StoryDir(home, slug string) (string, error) {
	if !story.ValidSlug(slug) {
		return "", fmt.Errorf("%q is not a story slug (lowercase letters, digits and dashes)", slug)
	}
	return filepath.Join(home, ".orchestrator", "stories", slug), nil
}

// SaveStoryRecord writes dir/story.json atomically, after checking it as LoadStoryRecord would.
func SaveStoryRecord(dir string, r StoryRecord) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("save story.json: %w", err)
	}
	data, err := marshalArtifact(r, "")
	if err == nil {
		err = writeFileAtomic(filepath.Join(dir, StoryRecordFile), data, 0o644)
	}
	if err != nil {
		return fmt.Errorf("save story.json: %w", err)
	}
	return nil
}

// WriteStoryTaskInput writes the child's task input to dir/tasks/<ID>.json atomically and
// returns its path. The input is checked as the child will check it, so a rendering that the
// child would refuse never reaches a process.
func WriteStoryTaskInput(dir string, in story.TaskInput) (string, error) {
	data, err := marshalArtifact(in, "")
	if err != nil {
		return "", fmt.Errorf("task input: %w", err)
	}
	if _, err := story.ParseTaskInput(data); err != nil {
		return "", err
	}
	tasks := filepath.Join(dir, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", tasks, err)
	}
	file := filepath.Join(tasks, in.Task+".json")
	if err := writeFileAtomic(file, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", file, err)
	}
	return file, nil
}

// ReplaceFile writes data over path atomically, keeping the permissions of the file it
// replaces (0644 for a new one): a spec file is the operator's, and a reader never sees it half
// written.
func ReplaceFile(path string, data []byte) error {
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	return writeFileAtomic(path, data, perm)
}

// LockStory takes an exclusive, non-blocking flock on the story directory itself, creating it
// when it does not exist (a hand-written spec's first start), so only one story run works on a
// story at a time (SPEC §14). As Store.Lock, the kernel releases it when the process dies and
// there is no lock file to delete. The returned func releases the lock.
func LockStory(dir string) (unlock func(), err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create the story directory: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open the story directory: %w", err)
	}
	fd := int(d.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		d.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &apperr.ConfigError{Msg: fmt.Sprintf("story %s is being run by another orchestrator process (%s is locked)", filepath.Base(dir), dir)}
		}
		return nil, fmt.Errorf("lock the story directory: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(fd, syscall.LOCK_UN)
			d.Close()
		})
	}, nil
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

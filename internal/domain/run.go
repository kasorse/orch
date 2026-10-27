package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunStopped   RunStatus = "stopped"
)

// Valid reports whether s is one of the defined run statuses.
func (s RunStatus) Valid() bool {
	switch s {
	case RunPending, RunRunning, RunCompleted, RunFailed, RunStopped:
		return true
	}
	return false
}

// ProviderRef identifies the provider call a step made: which CLI, which model, and the
// authority it ran with (specs/permissions/SPEC.md §5), so run.json answers "what was this
// step allowed to do" after the fact.
type ProviderRef struct {
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Effort    string    `json:"effort,omitempty"`
	Authority Authority `json:"authority,omitempty"`
	// ModelSource says where the model and effort came from: ModelSourceBase,
	// ModelSourceByComplexity + the decided complexity (specs/quality/SPEC.md §9.3), or
	// ModelSourceEscalated + the level above it for an implement that fixes a failed test or
	// review (specs/decision-shape/SPEC.md §8), or ModelSourceByComplexity + the decided
	// complexity + ModelSourceEscalateSuffix when that level's entry names its own escalation
	// (§8.1). Empty in a run written before the field.
	ModelSource string `json:"model_source,omitempty"`
}

// Values of ProviderRef.ModelSource.
const (
	ModelSourceBase         = "base"
	ModelSourceByComplexity = "by_complexity."
	ModelSourceEscalated    = "escalated."
	// ModelSourceEscalateSuffix ends the source of a fix routed by its level's own escalate.
	ModelSourceEscalateSuffix = ".escalate"
)

// ValidModelSource reports whether s is a model source a run records: base, by_complexity
// with one of the complexities (with or without ModelSourceEscalateSuffix), or escalated to one
// above simple.
func ValidModelSource(s string) bool {
	if s == ModelSourceBase {
		return true
	}
	if level, ok := strings.CutPrefix(s, ModelSourceByComplexity); ok {
		_, err := ParseComplexity(strings.TrimSuffix(level, ModelSourceEscalateSuffix))
		return err == nil
	}
	if level, ok := strings.CutPrefix(s, ModelSourceEscalated); ok {
		c, err := ParseComplexity(level)
		return err == nil && c != ComplexitySimple
	}
	return false
}

type StepExecStatus string

const (
	StepPending   StepExecStatus = "pending"
	StepRunning   StepExecStatus = "running"
	StepCompleted StepExecStatus = "completed"
	StepFailed    StepExecStatus = "failed"
)

// Valid reports whether s is one of the defined step statuses.
func (s StepExecStatus) Valid() bool {
	switch s {
	case StepPending, StepRunning, StepCompleted, StepFailed:
		return true
	}
	return false
}

// StepExecution is one recorded attempt at an action. Run.Executions is append-only -
// retries produce new entries rather than overwriting previous ones.
type StepExecution struct {
	Index        int            `json:"index"`
	Action       ActionType     `json:"action"`
	Provider     *ProviderRef   `json:"provider,omitempty"`
	Status       StepExecStatus `json:"status"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
	ArtifactPath string         `json:"artifact_path,omitempty"`
	TestResult   *TestResult    `json:"test_result,omitempty"`
	ReviewResult *ReviewResult  `json:"review_result,omitempty"`
	// Usage is what the step's provider call cost, as the CLI reported it; nil for a step
	// without a call (test) or a CLI that reports nothing. A call that failed after the
	// CLI reported usage still records it.
	Usage *Usage `json:"usage,omitempty"`
	// Call is the step's provider call beyond its cost: prompt size and cuts, the models
	// and tools it used. Nil for a step without a call (test).
	Call *CallReport `json:"call,omitempty"`
	// Diff is what an implement step changed, from git. Nil for every other step, and for
	// an implement whose call failed or whose changes the guard refused: git runs on the
	// working tree only after the guard has passed it.
	Diff *StepDiff `json:"diff,omitempty"`
	// TreeAfter is the git tree id of the working tree after a completed implement, which the
	// no-progress check compares between implements (specs/quality/SPEC.md §8.1). Empty for
	// every other step, when git could not compute it, and in a run written before the field
	// existed.
	TreeAfter string `json:"tree_after,omitempty"`
	// VerifyCalls are the calls the implement session made to the verify tools, in order, also
	// when the step failed (specs/quality/SPEC.md §12). Empty for every other step.
	VerifyCalls []VerifyCall `json:"verify_calls,omitempty"`
	// PathRules are the policy.paths rules the run's net diff matched after this implement,
	// also when the step failed because of them (specs/governance/SPEC.md §4.3). Empty for
	// every other step.
	PathRules []PathRuleMatch `json:"path_rules,omitempty"`
	Error     string          `json:"error,omitempty"`
	// Attempt is 0 for a step the original process ran and n for one run by the n-th
	// resume (specs/resume/SPEC.md §12).
	Attempt int `json:"attempt,omitempty"`
}

// Resume records one `orchestrator resume` of a run: what state it was found in and
// where it continued. Run.Resumes is append-only, like Run.Executions.
type Resume struct {
	At time.Time `json:"at"`
	// FromStatus is the status the run was found in; "running" means the previous process
	// died mid-run (interrupted).
	FromStatus RunStatus  `json:"from_status"`
	FromReason string     `json:"from_reason,omitempty"`
	Entry      ActionType `json:"entry"`
	Why        string     `json:"why"`
	Mode       RunMode    `json:"mode"`
	// ProviderCLI is what the fail-fast version check saw in this process: the CLI may
	// have been updated since the run started (specs/hardening/SPEC.md §7.3).
	ProviderCLI map[string]ProviderCLI `json:"provider_cli,omitempty"`
	// OrgConfig is the org file this resume ran under; nil when there was none
	// (specs/governance/SPEC.md §6.3).
	OrgConfig *OrgConfigRef `json:"org_config,omitempty"`
	// ClaudeAuth is the credential mode this resume ran under; a record, never an input
	// (specs/claude-auth/SPEC.md §11). Nil in a run written before the field.
	ClaudeAuth *ClaudeAuth `json:"claude_auth,omitempty"`
	// CodexAuth is the credential mode this resume ran under; a record, never an input.
	CodexAuth *CodexAuth `json:"codex_auth,omitempty"`
}

// OrgConfigRef names the org config file a run or a resume ran under and the sha256 of its
// bytes, which is the evidence of which policy applied (specs/governance/SPEC.md §6.3).
type OrgConfigRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Validate checks a recorded reference: an absolute, clean path and a 64-digit lowercase hex
// sha256.
func (o OrgConfigRef) Validate() error {
	if !filepath.IsAbs(o.Path) || filepath.Clean(o.Path) != o.Path {
		return fmt.Errorf("path %q is not an absolute, clean path", o.Path)
	}
	if len(o.SHA256) != 64 {
		return fmt.Errorf("sha256 %q is not 64 hex digits", o.SHA256)
	}
	for _, c := range o.SHA256 {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("sha256 %q is not lowercase hex", o.SHA256)
		}
	}
	return nil
}

// ProviderCLI is one provider CLI as the version check found it: the version string the
// CLI reported and whether it lies in the range its permission flags were verified on.
type ProviderCLI struct {
	Version  string `json:"version"`
	Verified bool   `json:"verified"`
}

type Run struct {
	ID   string `json:"id"`
	Task Task   `json:"task"`
	// Decision is the policy's verdict the workflow was built from; decision.json holds
	// the full trace behind it (TASKS.md T11).
	Decision *Decision `json:"decision,omitempty"`
	Workflow *Workflow `json:"workflow,omitempty"`
	// Mode is the execution mode this run actually ran under (MVP.md §1/§27).
	Mode RunMode `json:"mode,omitempty"`
	// PreRunRef is the restore point recorded before the first step: HEAD on a clean
	// tree, or a snapshot commit of the uncommitted changes (untracked files included)
	// the run started from (specs/permissions/SPEC.md §7.2).
	PreRunRef string `json:"pre_run_ref,omitempty"`
	// HeadRef is HEAD when the first step started. A resume refuses when HEAD has moved,
	// since review diffs against HEAD (specs/resume/SPEC.md §9.2).
	HeadRef string `json:"head_ref,omitempty"`
	// ResumeBaseline is the repository guard's state when the first step started: the
	// git fingerprint and the protected paths. A resume compares against it before
	// anything runs (specs/resume/SPEC.md §9.1). Its shape belongs to the guard, not to
	// domain.
	ResumeBaseline json.RawMessage `json:"resume_baseline,omitempty"`
	// ProviderCLI is the CLI version of each routed provider at run start.
	ProviderCLI map[string]ProviderCLI `json:"provider_cli,omitempty"`
	// OrgConfig is the org file the run started under; nil when there was none
	// (specs/governance/SPEC.md §6.3).
	OrgConfig *OrgConfigRef `json:"org_config,omitempty"`
	// ClaudeAuth is the credential mode the run started under (specs/claude-auth/SPEC.md §10);
	// a record, never read back to decide anything. Nil in a run written before the field.
	ClaudeAuth *ClaudeAuth `json:"claude_auth,omitempty"`
	// CodexAuth is the credential mode the run started under; a record, never read back to decide anything.
	CodexAuth *CodexAuth `json:"codex_auth,omitempty"`
	// Resumes is append-only: one entry per `orchestrator resume` of this run.
	Resumes []Resume `json:"resumes,omitempty"`
	// AllowRedTests is run --allow-red-tests: a red pre-run test does not stop the run, its
	// output goes to the first implement instead (specs/hardening/SPEC.md §8.4).
	AllowRedTests bool `json:"allow_red_tests,omitempty"`
	// Options are the config rules the run follows, recorded once when it is created and never
	// changed by a resume (specs/quality/SPEC.md §7.3). Omitted while they are all off.
	Options RunOptions `json:"options,omitzero"`
	// PreRunTest is the test command's result before the first step, when the workflow
	// implements and tests (specs/hardening/SPEC.md §8). It is not an execution.
	// SecretScanBaseline are the fingerprints of what looked like secrets in the working
	// tree when the run started; only findings not among them count (specs/hardening/SPEC.md
	// §9.4).
	SecretScanBaseline []string `json:"secret_scan_baseline,omitempty"`
	// SecretFindings are the new findings a run with security.secret_scan.mode: warn went on
	// with. Never the value.
	SecretFindings []SecretFinding `json:"secret_findings,omitempty"`
	PreRunTest     *TestResult     `json:"pre_run_test,omitempty"`
	Executions     []StepExecution `json:"executions"`
	Status         RunStatus       `json:"status"`
	FailureReason  string          `json:"failure_reason,omitempty"`
	TotalSteps     int             `json:"total_steps"`
	FixIterations  int             `json:"fix_iterations"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	// Outcome is how the run ended and what it cost, written when it reaches a terminal
	// status and recomputed over the whole history by a resume (ComputeOutcome).
	Outcome *Outcome `json:"outcome,omitempty"`
	// Commit is the commit the operator approved at the end of a completed run
	// (MVP.md §42); empty when nothing was committed.
	Commit string `json:"commit,omitempty"`
	// Worktree is the git worktree the run executes in (specs/worktree/SPEC.md §8.2); nil for an
	// inplace run and in a run written before the field.
	Worktree *RunWorktree `json:"worktree,omitempty"`
	// MR is where the end of a worktree run got to after its commit: the push of its branch and
	// the merge request that push opened (specs/delivery/SPEC.md §7, §13). Nil until the chain
	// ran; a record, never read back to decide anything.
	MR *RunMR `json:"mr,omitempty"`
	// BranchNaming is how the worktree's branch was named (specs/delivery/SPEC.md §4.3); nil for an
	// inplace run, a story task and a run written before the field.
	BranchNaming *BranchNaming `json:"branch_naming,omitempty"`
	// ReviewTarget is the change an `orchestrator review` run reviews (specs/delivery/SPEC.md
	// §12.2); nil for every other run. Such a run executes in a detached worktree at its head,
	// which has no branch.
	ReviewTarget *ReviewTarget `json:"review_target,omitempty"`
	// JiraWrite is what the run wrote to its task's Jira issue after MR was created
	// (specs/delivery/SPEC.md §10, §13). Nil unless options.jira_write asked for something and
	// the task has a key; a record, never read back to decide anything.
	JiraWrite *RunJiraWrite `json:"jira_write,omitempty"`
}

// maxReviewRefName bounds the ref names a review run records, as typed on the command line.
const maxReviewRefName = 255

// ReviewTarget is what a review run reviews: merge_base..head, with the refs as the operator
// named them. Base, Head and MergeBase are full commit SHAs.
type ReviewTarget struct {
	Base        string `json:"base"`
	Head        string `json:"head"`
	MergeBase   string `json:"merge_base"`
	BaseRefName string `json:"base_ref_name"`
	HeadRefName string `json:"head_ref_name"`
}

// Validate checks what a run.json records as its review target: three full lowercase hex
// commit names and two printable ref names of at most 255 bytes.
func (t ReviewTarget) Validate() error {
	for _, c := range []struct{ name, sha string }{{"base", t.Base}, {"head", t.Head}, {"merge_base", t.MergeBase}} {
		if (len(c.sha) != 40 && len(c.sha) != 64) || strings.Trim(c.sha, "0123456789abcdef") != "" {
			return fmt.Errorf("%s %q is not a full commit SHA", c.name, c.sha)
		}
	}
	for _, c := range []struct{ name, ref string }{{"base_ref_name", t.BaseRefName}, {"head_ref_name", t.HeadRefName}} {
		if err := ValidReviewRefName(c.ref); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	return nil
}

// ValidReviewRefName checks a ref name as typed after --base or --head: not empty, printable, at
// most 255 bytes.
func ValidReviewRefName(ref string) error {
	switch {
	case ref == "":
		return fmt.Errorf("is empty")
	case len(ref) > maxReviewRefName:
		return fmt.Errorf("is %d bytes, at most %d", len(ref), maxReviewRefName)
	case strings.ContainsFunc(ref, func(r rune) bool { return !unicode.IsPrint(r) }):
		return fmt.Errorf("has a non-printable character")
	}
	return nil
}

// Values of RunJiraComment.State.
const (
	// JiraCommentIntent is saved before the comment is posted: the request may have reached Jira
	// even when it reports a failure, so it is never repeated.
	JiraCommentIntent = "intent"
	// JiraCommentDone is a comment Jira answered with its id.
	JiraCommentDone = "done"
	// JiraCommentFailed is a comment Jira refused: nothing was posted.
	JiraCommentFailed = "failed"
)

// Values of RunJiraTransition.State.
const (
	JiraTransitionDone = "done"
	// JiraTransitionMissing is a configured transition the issue has none like (the warning
	// lists the names it has).
	JiraTransitionMissing = "missing"
	// JiraTransitionFailed is a transition that could not be found out or made.
	JiraTransitionFailed = "failed"
	// JiraTransitionUnchanged is an issue that is already in the configured status.
	JiraTransitionUnchanged = "unchanged"
)

// maxJiraWriteName bounds a recorded transition name, in bytes.
const maxJiraWriteName = 255

// JiraWriteOptions is jira.write as the run copied it when it started (specs/delivery/SPEC.md
// §10); a resume uses this copy and never the current config.
type JiraWriteOptions struct {
	Comment    bool   `json:"comment,omitempty"`
	Transition string `json:"transition,omitempty"`
}

// Validate checks the options read back from run.json: a transition is one line of at most
// maxJiraWriteName bytes.
func (o JiraWriteOptions) Validate() error {
	return validJiraName("transition", o.Transition)
}

func validJiraName(what, v string) error {
	if len(v) > maxJiraWriteName || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return fmt.Errorf("%s must be one line of at most %d bytes", what, maxJiraWriteName)
	}
	return nil
}

// RunJiraWrite is the record of the write-back to a run's Jira issue. Either part is nil when it
// was not asked for.
type RunJiraWrite struct {
	Comment    *RunJiraComment    `json:"comment,omitempty"`
	Transition *RunJiraTransition `json:"transition,omitempty"`
}

// RunJiraComment is the record of the comment: State is JiraCommentIntent until Jira answered,
// and ID, the id Jira gave it, goes with JiraCommentDone only.
type RunJiraComment struct {
	State string `json:"state"`
	ID    string `json:"id,omitempty"`
}

// RunJiraTransition is the record of the transition: Name is the configured name, State what came
// of it.
type RunJiraTransition struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// Validate applies the rules of specs/delivery/SPEC.md §13 to a record read back from run.json,
// against the options the run started with.
func (j RunJiraWrite) Validate(opts JiraWriteOptions) error {
	if j.Comment == nil && j.Transition == nil {
		return fmt.Errorf("a record needs a comment or a transition")
	}
	if c := j.Comment; c != nil {
		switch {
		case !opts.Comment:
			return fmt.Errorf("comment recorded, but options.jira_write.comment is off")
		case c.State != JiraCommentIntent && c.State != JiraCommentDone && c.State != JiraCommentFailed:
			return fmt.Errorf("comment.state %q is not intent, done or failed", c.State)
		case (c.State == JiraCommentDone) != (c.ID != ""):
			return fmt.Errorf("comment.id goes with state done only")
		case len(c.ID) > 20 || strings.Trim(c.ID, "0123456789") != "":
			return fmt.Errorf("comment.id %q is not a number", c.ID)
		}
	}
	if t := j.Transition; t != nil {
		switch {
		case opts.Transition == "":
			return fmt.Errorf("transition recorded, but options.jira_write.transition is empty")
		case t.Name != opts.Transition:
			return fmt.Errorf("transition.name is %q, but options.jira_write.transition is %q", t.Name, opts.Transition)
		}
		switch t.State {
		case JiraTransitionDone, JiraTransitionMissing, JiraTransitionFailed, JiraTransitionUnchanged:
		default:
			return fmt.Errorf("transition.state %q is not done, missing, failed or unchanged", t.State)
		}
	}
	return nil
}

// Values of RunMR.State.
const (
	// MRIntent is saved before the push: the push may have reached the remote even when it
	// reports a failure, so it is never repeated.
	MRIntent = "intent"
	// MRPushed is a push that succeeded without a merge request: none wanted, or the remote
	// reported none.
	MRPushed = "pushed"
	// MRCreated is a push whose answer named the merge request it opened.
	MRCreated = "created"
)

// Bounds of RunMR's text, in bytes.
const (
	maxMRText = 500
	maxMRURL  = 2048
)

// RunMR is the record of a worktree run's push and merge request. State is empty when the chain
// stopped before the push: Skipped says why (nobody committed, or somebody said no), or Error
// says what the checks before the push found.
type RunMR struct {
	State  string `json:"state,omitempty"`
	Remote string `json:"remote,omitempty"`
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
	// IID and URL are set with MRCreated only; URL is https and ends /-/merge_requests/<iid>.
	IID     int    `json:"iid,omitempty"`
	URL     string `json:"url,omitempty"`
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Validate applies the rules of specs/delivery/SPEC.md §13 to a record read back from run.json.
func (m RunMR) Validate() error {
	switch m.State {
	case "", MRIntent, MRPushed, MRCreated:
	default:
		return fmt.Errorf("state %q is not intent, pushed or created", m.State)
	}
	for name, v := range map[string]string{"skipped": m.Skipped, "error": m.Error} {
		if len(v) > maxMRText || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return fmt.Errorf("%s must be one line of at most %d bytes", name, maxMRText)
		}
	}
	switch {
	case m.State == "" && m.Skipped == "" && m.Error == "":
		return fmt.Errorf("a record without a state needs skipped or error")
	case m.State != "" && (m.Remote == "" || m.Source == "" || m.Target == ""):
		return fmt.Errorf("state %s needs remote, source and target", m.State)
	case m.State != MRCreated && (m.IID != 0 || m.URL != ""):
		return fmt.Errorf("iid and url only go with state created")
	case m.State == MRCreated && (m.IID <= 0 || m.URL == ""):
		return fmt.Errorf("state created needs iid and url")
	case m.State == MRCreated && (m.Skipped != "" || m.Error != ""):
		return fmt.Errorf("state created has neither skipped nor error")
	}
	for name, v := range map[string]string{"remote": m.Remote, "source": m.Source, "target": m.Target} {
		if strings.HasPrefix(v, "-") || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return fmt.Errorf("%s %q is not a name git would take", name, v)
		}
	}
	if m.State == MRCreated {
		return validMRURL(m.URL, m.IID)
	}
	return nil
}

// validMRURL checks that raw is an https URL without userinfo, query or fragment that ends
// /-/merge_requests/<iid>.
func validMRURL(raw string, iid int) error {
	u, err := url.Parse(raw)
	switch {
	case len(raw) > maxMRURL || strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r >= 0x7f }):
		return fmt.Errorf("url is not a plain address of at most %d bytes", maxMRURL)
	case err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("url %q is not an https address on a host", raw)
	}
	prefix, ok := strings.CutSuffix(u.Path, "/-/merge_requests/"+strconv.Itoa(iid))
	if !ok || !strings.HasPrefix(prefix, "/") || len(prefix) < 2 {
		return fmt.Errorf("url %q does not end /-/merge_requests/%d", raw, iid)
	}
	return nil
}

// Values of RunOptions.Isolation.
const (
	IsolationInplace   = "inplace"
	IsolationWorktree  = "worktree"
	worktreeBranchBase = "orchestrator/"
)

// WorktreeBranch is the branch a run's worktree is on (specs/worktree/SPEC.md §8.2).
func WorktreeBranch(runID string) string { return worktreeBranchBase + runID }

// RunWorktreeBranch is the branch run's worktree must be on: the fallback a story task was given
// (specs/story/SPEC.md §11.1); for any other run the one worktree.branch records, which is named
// when the worktree is created (specs/delivery/SPEC.md §4.3), and orchestrator/<run-id> until
// there is one. Loading checks that a story task's fallback has the story pattern and names its
// story and task, and any other run's branch passes ValidBranchName. A story task's worktree is on
// this fallback or, when its repository's rules named one, on the name worktree.branch records
// (specs/delivery/SPEC.md §9.1). A review run's worktree is detached: its branch is "", and only a
// run with a review target may have none.
func RunWorktreeBranch(run *Run) string {
	if run.ReviewTarget != nil {
		return ""
	}
	if src := run.Task.Source; src != nil && src.Kind == TaskSourceStory {
		return src.Branch
	}
	if w := run.Worktree; w != nil && w.Branch != "" {
		return w.Branch
	}
	return WorktreeBranch(run.ID)
}

// RunWorktree is the record of a run's worktree. Everything a resume uses from it is derived
// and checked, never trusted (specs/worktree/SPEC.md §8).
type RunWorktree struct {
	Path       string   `json:"path"`
	Branch     string   `json:"branch"`
	BaseRef    string   `json:"base_ref"`
	BaseBranch string   `json:"base_branch,omitempty"`
	AdminDir   string   `json:"admin_dir"`
	SetupDone  bool     `json:"setup_done,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Removed    bool     `json:"removed,omitempty"`
}

// RunOptions are the config choices that change how a run moves between steps. They are
// copied into the run when it is created, so a resume follows the rule the run started with
// whatever the config says now.
type RunOptions struct {
	// Isolation is where the run executes: IsolationInplace or IsolationWorktree, chosen at run
	// start from execution.isolation and run --inplace (specs/worktree/SPEC.md §6). Empty in a
	// run written before the field, which is inplace; see IsolationMode.
	Isolation string `json:"isolation,omitempty"`
	// ReviewFixMedium is review.fix_medium: a passing review with a medium issue sends the run
	// back to implement once.
	ReviewFixMedium bool `json:"review_fix_medium,omitempty"`
	// PathRules is a copy of policy.paths (specs/governance/SPEC.md §4.4). Nil, never empty,
	// when there are none, so a run without rules writes no options.
	PathRules []PathRule `json:"path_rules,omitempty"`
	// JiraWrite is a copy of jira.write (specs/delivery/SPEC.md §10): what the run writes to its
	// task's Jira issue after a merge request. A resume reads it instead of the config.
	JiraWrite JiraWriteOptions `json:"jira_write,omitzero"`
}

// IsolationMode is the run's isolation, inplace when none was recorded.
func (o RunOptions) IsolationMode() string {
	if o.Isolation == "" {
		return IsolationInplace
	}
	return o.Isolation
}

// SecretFinding is one likely secret a step added: where, which rule, and the fingerprint
// that identifies it across steps and resumes (secretscan.Finding). Never the value.
type SecretFinding struct {
	StepIndex   int    `json:"step_index"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Rule        string `json:"rule"`
	Fingerprint string `json:"fingerprint"`
}

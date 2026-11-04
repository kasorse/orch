package action

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/story"
)

// The two planning calls of a story (specs/story/SPEC.md §7). Like the translator's, they are
// calls outside a run's steps: fixed here, with a fixed authority, and in no workflow and in
// no domain.AuthorityFor table. A survey reads one of the story's services with read
// authority; specify gets no tools and works from the request and the surveys. The caller
// (story plan) picks the services - only those the task names (§7.1), never every candidate
// under story.roots - and loads each one's route; nothing here reads a config.

// surveyRules state the bounds story.ParseSurvey enforces.
var surveyRules = fmt.Sprintf(`"summary" is at most %d bytes. "relevant_files" lists at most %d paths, relative to the repository root, `+
	`slash-separated and clean (no "..", no leading "/"). "interfaces" (an API, an event or a schema, each with the file that defines it) `+
	`and "constraints" (what a change in this repository must respect) list at most %d and %d entries, each one line of at most %d bytes. `+
	`Leave a list empty rather than guess.`,
	story.MaxSurveySummaryBytes, story.MaxSurveyFiles, story.MaxSurveyInterfaces, story.MaxSurveyConstraints, story.MaxSurveyItemBytes)

// BuildSurveyPrompt is the prompt of the survey of repo (SPEC §7.2): the request, the
// repository's name, and what to describe.
func BuildSurveyPrompt(request, repo string) string {
	var b strings.Builder
	b.WriteString("ACTION: survey\n\n")
	b.WriteString("Survey the repository \"" + repo + "\" for the following software engineering task: describe what in this " +
		"repository relates to it - the code the task would touch, the interfaces the repository provides or consumes, " +
		"and the constraints a change must respect. A later step splits the task into a specification and tasks from " +
		"the surveys of every repository it spans; this step only reports. The working directory is the repository's " +
		"checkout. " + readOnlyRule + " Respond with a single JSON object, no markdown fences, no prose before or after it, " +
		"matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"relevant_files\": [\"...\"],\n  \"interfaces\": [\"...\"],\n  \"constraints\": [\"...\"]\n}\n\n")
	b.WriteString(surveyRules + "\n\n")
	b.WriteString("Task:\n" + taskBlock(request))
	return b.String()
}

// SurveyInput is one service's survey, as the specify prompt embeds it.
type SurveyInput struct {
	Repo   string
	Answer story.SurveyAnswer
}

// specifyRules are the rules of SPEC §7.3 a task must follow.
const specifyRules = `Every task follows these rules:
- it is a vertical slice that one run can implement, test and review (size S or M; an L is split);
- after it, that repository's tests pass;
- it updates, in the same task, the documentation its own change makes out of date (the sections of the specification, README and other docs that describe what it changes); no task is set aside to write the documentation of the others;
- it changes exactly one repository;
- it lists the tasks it needs in "depends_on"; within one repository the tasks a task needs form a chain: of any two of them, one depends on the other;
- work it finds necessary in a repository that is not listed above goes into the spec's "out_of_scope" or "risks", never into a task;
- contracts between repositories (an API, an event, a schema) are written in the spec's "contracts", and the task that provides one comes before the tasks that consume it;
- the spec and the tasks are written in the language of the task; IDs and keys are ASCII.`

// specifyShapeRules state the rules story.ParseSpecify enforces on the answer's fields.
var specifyShapeRules = fmt.Sprintf(`"slug" names the spec's directory: 2 to %d lowercase letters, digits and "-", starting with a letter or digit. `+
	`"prefix" is two capital letters, and every task's "id" is the prefix and two digits (RF01, RF02, ...), unique. `+
	`Every "title" is one line of at most %d bytes. "size" is S, M or L. "depends_on" lists IDs of other tasks of this answer. `+
	`"acceptance" lists at most %d items of at most %d characters, each one observable, checkable statement about the result. `+
	`"goal" and "notes" are Markdown without "#" or "##" headings. A task's title, goal, acceptance and notes are at most %d bytes together, `+
	`the five fields of "spec" at most %d.`,
	story.MaxSlugLen, story.MaxTitleBytes, story.MaxAcceptanceItems, story.MaxAcceptanceRunes, story.MaxTaskTextBytes, story.MaxSpecTextBytes)

// BuildSpecifyPrompt is the prompt of the specify call (SPEC §7.3): the request, the closed list
// of repositories, maxTasks, the rules a task follows, and every survey in its own fence, in
// the order of the repositories' names. A survey is a model's account of a repository that may
// be hostile, so it is data: fenced, its list entries on one line each.
func BuildSpecifyPrompt(request string, surveys []SurveyInput, repos []string, maxTasks int) string {
	var b strings.Builder
	b.WriteString("ACTION: specify\n\n")
	b.WriteString("Write a specification and a list of tasks for the following software engineering task, which spans the " +
		"repositories listed below. You have no tools: what you know of each repository is its survey, given after the task. " +
		"Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"slug\": \"...\",\n  \"prefix\": \"...\",\n  \"title\": \"...\",\n" +
		"  \"spec\": {\n    \"problem\": \"...\",\n    \"approach\": \"...\",\n    \"contracts\": \"...\",\n    \"out_of_scope\": \"...\",\n    \"risks\": \"...\"\n  },\n" +
		"  \"tasks\": [\n    {\"id\": \"...\", \"repo\": \"...\", \"title\": \"...\", \"size\": \"M\", \"depends_on\": [\"...\"], " +
		"\"goal\": \"...\", \"acceptance\": [\"...\"], \"notes\": \"...\"}\n  ]\n}\n\n")
	fmt.Fprintf(&b, "Repositories (the story's services, the only values \"repo\" may take): %s\nAt most %d tasks.\n\n",
		strings.Join(slices.Sorted(slices.Values(repos)), ", "), maxTasks)
	b.WriteString(specifyRules + "\n\n")
	b.WriteString(specifyShapeRules + "\n\n")
	b.WriteString("Task:\n" + taskBlock(request))

	sorted := slices.SortedFunc(slices.Values(surveys), func(x, y SurveyInput) int { return cmp.Compare(x.Repo, y.Repo) })
	if len(sorted) > 0 {
		b.WriteString("\nSurveys, one per repository, each written by a read-only step in that repository:\n")
	}
	for i, s := range sorted {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fence("survey", "the survey of the repository "+s.Repo, renderSurvey(s)))
	}
	return b.String()
}

// renderSurvey is a survey as the specify prompt shows it, inside its fence.
func renderSurvey(s SurveyInput) string {
	var b strings.Builder
	b.WriteString("Repository: " + s.Repo + "\nSummary:\n" + strings.TrimSpace(s.Answer.Summary) + "\n")
	for _, list := range []struct {
		heading string
		items   []string
	}{
		{"Relevant files", s.Answer.RelevantFiles},
		{"Interfaces", s.Answer.Interfaces},
		{"Constraints", s.Answer.Constraints},
	} {
		items := make([]string, 0, len(list.items))
		for _, item := range list.items {
			items = append(items, oneLine(item))
		}
		writeList(&b, list.heading, items)
	}
	return b.String()
}

// PlanningLogger persists the raw output of a planning call as logs/<name>.{stdout,stderr}.log
// under the story's directory; name is survey-<repo> or specify.
type PlanningLogger interface {
	AppendCallLog(name, stream, content string) error
	CallLogPath(name, stream string) string
}

// PlanningCall is what a planning call runs with besides its prompt. Route and Timeout are the
// base route and the timeout of the action the call runs on (SPEC A2): for a survey the
// surveyed repository's own actions.investigate, for specify the story home's actions.investigate.
type PlanningCall struct {
	Registry *provider.Registry
	Route    config.ProviderModel
	// Timeout bounds the call when it is positive; otherwise only ctx does, which should
	// carry execution.timeout.
	Timeout time.Duration
	// Observer, Stream and Logger are optional (nil: no progress, no live output, no logs).
	Observer CallObserver
	Stream   io.Writer
	Logger   PlanningLogger
}

// SurveyRepo is the repository a survey reads: one of the story's services.
type SurveyRepo struct {
	Name string
	Dir  string
	// ExtraDeny is the repository's context.extra_deny and RunsDir its runs directory,
	// relative to Dir and slash-separated ("" when outside it): with projectctx's patterns,
	// the survey's deny list, as every step in that repository has it.
	ExtraDeny []string
	RunsDir   string
}

// SurveyResult is what a survey call gave, as far as it got: the prompt, the raw answer (also
// when it is refused, for the story's artifacts) and the usage (nil when the CLI reported
// none or the call did not run).
type SurveyResult struct {
	Answer story.SurveyAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

// SpecifyInput is what the specify call is asked from.
type SpecifyInput struct {
	Request string
	Surveys []SurveyInput
	// Repos are the story's services and MaxTasks is story.max_tasks.
	Repos    []string
	MaxTasks int
	// SpecDirExists reports whether <spec_dir>/<slug> exists in the story home; nil does not ask.
	SpecDirExists func(slug string) bool
}

// SpecifyResult is SurveyResult for the specify call.
type SpecifyResult struct {
	Answer story.SpecifyAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

// Survey runs the survey of one service (SPEC §7.2): in its checkout, with read authority and
// the repository's deny list, and decodes the answer strictly. An answer that holds no JSON
// object or breaks a rule is an *apperr.InvalidResponseError naming the field.
func Survey(ctx context.Context, call PlanningCall, repo SurveyRepo, request string) (SurveyResult, error) {
	res := SurveyResult{Prompt: BuildSurveyPrompt(request, repo.Name)}
	if repo.Dir == "" {
		return res, &apperr.WorkflowError{Msg: fmt.Sprintf("survey of %s: the repository has no checkout directory", repo.Name)}
	}
	kind := planningKind{action: "survey", log: "survey-" + repo.Name, timeout: "actions.investigate.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{
		Prompt: res.Prompt, WorkingDir: repo.Dir, Authority: domain.AuthorityRead,
		DenyRead: denyList(repo.ExtraDeny, repo.RunsDir),
	})
	if err != nil {
		return res, err
	}
	obj := extractJSONObject(res.Output)
	if obj == "" {
		return res, &apperr.InvalidResponseError{Call: "survey", Repo: repo.Name, Msg: "the answer holds no JSON object"}
	}
	res.Answer, err = story.ParseSurvey([]byte(obj))
	if err != nil {
		return res, invalidResponse("survey", repo.Name, err)
	}
	return res, nil
}

// Specify runs the specify call (SPEC §7.3) with no tools, no working directory and no deny
// list (there is nothing it could read), and checks the answer against §7.4 and the graph
// rules of §9. An answer that holds no JSON object or breaks a rule is an
// *apperr.InvalidResponseError naming the field; the raw answer is in the result either way.
func Specify(ctx context.Context, call PlanningCall, in SpecifyInput) (SpecifyResult, error) {
	res := SpecifyResult{Prompt: BuildSpecifyPrompt(in.Request, in.Surveys, in.Repos, in.MaxTasks)}
	kind := planningKind{action: "specify", log: "specify", timeout: "actions.investigate.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{Prompt: res.Prompt, Authority: domain.AuthorityNone})
	if err != nil {
		return res, err
	}
	obj := extractJSONObject(res.Output)
	if obj == "" {
		return res, &apperr.InvalidResponseError{Call: "specify", Msg: "the answer holds no JSON object"}
	}
	res.Answer, err = story.ParseSpecify([]byte(obj), in.Repos, in.MaxTasks, in.SpecDirExists)
	if err != nil {
		return res, invalidResponse("specify", "", err)
	}
	return res, nil
}

func invalidResponse(call, repo string, err error) error {
	var aerr *story.AnswerError
	if errors.As(err, &aerr) {
		return &apperr.InvalidResponseError{Call: call, Repo: repo, Field: aerr.Field, Msg: aerr.Msg, Err: err}
	}
	return &apperr.InvalidResponseError{Call: call, Repo: repo, Msg: err.Error(), Err: err}
}

// planningKind names a planning call in what it reports: the ProviderError's action, the
// logs' name, and the setting its timeout comes from.
type planningKind struct {
	action  string
	log     string
	timeout string
}

// run sends req on the call's route and returns the raw answer and the usage, also with an
// error. Like Dispatcher.call it fills in the *apperr.ProviderError's action and log path,
// and the configured timeout when that, and not ctx, ended the call.
func (c PlanningCall) run(ctx context.Context, kind planningKind, req provider.Request) (string, *domain.Usage, error) {
	pm := c.Route
	if c.Registry == nil {
		return "", nil, &apperr.WorkflowError{Msg: fmt.Sprintf("%s: no provider registry", kind.action)}
	}
	client, err := c.Registry.Resolve(pm.Provider)
	if err != nil {
		return "", nil, &apperr.WorkflowError{
			Msg: fmt.Sprintf("no provider client registered for %q (%s)", pm.Provider, kind.action),
			Err: err,
		}
	}
	callCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	req.Model, req.Effort, req.Stream = pm.Model, pm.Effort, c.Stream
	req.Activity = provider.ActivityHook(c.Observer)

	if c.Observer != nil {
		c.Observer.ProviderCallStarted(pm.Provider, pm.Model)
	}
	start := time.Now()
	stopHeartbeat := startHeartbeat(c.Observer)
	result, err := client.Execute(callCtx, req)
	stopHeartbeat()
	provider.ReportUsage(c.Observer, result)
	if c.Observer != nil {
		c.Observer.ProviderCallFinished(pm.Provider, pm.Model, time.Since(start), err)
	}

	wroteStdout := c.log(kind.log, "stdout", result.Output)
	wroteStderr := c.log(kind.log, "stderr", result.Stderr)
	if err != nil {
		var providerErr *apperr.ProviderError
		if errors.As(err, &providerErr) {
			if providerErr.Action == "" {
				providerErr.Action = kind.action
			}
			if providerErr.Kind == apperr.FailureTimeout && c.Timeout > 0 && ctx.Err() == nil {
				providerErr.ConfiguredTimeout, providerErr.TimeoutSetting = c.Timeout, kind.timeout
			}
			// Point only at a log that exists (MVP.md §23).
			if providerErr.LogPath == "" {
				switch {
				case wroteStdout:
					providerErr.LogPath = c.Logger.CallLogPath(kind.log, "stdout")
				case wroteStderr:
					providerErr.LogPath = c.Logger.CallLogPath(kind.log, "stderr")
				}
			}
		}
		return result.Output, result.Usage, err
	}
	return result.Output, result.Usage, nil
}

// log is best-effort: losing a log never fails the call. It reports whether the log was
// written.
func (c PlanningCall) log(name, stream, content string) bool {
	if c.Logger == nil || content == "" {
		return false
	}
	return c.Logger.AppendCallLog(name, stream, content) == nil
}

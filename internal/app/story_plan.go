package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/jira"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/story"
)

const storyPlanUsage = `usage: orchestrator story plan [--repo <name>]... "<task>"   or   orchestrator story plan --jira <KEY|URL> [--repo <name>]... ["notes"]`

// storiesDir is where a story's own directory lives, relative to the story home.
const storiesDir = ".orchestrator/stories"

// The planning logger of a story is its directory.
var _ action.PlanningLogger = (*persistence.StoryPlan)(nil)

// storyService is one of a story's services with what its survey runs on: its own
// orchestrator.yaml's actions.plan route and timeout, and its deny list.
type storyService struct {
	serviceCandidate
	survey  action.SurveyRepo
	route   config.ProviderModel
	timeout time.Duration
}

// storyPlan is `orchestrator story plan` (specs/story/SPEC.md §6, §7): it finds the services the
// task names, surveys each of them, asks specify for a spec and its tasks, and writes the spec
// under story.spec_dir - or, for one task, prints the command that runs it. request is the
// task or, with jiraRef, the operator's notes on the ticket. Nothing is called and no story
// directory is created before every check that needs no model has passed.
func storyPlan(ctx context.Context, configPath, request, jiraRef string, repos []string, verbose bool, stdout io.Writer) error {
	reporter := newReporter(stdout, verbose)
	defer reporter.Finish()

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	// Fail fast (MVP.md §7): the home's whole chain, with the Jira variables for --jira.
	cfg, _, _, _, _, err := loadAndValidateConfig(configPath, "", "", registry, osExec, reporter,
		func(config.Config) config.DependencyOptions { return config.DependencyOptions{Jira: jiraRef != ""} })
	if err != nil {
		return err
	}
	if cfg.Story == nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("story plan needs a story section in %s (specs/story/SPEC.md §5)", configPath)}
	}
	var jiraKey string
	if jiraRef != "" {
		if jiraKey, err = jira.ParseRef(jiraRef, cfg.Jira.BaseURL); err != nil {
			return err
		}
	}

	// execution.timeout bounds the whole plan: the fetch, git and every call.
	planCtx, cancel := context.WithTimeout(ctx, cfg.Execution.Timeout)
	defer cancel()

	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to determine working directory", Err: err}
	}
	repo, err := repository.Open(planCtx, cwd, osExec)
	if err != nil {
		return err
	}
	home, err := repo.TopLevel(planCtx)
	if err != nil {
		return err
	}
	// git reports the top level with symlinks resolved; so must the working directory be, for
	// the paths shown relative to it.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	// The one Jira request. A failure creates nothing.
	var ticket domain.Ticket
	if jiraKey != "" {
		if ticket, request, err = taskFromJira(planCtx, cfg, jiraKey, request, reporter); err != nil {
			return err
		}
	}

	// The services the task names (§7.1): a refusal here makes no call and creates nothing.
	roots, err := cfg.Story.ResolveRoots(home)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	cands, err := candidates(roots, home)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	found, err := findServices(planCtx, osExec, request, ticket.Components, repos, cands, roots, cfg.Story.MaxServices)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	shown := make([]logging.StoryService, len(found))
	names := make([]string, len(found))
	for i, s := range found {
		shown[i] = logging.StoryService{Name: s.Name, Path: s.Path}
		names[i] = s.Name
	}
	reporter.StoryServices(shown)

	// Every service's own config, before anything is created: a service that cannot be
	// surveyed stops the plan here.
	services := make([]storyService, len(found))
	for i, s := range found {
		if services[i], err = loadStoryService(planCtx, s, home, cfg, registry, reporter); err != nil {
			return err
		}
	}

	plan, err := persistence.NewStoryPlan(filepath.Join(home, storiesDir))
	if err != nil {
		return err
	}
	shownDir := func() string { return displayPath(cwd, plan.Dir()) }
	reporter.StoryPlanDir(shownDir())
	// A plan that does not reach a spec keeps its directory, for the operator (§14).
	keep := func(err error) error {
		reporter.StoryPlanKept(shownDir())
		return err
	}
	if jiraKey != "" {
		if err := plan.SaveTicket(ticket); err != nil {
			return keep(err)
		}
	}

	var stream io.Writer
	if verbose {
		stream = reporter.StreamWriter()
	}
	call := func(route config.ProviderModel, timeout time.Duration) action.PlanningCall {
		return action.PlanningCall{
			Registry: registry, Route: route, Timeout: timeout,
			Observer: reporter, Stream: stream, Logger: plan,
		}
	}

	calls := map[string]*domain.Usage{}
	surveys := make([]action.SurveyInput, 0, len(services))
	for _, s := range services {
		reporter.StorySurvey(s.Name)
		res, err := action.Survey(planCtx, call(s.route, s.timeout), s.survey, request)
		name := "survey-" + s.Name
		calls[name] = res.Usage
		if err := saveCall(plan, name, res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
			return keep(err)
		}
		surveys = append(surveys, action.SurveyInput{Repo: s.Name, Answer: res.Answer})
	}

	specRoot := filepath.Join(home, cfg.Story.SpecDir)
	reporter.StorySpecify()
	res, err := action.Specify(planCtx, call(cfg.Actions.Plan.ProviderModel, cfg.Actions.Plan.Timeout), action.SpecifyInput{
		Request: request, Surveys: surveys, Repos: names, MaxTasks: cfg.Story.MaxTasks,
		SpecDirExists: func(slug string) bool {
			_, err := os.Lstat(filepath.Join(specRoot, slug))
			return err == nil
		},
	})
	calls["specify"] = res.Usage
	if err := saveCall(plan, "specify", res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
		return keep(err)
	}

	answer := res.Answer
	tasks := answer.Tasks()
	if err := story.Validate(tasks, names, cfg.Story.MaxTasks); err != nil {
		return keep(&apperr.InvalidResponseError{Call: "specify", Field: "tasks", Msg: err.Error(), Err: err})
	}

	// One task is not a story (§7.4): nothing under spec_dir, only the command that runs it.
	if len(tasks) == 1 {
		for _, s := range services {
			if s.Name == tasks[0].Repo {
				reporter.StoryOneTask(s.Name, s.Path, tasks[0].Title)
			}
		}
		reporter.StoryPlanKept(shownDir())
		return nil
	}

	// The story directory takes the slug once the spec is written; a story of that name
	// would make the rename fail after the spec is on disk, so it is refused first.
	if plan.SlugTaken(answer.Slug) {
		return keep(&apperr.ConfigError{Msg: fmt.Sprintf("a story named %s exists already in %s; nothing was written under %s",
			answer.Slug, displayPath(cwd, filepath.Join(home, storiesDir)), cfg.Story.SpecDir)})
	}
	specDir, err := writeSpec(specRoot, answer, tasks)
	if err != nil {
		return keep(err)
	}
	if err := plan.Rename(answer.Slug); err != nil {
		return keep(err)
	}
	record := persistence.StoryRecord{
		Slug:       answer.Slug,
		SpecDir:    filepath.ToSlash(filepath.Join(cfg.Story.SpecDir, answer.Slug)),
		JiraParent: ticket.Key,
		CreatedAt:  time.Now().UTC(),
		Services:   map[string]string{},
		Status:     story.StatusPlanned,
		Calls:      calls,
	}
	for _, s := range services {
		record.Services[s.Name] = s.Path
	}
	if err := plan.SaveRecord(record); err != nil {
		return err
	}
	reporter.StorySpecWritten(displayPath(cwd, specDir), len(tasks))
	return nil
}

// loadStoryService loads what the survey of one service runs on from that service's own
// orchestrator.yaml (SPEC §5, A2): Load (with the org layer), Validate, the authorities its
// routes can enforce and its CLIs' versions. The story home's config, already through the
// whole chain, serves the home when it is one of the services. The claude credential mode
// stays the home's: the adapter is shared.
func loadStoryService(ctx context.Context, s serviceCandidate, home string, homeCfg config.Config,
	registry *provider.Registry, reporter *logging.ConsoleReporter) (storyService, error) {
	cfg := homeCfg
	if !samePath(s.Path, home) {
		path := filepath.Join(s.Path, defaultConfigPath)
		fail := func(err error) (storyService, error) {
			return storyService{}, apperr.NewConfigError(fmt.Sprintf("service %s (%s)", s.Name, path), err)
		}
		if _, err := os.Stat(path); err != nil {
			return storyService{}, &apperr.ConfigError{Msg: fmt.Sprintf(
				"service %s has no %s: every service keeps its own config (specs/story/SPEC.md §5)", s.Name, path)}
		}
		orgPath, _ := config.FindOrgConfig(orgConfigEtcPath, os.Getenv)
		loaded, _, err := config.LoadLayered(path, orgPath)
		if err != nil {
			return fail(err)
		}
		if err := config.Validate(loaded); err != nil {
			return fail(err)
		}
		if err := config.ValidateAuthorities(loaded, registry); err != nil {
			return fail(err)
		}
		if _, err := checkProviderCLIs(ctx, loaded, registry, reporter); err != nil {
			return fail(err)
		}
		cfg = loaded
	}
	plan := cfg.Actions.Plan
	return storyService{
		serviceCandidate: s,
		survey:           action.SurveyRepo{Name: s.Name, Dir: s.Path, ExtraDeny: cfg.Context.ExtraDeny, RunsDir: runsBaseDir},
		route:            plan.ProviderModel,
		timeout:          plan.Timeout,
	}, nil
}

// saveCall writes the artifact of one planning call, also when it failed, and returns the
// call's error, or the failure to save when the call succeeded.
func saveCall(plan *persistence.StoryPlan, name string, answer any, output, prompt string, usage *domain.Usage, callErr error) error {
	a := persistence.PlanningArtifact{Output: output, Prompt: prompt, Usage: usage}
	if callErr != nil {
		a.Error = callErr.Error()
	} else {
		a.Answer = answer
	}
	if err := plan.SaveArtifact(name, a); err != nil && callErr == nil {
		return err
	}
	return callErr
}

// writeSpec writes <specRoot>/<slug>/ (SPEC §8): README.md, SPEC.md and a task file per task,
// named by story.TaskFileName. The directory is claimed with os.Mkdir, so an existing one -
// empty or not - is refused and left as it is; a failure part way removes only what this call
// created. It returns the directory.
func writeSpec(specRoot string, answer story.SpecifyAnswer, tasks []story.Task) (string, error) {
	if err := os.MkdirAll(specRoot, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", specRoot, err)
	}
	dir := filepath.Join(specRoot, answer.Slug)
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", &apperr.ConfigError{Msg: fmt.Sprintf("%s exists already: refused, nothing was written", dir)}
		}
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	type specFile struct {
		name string
		data []byte
	}
	files := []specFile{
		{"README.md", []byte(story.RenderREADME(answer.Title, story.StatusPlanned, tasks, nil))},
		{"SPEC.md", story.RenderSpec(answer.Title, answer.Spec)},
	}
	for i, t := range tasks {
		name := filepath.Join("tasks", story.TaskFileName(t.ID, t.Title))
		files = append(files, specFile{name, story.RenderTaskFile(t, answer.TaskAnswers[i].Size)})
	}
	err := os.Mkdir(filepath.Join(dir, "tasks"), 0o755)
	for _, f := range files {
		if err != nil {
			break
		}
		err = writeNewFile(filepath.Join(dir, f.name), f.data)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write the spec in %s: %w", dir, err)
	}
	return dir, nil
}

// writeNewFile creates path, which must not exist, with data.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// displayPath is p relative to the working directory when it is inside it, else p.
func displayPath(cwd, p string) string {
	rel, err := filepath.Rel(cwd, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}

package app

import (
	"context"
	"errors"
	"io"

	"github.com/kasorse/orch/internal/action"
	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/classification"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/decision"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/logging"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/projectctx"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/repository"
)

// The clarify step's wiring (specs/clarify, Approach §3): the instructions Go chooses from the
// repository, the one call on the clarify route with authority none, and the operator at the
// terminal.

// docsSource chooses the instructions of the clarify call from the repository (projectctx.Docs).
type docsSource struct {
	root string
	opts projectctx.DocsOptions
}

func (d docsSource) Docs(ctx context.Context, request, requestEN string) (domain.Instructions, error) {
	return projectctx.Docs(ctx, d.root, d.opts, request, requestEN)
}

// runCallLogger is the planning call's logger for one run: logs/<name>.<stream>.log.
type runCallLogger struct {
	store *persistence.Store
	runID string
}

func (l runCallLogger) AppendCallLog(name, stream, content string) error {
	return l.store.AppendCallLog(l.runID, name, stream, content)
}

func (l runCallLogger) CallLogPath(name, stream string) string {
	return l.store.CallLogPath(l.runID, name, stream)
}

// clarifyCaller makes the clarify call (action.Clarify, domain.AuthorityNone) on the route of the
// clarify section and records its artifact. The adapters run it with the scrubbed environment, and
// the claude adapter gives authority none no tools.
type clarifyCaller struct {
	call  action.PlanningCall
	store *persistence.Store
}

func (c clarifyCaller) Clarify(ctx context.Context, runID string, n int, in action.ClarifyPromptInput) (classification.ClarifyReply, error) {
	call := c.call
	call.Logger = runCallLogger{store: c.store, runID: runID}
	res, err := action.Clarify(ctx, call, in, n)
	// Best-effort, like a log: the artifact is a record for the operator, and losing it must not
	// fail a run that has its answer.
	_, _ = c.store.SaveClarifyArtifact(runID, n, newClarifyArtifact(in, res), res.Prompt)
	return classification.ClarifyReply{Answer: res.Answer, Usage: res.Usage}, err
}

// clarifyArtifact is artifacts/clarify-<N>.json besides the prompt as sent: which excerpts the call
// was given and what it answered.
type clarifyArtifact struct {
	Mode         domain.ClarifyMode     `json:"mode"`
	Rules        int                    `json:"rules"`
	Excerpts     []clarifyExcerpt       `json:"excerpts"`
	Truncated    bool                   `json:"truncated,omitempty"`
	Points       []clarifyArtifactPoint `json:"points"`
	SummaryShort string                 `json:"summary_short,omitempty"`
}

type clarifyExcerpt struct {
	Path    string `json:"path"`
	Section string `json:"section,omitempty"`
}

type clarifyArtifactPoint struct {
	Unclear  string `json:"unclear"`
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
	Basis    string `json:"basis"`
	Source   string `json:"source,omitempty"`
}

func newClarifyArtifact(in action.ClarifyPromptInput, res action.ClarifyResult) clarifyArtifact {
	a := clarifyArtifact{
		Mode: in.Mode, Rules: len(in.Instructions.Rules), Truncated: in.Instructions.Truncated,
		Excerpts: []clarifyExcerpt{}, Points: []clarifyArtifactPoint{}, SummaryShort: res.Answer.SummaryShort,
	}
	for _, e := range in.Instructions.Excerpts {
		a.Excerpts = append(a.Excerpts, clarifyExcerpt{Path: e.Path, Section: e.Section})
	}
	for _, p := range res.Answer.Points {
		a.Points = append(a.Points, clarifyArtifactPoint{
			Unclear: p.Unclear, Question: p.Question, Answer: p.Answer, Basis: string(p.Basis), Source: p.Source,
		})
	}
	return a
}

// newClarifyLoop builds the clarify step for a run: the decision provider that re-checks the
// clarity, and, at a terminal outside --mode auto, the operator who answers and adds to the task.
func newClarifyLoop(cfg config.Config, registry *provider.Registry, store *persistence.Store, repo *repository.Repository, cwd string,
	decisions decision.Provider, reporter *logging.ConsoleReporter, prompter interaction.Prompter, interactive bool, stream io.Writer) *classification.ClarifyLoop {
	c := cfg.Clarify
	loop := &classification.ClarifyLoop{
		Docs: docsSource{root: cwd, opts: projectctx.DocsOptions{
			Repo: repo, Globs: c.DocGlobs(), MaxBytes: c.InstructionBytes(), ExtraDeny: cfg.Context.ExtraDeny,
		}},
		Caller: clarifyCaller{store: store, call: action.PlanningCall{
			Registry: registry, Route: c.ProviderModel, Timeout: c.Timeout,
			Observer: logging.CallReporter{ConsoleReporter: reporter, Label: "Clarifying"}, Stream: stream,
		}},
		Decisions:   decisions,
		Policy:      cfg.DecisionPolicy(),
		Interactive: interactive,
		MaxRounds:   c.AdditionRounds(),
	}
	if interactive {
		loop.Operator = interaction.NewClarifier(prompter, reporter.PausingWriter())
	}
	return loop
}

// stopUndecided ends a run that got no decision: a task that stayed unclear stops with
// "task needs input" (exit 4, nothing to resume), anything else fails the run. Such a run has no
// decision.json; the calls its decision step made - the clarify call and the re-checks among them -
// are counted in its outcome from the trace the step had so far (out.trace, when it has one).
func stopUndecided(store *persistence.Store, run *domain.Run, out decisionOutcome, err error) error {
	var partial *domain.DecisionTrace
	if out.trace != nil {
		t := out.trace(domain.Workflow{})
		partial = &t
	}
	var needs *classification.NeedsInputError
	if !errors.As(err, &needs) {
		run.Status = domain.RunFailed
		run.FailureReason = err.Error()
		_ = writeOutcomeWith(store, run, partial)
		return err
	}
	run.Task.Clarification = needs.Clarification
	run.Status = domain.RunStopped
	run.FailureReason = domain.FailureTaskNeedsInput
	_ = writeOutcomeWith(store, run, partial)
	return &apperr.LimitExceededError{Reason: "Run status: stopped\nReason: " + domain.FailureTaskNeedsInput + "\n\n" +
		needs.Clarification.NeedsInputText()}
}

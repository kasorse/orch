// Package classifier turns a raw task description into a structured domain.Classification
// via a single AI call, with strict parsing/validation (malformed output is a hard
// error, not something the workflow retries).
package classifier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

var errNoJSONObject = errors.New("no JSON object found in classifier output")

// actionName is what classification failures are tagged with in ProviderError.Action and
// what their log files are keyed by (logs/classify.{stdout,stderr}.log) - distinguishing
// them from the workflow's per-step logs, since classification runs once, before any step.
const actionName = "classify"

// CallObserver mirrors action.CallObserver's method shape structurally (not by import) so
// the same concrete type - typically *logging.ConsoleReporter - can be wired into both the
// action.Dispatcher and the Classifier without either package depending on the other.
type CallObserver interface {
	ProviderCallStarted(providerID, model string)
	ProviderCallHeartbeat(elapsed time.Duration)
	ProviderCallFinished(providerID, model string, duration time.Duration, err error)
}

// heartbeatInterval mirrors action.heartbeatInterval (kept as an independent constant -
// see the package doc comment on why this package never imports internal/action).
const heartbeatInterval = 30 * time.Second

// Logger persists the classifier's raw provider call output. Satisfied by
// *persistence.Store.
type Logger interface {
	AppendClassifierLog(runID, stream, content string) error
	// ClassifierLogPath returns the path AppendClassifierLog writes stream to, without
	// writing anything - used to populate apperr.ProviderError.LogPath.
	ClassifierLogPath(runID, stream string) string
}

type Classifier struct {
	Client     provider.Client
	ProviderID string
	Model      string
	Logger     Logger

	// Observer, if set, receives a start/finish notification around the classification
	// call, mirroring action.Dispatcher.Observer. Nil means no-op.
	Observer CallObserver
	// Stream, if set (--verbose only), mirrors action.Dispatcher.Stream - a live tee of
	// the CLI's combined output. Nil means capture-only.
	Stream io.Writer
}

func New(client provider.Client, providerID, model string, logger Logger) *Classifier {
	return &Classifier{Client: client, ProviderID: providerID, Model: model, Logger: logger}
}

// Classify runs the classification prompt and persists the raw stdout/stderr it produced
// (success or failure) to runID's classify.* log files, so a hung/failed classification is
// diagnosable the same way a failing workflow step is.
func (c *Classifier) Classify(ctx context.Context, runID, task string) (domain.Classification, error) {
	prompt := BuildClassifyPrompt(task)

	if c.Observer != nil {
		c.Observer.ProviderCallStarted(c.ProviderID, c.Model)
	}
	start := time.Now()
	stopHeartbeat := c.startHeartbeat()
	result, err := c.Client.Execute(ctx, provider.Request{Prompt: prompt, Model: c.Model, Stream: c.Stream})
	stopHeartbeat()
	if c.Observer != nil {
		c.Observer.ProviderCallFinished(c.ProviderID, c.Model, time.Since(start), err)
	}

	c.logStream(runID, "stdout", result.Output)
	c.logStream(runID, "stderr", result.Stderr)

	if err != nil {
		var providerErr *apperr.ProviderError
		if errors.As(err, &providerErr) {
			if providerErr.Action == "" {
				providerErr.Action = actionName
			}
			if c.Logger != nil && providerErr.LogPath == "" {
				stream := "stderr"
				if result.Output != "" {
					stream = "stdout"
				}
				providerErr.LogPath = c.Logger.ClassifierLogPath(runID, stream)
			}
		}
		return domain.Classification{}, err
	}

	raw := extractJSONObject(result.Output)
	if raw == "" {
		return domain.Classification{}, &apperr.ClassificationError{
			Raw: result.Output,
			Err: errNoJSONObject,
		}
	}

	var classification domain.Classification
	if err := json.Unmarshal([]byte(raw), &classification); err != nil {
		return domain.Classification{}, &apperr.ClassificationError{Raw: result.Output, Err: err}
	}

	if err := classification.Validate(); err != nil {
		return domain.Classification{}, &apperr.ClassificationError{Raw: result.Output, Err: err}
	}

	return classification, nil
}

// startHeartbeat mirrors action.Dispatcher.startHeartbeat - see that method's comment.
func (c *Classifier) startHeartbeat() func() {
	if c.Observer == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				c.Observer.ProviderCallHeartbeat(time.Since(start))
			}
		}
	}()
	return func() { close(done) }
}

// logStream is best-effort: a failure to persist the log does not fail classification,
// matching action.Dispatcher.saveArtifact's tolerance for persistence errors.
func (c *Classifier) logStream(runID, stream, content string) {
	if c.Logger == nil || content == "" {
		return
	}
	_ = c.Logger.AppendClassifierLog(runID, stream, content)
}

// extractJSONObject finds the first top-level {...} object in s, tolerant of markdown
// code fences or leading/trailing prose the model might add despite instructions.
func extractJSONObject(s string) string {
	start := -1
	depth := 0
	for i, r := range s {
		switch r {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start != -1 {
					return s[start : i+1]
				}
			}
		}
	}
	return ""
}

package translate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// logName keys the translation call's logs: logs/translate.{stdout,stderr}.log.
const logName = "translate"

const heartbeatInterval = 30 * time.Second

// Length bounds of a translation relative to the original, in characters.
const (
	minLengthRatio = 0.3
	maxLengthRatio = 3.0
)

// CallObserver mirrors action.CallObserver structurally, so the console reporter that
// watches workflow steps also shows the translation call.
type CallObserver interface {
	ProviderCallStarted(providerID, model string)
	ProviderCallHeartbeat(elapsed time.Duration)
	ProviderCallFinished(providerID, model string, duration time.Duration, err error)
}

// Logger persists the raw output of the translation call. Satisfied by
// *persistence.Store.
type Logger interface {
	AppendCallLog(runID, name, stream, content string) error
	CallLogPath(runID, name, stream string) string
}

// Result is what the rest of the flow and the decision trace need to know about the
// request's language. Text is what goes to the decision provider and to relevant-file
// search: the translation, or Original when none was made or it failed. Stage prompts
// always get Original.
type Result struct {
	Original   string        `json:"-"`
	Text       string        `json:"-"`
	Detected   Detection     `json:"detected"`
	Translated bool          `json:"translated"`
	Failed     bool          `json:"failed"`
	Reason     string        `json:"reason,omitempty"`
	Provider   string        `json:"provider,omitempty"`
	Model      string        `json:"model,omitempty"`
	Latency    time.Duration `json:"latency,omitempty"`
	// Usage is what the translation call cost, when the CLI reported it (also for a
	// translation that failed its check); the decision trace records it.
	Usage *domain.Usage `json:"-"`
}

// Translator translates via one provider CLI call (translation.provider/model).
type Translator struct {
	Client     provider.Client
	ProviderID string
	Model      string
	// Timeout bounds the call; it runs inside the run-wide execution.timeout.
	Timeout time.Duration
	Logger  Logger
	// Observer and Stream mirror action.Dispatcher's: progress on the console and, with
	// --verbose, the CLI's live output. Nil means no-op.
	Observer CallObserver
	Stream   io.Writer
}

// Prepare returns the request as it should be sent to the decision provider. A failed
// translation is not an error - the original goes out with Failed set and a Reason, and
// the decision provider copes with Russian, only worse (TASKS.md В12). The only errors
// are the run itself ending: Ctrl-C or execution.timeout.
func (t *Translator) Prepare(ctx context.Context, runID, request string) (Result, error) {
	res := Result{Original: request, Text: request, Detected: Detect(request)}
	if !res.Detected.NeedsTranslate {
		return res, nil
	}
	res.Provider, res.Model = t.ProviderID, t.Model

	start := time.Now()
	out, err := t.call(ctx, runID, request, &res)
	res.Latency = time.Since(start)
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if err == nil {
		out, err = check(request, out)
	}
	if err != nil {
		res.Failed = true
		res.Reason = firstLine(err.Error())
		return res, nil
	}
	res.Text = out
	res.Translated = true
	return res, nil
}

// call runs the CLI and records the call's usage on res.
func (t *Translator) call(ctx context.Context, runID, request string, res *Result) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	if t.Observer != nil {
		t.Observer.ProviderCallStarted(t.ProviderID, t.Model)
	}
	start := time.Now()
	stopHeartbeat := t.startHeartbeat()
	// Translation reads nothing but the request text in its prompt, so it gets no tools.
	result, err := t.Client.Execute(callCtx, provider.Request{
		Prompt: BuildPrompt(request), Model: t.Model, Stream: t.Stream, Authority: domain.AuthorityNone,
		Activity: provider.ActivityHook(t.Observer),
	})
	stopHeartbeat()
	provider.ReportUsage(t.Observer, result)
	res.Usage = result.Usage
	if t.Observer != nil {
		t.Observer.ProviderCallFinished(t.ProviderID, t.Model, time.Since(start), err)
	}

	wroteStdout := t.log(runID, "stdout", result.Output)
	wroteStderr := t.log(runID, "stderr", result.Stderr)

	if err != nil {
		var providerErr *apperr.ProviderError
		if errors.As(err, &providerErr) {
			if providerErr.Action == "" {
				providerErr.Action = logName
			}
			// Point only at a log that exists (MVP.md §23).
			if providerErr.LogPath == "" {
				switch {
				case wroteStderr:
					providerErr.LogPath = t.Logger.CallLogPath(runID, logName, "stderr")
				case wroteStdout:
					providerErr.LogPath = t.Logger.CallLogPath(runID, logName, "stdout")
				}
			}
			if providerErr.Kind == apperr.FailureTimeout || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
				return "", fmt.Errorf("translation timed out after %s", t.Timeout)
			}
			if providerErr.LogPath == "" {
				return "", fmt.Errorf("translation failed: %s (no output)", providerErr.Kind)
			}
			return "", fmt.Errorf("translation failed: %s (see %s)", providerErr.Kind, providerErr.LogPath)
		}
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("translation timed out after %s", t.Timeout)
		}
		return "", fmt.Errorf("translation failed: %w", err)
	}
	return result.Output, nil
}

var (
	// identifierRe: CamelCase with an inner capital, or snake_case.
	identifierRe = regexp.MustCompile(`\b(?:[A-Za-z][a-z0-9]*[A-Z][A-Za-z0-9]*|[A-Za-z0-9]+_[A-Za-z0-9_]+)\b`)
	// pathRe: slash-separated paths (internal/config/loader.go) and file names with an
	// extension (README.md).
	pathRe = regexp.MustCompile(`[\w.-]+(?:/[\w.-]+)+|\b[\w-]+\.[A-Za-z][A-Za-z0-9]{0,4}\b`)
)

// check is a sanity check, not a quality check: it catches an empty answer, one that
// answered the request instead of translating it (length), one still in Russian, and one
// that "translated" code - every identifier and path of the original must survive
// verbatim, since relevant-file search depends on them.
func check(original, raw string) (string, error) {
	out := cleanOutput(raw)
	if out == "" {
		return "", errors.New("translation is empty")
	}
	origLen, outLen := utf8.RuneCountInString(original), utf8.RuneCountInString(out)
	if ratio := float64(outLen) / float64(origLen); ratio < minLengthRatio || ratio > maxLengthRatio {
		return "", fmt.Errorf("translation length is %.1fx the original, want %.1fx-%.1fx", ratio, minLengthRatio, maxLengthRatio)
	}
	if share := cyrillicShare(out); share >= maxTranslatedCyrillic {
		return "", fmt.Errorf("translation is still %.0f%% Cyrillic", share*100)
	}
	for _, token := range preservedTokens(original) {
		if !strings.Contains(out, token) {
			return "", fmt.Errorf("translation lost %q", token)
		}
	}
	return out, nil
}

// preservedTokens returns the identifiers and paths that must appear unchanged in a
// translation. All-caps words (API, HTTP) are not identifiers: a translator may
// legitimately rephrase around them.
func preservedTokens(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{identifierRe, pathRe} {
		for _, m := range re.FindAllString(text, -1) {
			if strings.ToUpper(m) == m || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func (t *Translator) startHeartbeat() func() {
	if t.Observer == nil {
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
				t.Observer.ProviderCallHeartbeat(time.Since(start))
			}
		}
	}()
	return func() { close(done) }
}

// log is best-effort, like the classifier's: losing a log never fails the run.
// It reports whether the log was written.
func (t *Translator) log(runID, stream, content string) bool {
	if t.Logger == nil || content == "" {
		return false
	}
	return t.Logger.AppendCallLog(runID, logName, stream, content) == nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

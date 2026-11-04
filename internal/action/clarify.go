package action

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
)

// The clarify step's prompt and answer (specs/clarify, Approach §3, Contracts: Clarify answer).
// Like the translator it is a call outside a run's steps: no tools, so "do not read the code" is
// enforced by the authority (domain.AuthorityNone), not asked of the model. What it knows of the
// repository is the instructions Go chose for it (projectctx.Docs), which come in fenced.

// Bounds of a clarify answer; the prompt states the same numbers and ParseClarify enforces them.
const (
	maxClarifyPoints     = domain.MaxClarifyPoints
	maxClarifyFieldBytes = domain.MaxClarifyFieldBytes
)

const clarifyIntro = `Find what is unclear in the following software engineering task, and settle what the repository's own ` +
	`instructions settle. You have no tools and cannot read the code: all you know of the repository is the rules and ` +
	`documentation excerpts given after the task. Respond with a single JSON object, no markdown fences, no prose before ` +
	`or after it, matching exactly this shape:`

const clarifyShape = "{\n  \"points\": [\n    {\"unclear\": \"...\", \"question\": \"...\", \"answer\": \"...\", " +
	"\"basis\": \"docs\", \"source\": \"...\"}\n  ],\n  \"summary_short\": \"...\"\n}"

// clarifyPointRules say what a point is and what its basis means; ParseClarify enforces them.
var clarifyPointRules = fmt.Sprintf(`A point is one thing the task leaves open that matters for doing it: the goal, the part of the `+
	`system it concerns, the expected behaviour or result, or a choice the request leaves open. "unclear" says what is `+
	`missing in a few words and "question" is the question that would settle it. Give at most %d points, the most important `+
	`first, and none when nothing essential is missing. Every field is at most %d bytes. "basis" is exactly one of: docs, `+
	`assumption, none. "docs": the excerpts below settle it, "answer" is what they say and "source" names the excerpt as it `+
	`is shown, its path, a "#" and its section heading. "assumption": nothing settles it and "answer" is the assumption you `+
	`propose. "none": nothing settles it and you propose nothing, leave "answer" out. "source" belongs to "docs" alone. `+
	`Write the points in the language of the task.`, maxClarifyPoints, maxClarifyFieldBytes)

const clarifyInteractiveRule = `An operator will read each point and answer it, so ask what only they can know. ` +
	`"assumption" is not allowed in this mode: use "none" for what the excerpts do not settle.`

const clarifyAutoRule = `No operator is available. What the excerpts settle, answer with "docs"; what they do not, ` +
	`give an "assumption" you can defend, or "none" when no assumption is sensible.`

// clarifyNoInstructions replaces the rules and excerpts when Go found none.
const clarifyNoInstructions = `The repository has no rules or documentation excerpts for this task: nothing can be answered from docs.`

// ClarifyPromptInput is what the clarify prompt is built from. Clarification is the rendered
// clarification so far (domain.Clarification.Render), "" when there is none.
type ClarifyPromptInput struct {
	Task          string
	Instructions  domain.Instructions
	Clarification string
	Mode          domain.ClarifyMode
}

// BuildClarifyPrompt is the prompt of the clarify call: the task through taskBlock, then the rules,
// each excerpt with its source and the clarification so far through fence, each its own block.
func BuildClarifyPrompt(in ClarifyPromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: clarify\n\n")
	b.WriteString(clarifyIntro + "\n\n")
	b.WriteString(clarifyShape + "\n\n")
	b.WriteString(clarifyPointRules + "\n\n")
	if in.Mode == domain.ClarifyInteractive {
		b.WriteString(clarifyInteractiveRule + "\n\n")
	} else {
		b.WriteString(clarifyAutoRule + "\n\n")
	}
	b.WriteString("Task:\n" + taskBlock(in.Task))

	if rules := clarifyRules(in.Instructions.Rules); rules != "" {
		b.WriteString("\nRules of the repository, the bullets of its instruction files:\n")
		b.WriteString(fence("rules", "the repository's rules, one per line", rules))
	}
	for i, e := range in.Instructions.Excerpts {
		if i == 0 {
			b.WriteString("\nDocumentation excerpts, each with its source:\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString(fence("excerpt", "an excerpt of the repository's documentation", "source: "+ExcerptSource(e)+"\n"+strings.TrimSpace(e.Text)))
	}
	if len(in.Instructions.Rules) == 0 && len(in.Instructions.Excerpts) == 0 {
		b.WriteString("\n" + clarifyNoInstructions + "\n")
	}
	if strings.TrimSpace(in.Clarification) != "" {
		b.WriteString("\nClarification so far:\n")
		b.WriteString(fence("clarification", "the clarification agreed so far", in.Clarification))
	}
	return b.String()
}

// ExcerptSource is how an excerpt is named to the model and in a point's source: its path, a "#"
// and its section heading (the path alone for text before the first heading).
func ExcerptSource(e domain.DocExcerpt) string {
	if strings.TrimSpace(e.Section) == "" {
		return oneLine(e.Path)
	}
	return oneLine(e.Path) + "#" + oneLine(e.Section)
}

// clarifyRules is the rules as one "- rule" line each, the blank ones left out.
func clarifyRules(rules []string) string {
	var b strings.Builder
	for _, r := range rules {
		if r = oneLine(r); r != "" {
			b.WriteString("- " + r + "\n")
		}
	}
	return b.String()
}

// ClarifyResult is what a clarify call gave, as far as it got: the prompt as sent, the raw output
// (also when it is refused, for the run's logs) and the usage (nil when the CLI reported none or
// the call did not run).
type ClarifyResult struct {
	Answer ClarifyAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

// Clarify makes the one call of the clarify step: the prompt of in on call's route with
// domain.AuthorityNone, passed explicitly like the translator's and never through AuthorityFor, so
// the model has no tools. n (from 1) numbers its logs, logs/clarify-<n>.*. Any failure, a
// credential problem included, is the caller's to turn into a failed run: an unclear task never
// goes on unclarified. An answer that holds no JSON object or breaks a rule is an
// *apperr.InvalidResponseError naming the field; the raw output is in the result either way.
func Clarify(ctx context.Context, call PlanningCall, in ClarifyPromptInput, n int) (ClarifyResult, error) {
	res := ClarifyResult{Prompt: BuildClarifyPrompt(in)}
	kind := planningKind{action: "clarify", log: fmt.Sprintf("clarify-%d", n), timeout: "clarify.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{Prompt: res.Prompt, Authority: domain.AuthorityNone})
	if err != nil {
		return res, err
	}
	res.Answer, err = ParseClarify(res.Output, in.Mode)
	return res, err
}

// ClarifyBasis says what a point's answer rests on.
type ClarifyBasis string

const (
	// ClarifyBasisDocs: the excerpts say so; the point has a source.
	ClarifyBasisDocs ClarifyBasis = "docs"
	// ClarifyBasisAssumption: no excerpt says so and the model proposes it; auto mode only.
	ClarifyBasisAssumption ClarifyBasis = "assumption"
	// ClarifyBasisNone: nothing settles the point; it is left to the operator, or stays open.
	ClarifyBasisNone ClarifyBasis = "none"
)

// ClarifyAnswerPoint is one point of the clarify answer, trimmed and cut to the field cap.
type ClarifyAnswerPoint struct {
	Unclear  string
	Question string
	// Answer is empty for basis none.
	Answer string
	Basis  ClarifyBasis
	// Source is the excerpt a docs answer rests on, as ExcerptSource names it; it may be empty.
	Source string
}

// ClarifyAnswer is the parsed answer of the clarify call. No points is an answer: the re-check
// still runs and Go decides.
type ClarifyAnswer struct {
	Points       []ClarifyAnswerPoint
	SummaryShort string
}

type clarifyWire struct {
	Points       []clarifyWirePoint `json:"points"`
	SummaryShort string             `json:"summary_short"`
}

type clarifyWirePoint struct {
	Unclear  string `json:"unclear"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Basis    string `json:"basis"`
	Source   string `json:"source"`
}

// ParseClarify decodes the clarify call's output strictly: a JSON object with no other key, every
// point with a basis of docs, assumption or none, a source only on a docs point, and no
// assumption in interactive mode. Anything else, prose without JSON included, is an
// *apperr.InvalidResponseError naming the field. At most maxClarifyPoints points are kept and each
// field is cut to maxClarifyFieldBytes, on a rune boundary, so the artifact holds what the prompts
// used.
func ParseClarify(output string, mode domain.ClarifyMode) (ClarifyAnswer, error) {
	if !mode.Valid() {
		return ClarifyAnswer{}, fmt.Errorf("clarify: unknown mode %q", mode)
	}
	obj := extractJSONObject(output)
	if obj == "" {
		return ClarifyAnswer{}, clarifyInvalid("", nil, "the answer holds no JSON object")
	}
	var wire clarifyWire
	dec := json.NewDecoder(strings.NewReader(obj))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			if unquoted, uerr := strconv.Unquote(name); uerr == nil {
				name = unquoted
			}
			return ClarifyAnswer{}, clarifyInvalid(name, err, "not a field of the answer")
		}
		return ClarifyAnswer{}, clarifyInvalid("", err, "%s", err.Error())
	}

	answer := ClarifyAnswer{SummaryShort: cutRunes(oneLine(wire.SummaryShort), maxClarifyFieldBytes)}
	for i, p := range wire.Points {
		if i == maxClarifyPoints {
			break
		}
		point, err := clarifyPoint(fmt.Sprintf("points[%d]", i), p, mode)
		if err != nil {
			return ClarifyAnswer{}, err
		}
		answer.Points = append(answer.Points, point)
	}
	return answer, nil
}

func clarifyPoint(at string, p clarifyWirePoint, mode domain.ClarifyMode) (ClarifyAnswerPoint, error) {
	basis := ClarifyBasis(strings.TrimSpace(p.Basis))
	switch basis {
	case ClarifyBasisDocs, ClarifyBasisNone:
	case ClarifyBasisAssumption:
		if mode == domain.ClarifyInteractive {
			return ClarifyAnswerPoint{}, clarifyInvalid(at+".basis", nil, "assumption is not allowed in interactive mode")
		}
	default:
		return ClarifyAnswerPoint{}, clarifyInvalid(at+".basis", nil, "%q is not one of docs, assumption, none", p.Basis)
	}
	point := ClarifyAnswerPoint{
		Unclear:  cutField(p.Unclear),
		Question: cutField(p.Question),
		Answer:   cutField(p.Answer),
		Basis:    basis,
		Source:   cutField(p.Source),
	}
	switch {
	case point.Unclear == "":
		return ClarifyAnswerPoint{}, clarifyInvalid(at+".unclear", nil, "is empty")
	case point.Question == "":
		return ClarifyAnswerPoint{}, clarifyInvalid(at+".question", nil, "is empty")
	case point.Source != "" && basis != ClarifyBasisDocs:
		return ClarifyAnswerPoint{}, clarifyInvalid(at+".source", nil, "only a point with basis docs has a source")
	case point.Answer == "" && basis != ClarifyBasisNone:
		return ClarifyAnswerPoint{}, clarifyInvalid(at+".answer", nil, "is empty on a point with basis %s", basis)
	}
	if basis == ClarifyBasisNone {
		point.Answer = ""
	}
	return point, nil
}

// cutField trims s and cuts it to maxClarifyFieldBytes without splitting a character.
func cutField(s string) string {
	return strings.TrimSpace(cutRunes(strings.TrimSpace(s), maxClarifyFieldBytes))
}

func clarifyInvalid(field string, err error, format string, args ...any) error {
	return &apperr.InvalidResponseError{Call: "clarify", Field: field, Msg: fmt.Sprintf(format, args...), Err: err}
}

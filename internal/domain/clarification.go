package domain

import (
	"fmt"
	"math"
	"strings"
)

// ClarifyMode is how the clarify step asked its points: of an operator at a terminal, or of
// nobody (specs/clarify, Approach §3).
type ClarifyMode string

const (
	ClarifyInteractive ClarifyMode = "interactive"
	ClarifyAuto        ClarifyMode = "auto"
)

// Valid reports whether m is a mode a clarify round may record.
func (m ClarifyMode) Valid() bool { return m == ClarifyInteractive || m == ClarifyAuto }

// ClarifyRoundKind is what a round of a clarification is.
type ClarifyRoundKind string

const (
	// ClarifyRoundClarify is the clarify step: the model's points and who answered them.
	ClarifyRoundClarify ClarifyRoundKind = "clarify"
	// ClarifyRoundAddition is text the operator added to the task after a re-check stayed below
	// the threshold.
	ClarifyRoundAddition ClarifyRoundKind = "addition"
)

// ClarifyBy is who answered a point of the clarify step.
type ClarifyBy string

const (
	ClarifyByOperator   ClarifyBy = "operator"
	ClarifyByDocs       ClarifyBy = "docs"
	ClarifyByAssumption ClarifyBy = "assumption"
)

// Valid reports whether b is a closed-set value; the empty value (a point left open) is not.
func (b ClarifyBy) Valid() bool {
	return b == ClarifyByOperator || b == ClarifyByDocs || b == ClarifyByAssumption
}

// Bounds of a Clarification, enforced when run.json is read back. A clarify answer is cut to
// the same numbers before it gets here.
const (
	// MaxClarifyPoints is the most points one clarify round holds.
	MaxClarifyPoints = 8
	// MaxClarifyFieldBytes is the longest unclear, question, answer and source of a point.
	MaxClarifyFieldBytes = 300
	// MaxClarifyRounds is the clarify round and at most clarify.max_rounds (5) additions.
	MaxClarifyRounds = 6
	// MaxClarifyAdditionBytes is the longest text of an addition.
	MaxClarifyAdditionBytes = 4096
)

// ClarifyPoint is one thing the clarify step found unclear: what is unclear, the question that
// settles it, and the answer with who gave it. By and Answer are empty while the point is open;
// Source names the documentation excerpt of a docs answer.
type ClarifyPoint struct {
	Unclear  string    `json:"unclear"`
	Question string    `json:"question"`
	Answer   string    `json:"answer,omitempty"`
	By       ClarifyBy `json:"by,omitempty"`
	Source   string    `json:"source,omitempty"`
}

// ClarifyRound is one round of a clarification: the clarify step (Mode and Points) or an
// operator's addition (Text).
type ClarifyRound struct {
	Kind   ClarifyRoundKind `json:"kind"`
	Mode   ClarifyMode      `json:"mode,omitempty"`
	Points []ClarifyPoint   `json:"points,omitempty"`
	Text   string           `json:"text,omitempty"`
}

// Clarification is run.json: task.clarification (specs/clarify, Contracts): what was agreed
// about an unclear task before the decision. Checks holds P(clear) of the pre-check and then of
// every re-check, so it is one longer than Rounds; Threshold is policy.clarify.threshold and
// Status the outcome of the last check.
type Clarification struct {
	Threshold float64        `json:"threshold"`
	Checks    []float64      `json:"checks"`
	Status    ClarityStatus  `json:"status"`
	Rounds    []ClarifyRound `json:"rounds"`
}

// Validate checks a Clarification read back from run.json: probabilities in [0, 1], one more
// check than rounds, the first round of kind clarify, the closed sets and the byte caps.
func (c *Clarification) Validate() error {
	if c == nil {
		return nil
	}
	if math.IsNaN(c.Threshold) || c.Threshold <= 0 || c.Threshold > 1 {
		return fmt.Errorf("threshold %v outside (0, 1]", c.Threshold)
	}
	for i, p := range c.Checks {
		if err := validateUnit(fmt.Sprintf("checks[%d]", i), p); err != nil {
			return err
		}
	}
	if len(c.Checks) != 1+len(c.Rounds) {
		return fmt.Errorf("%d checks for %d rounds, want one more check than rounds", len(c.Checks), len(c.Rounds))
	}
	if c.Status != ClarityClear && c.Status != ClarityUnclear {
		return fmt.Errorf("unknown status %q, want %q or %q", c.Status, ClarityClear, ClarityUnclear)
	}
	if n := len(c.Rounds); n < 1 || n > MaxClarifyRounds {
		return fmt.Errorf("%d rounds, want 1 to %d", n, MaxClarifyRounds)
	}
	if c.Rounds[0].Kind != ClarifyRoundClarify {
		return fmt.Errorf("rounds[0] has kind %q, want %q", c.Rounds[0].Kind, ClarifyRoundClarify)
	}
	for i, r := range c.Rounds {
		if err := r.validate(); err != nil {
			return fmt.Errorf("rounds[%d]: %w", i, err)
		}
	}
	return nil
}

func (r ClarifyRound) validate() error {
	switch r.Kind {
	case ClarifyRoundClarify:
		if !r.Mode.Valid() {
			return fmt.Errorf("unknown mode %q", r.Mode)
		}
		if r.Text != "" {
			return fmt.Errorf("a %s round has no text", r.Kind)
		}
		if len(r.Points) > MaxClarifyPoints {
			return fmt.Errorf("%d points, at most %d", len(r.Points), MaxClarifyPoints)
		}
		for i, p := range r.Points {
			if err := p.validate(r.Mode); err != nil {
				return fmt.Errorf("points[%d]: %w", i, err)
			}
		}
	case ClarifyRoundAddition:
		if r.Mode != "" || len(r.Points) > 0 {
			return fmt.Errorf("an %s round has only text", r.Kind)
		}
		if strings.TrimSpace(r.Text) == "" {
			return fmt.Errorf("an %s round has no text", r.Kind)
		}
		if len(r.Text) > MaxClarifyAdditionBytes {
			return fmt.Errorf("text is %d bytes, at most %d", len(r.Text), MaxClarifyAdditionBytes)
		}
	default:
		return fmt.Errorf("unknown kind %q", r.Kind)
	}
	return nil
}

func (p ClarifyPoint) validate(mode ClarifyMode) error {
	for _, f := range []struct{ name, value string }{
		{"unclear", p.Unclear}, {"question", p.Question}, {"answer", p.Answer}, {"source", p.Source},
	} {
		if len(f.value) > MaxClarifyFieldBytes {
			return fmt.Errorf("%s is %d bytes, at most %d", f.name, len(f.value), MaxClarifyFieldBytes)
		}
	}
	if strings.TrimSpace(p.Unclear) == "" || strings.TrimSpace(p.Question) == "" {
		return fmt.Errorf("unclear and question are required")
	}
	switch {
	case p.By == "":
		if p.Answer != "" || p.Source != "" {
			return fmt.Errorf("an open point has no answer and no source")
		}
	case !p.By.Valid():
		return fmt.Errorf("unknown by %q, want %q, %q or %q", p.By, ClarifyByOperator, ClarifyByDocs, ClarifyByAssumption)
	case strings.TrimSpace(p.Answer) == "":
		return fmt.Errorf("a point answered by %s has no answer", p.By)
	case p.By == ClarifyByAssumption && mode == ClarifyInteractive:
		return fmt.Errorf("an interactive round has no assumptions")
	case p.Source != "" && p.By != ClarifyByDocs:
		return fmt.Errorf("source is only for a point answered by %s", ClarifyByDocs)
	}
	return nil
}

// Open returns the points no round has answered: what the run still lacks.
func (c *Clarification) Open() []ClarifyPoint {
	if c == nil {
		return nil
	}
	var open []ClarifyPoint
	for _, r := range c.Rounds {
		for _, p := range r.Points {
			if p.By == "" {
				open = append(open, p)
			}
		}
	}
	return open
}

// Render is the clarification as text, the one rendering the stage prompts, the re-check's
// state and summary.md share (specs/clarify, Contracts). A nil clarification, and one with no
// point and no addition, render as "".
func (c *Clarification) Render() string {
	if c == nil {
		return ""
	}
	var lines, added []string
	for _, r := range c.Rounds {
		for _, p := range r.Points {
			lines = append(lines, p.render())
		}
		if r.Kind == ClarifyRoundAddition && strings.TrimSpace(r.Text) != "" {
			added = append(added, r.Text)
		}
	}
	if len(lines) == 0 && len(added) == 0 {
		return ""
	}
	out := "Clarification (agreed before the run; the task above stays the source):"
	for _, l := range lines {
		out += "\n" + l
	}
	if len(added) > 0 {
		out += "\nAdded by the operator:"
		for i, text := range added {
			if i > 0 {
				out += "\n"
			}
			for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")), "\n") {
				if l = strings.TrimRight(l, " \t"); l == "" {
					out += "\n"
				} else {
					out += "\n  " + l
				}
			}
		}
	}
	return out
}

func (p ClarifyPoint) render() string {
	question, answer := oneLine(p.Question), oneLine(p.Answer)
	switch p.By {
	case ClarifyByDocs:
		if p.Source != "" {
			return fmt.Sprintf("- %s %s  [from %s]", question, answer, oneLine(p.Source))
		}
		return fmt.Sprintf("- %s %s  [docs]", question, answer)
	case ClarifyByOperator:
		return fmt.Sprintf("- %s %s  [operator]", question, answer)
	case ClarifyByAssumption:
		return fmt.Sprintf("- Assumed: %s  [assumption]", answer)
	}
	return "- Open: " + question
}

// oneLine collapses s to a single line, so model text in a list stays one item.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"orchestrator/internal/domain"
	"orchestrator/internal/tui"
)

// The review screen: the decision as a keyboard form instead of a typed prompt. The
// operator can toggle stages, override the complexity (which re-applies the policy's
// forced stages) and pick a button - Run, Plan only, Cancel - and, when no --mode was
// given, the execution mode, all on one screen. It is drawn only on an interactive
// terminal; everywhere else the caller asks with line prompts as before.

// ReviewInput is what the screen starts from.
type ReviewInput struct {
	Decision domain.Decision
	Policy   domain.DecisionPolicy
	// AskMode shows the mode selector (no --mode given); DefaultMode is preselected.
	AskMode     bool
	DefaultMode domain.RunMode
	// Editable allows changing stages and complexity (a fresh run; a resume only approves).
	Editable bool
	// Build turns the stages into the workflow line ("analyze → implement"); it reports
	// the steps so the screen can preview exactly what will run.
	Build func(domain.StageSelection) ([]domain.ActionType, error)
}

// ReviewResult is the operator's answer.
type ReviewResult struct {
	Approved bool
	Mode     domain.RunMode // "" when AskMode was false
	Decision domain.Decision
}

// ErrReviewUnavailable means the screen cannot run here; ask with line prompts instead.
var ErrReviewUnavailable = errors.New("interactive review not available")

type reviewFocus int

const (
	focusStages reviewFocus = iota
	focusComplexity
	focusMode
	focusButtons
)

type reviewState struct {
	in         ReviewInput
	stages     []domain.StageOutcome
	on         map[domain.Stage]bool
	complexity domain.Complexity
	modes      []domain.RunMode
	mode       int
	focus      reviewFocus
	row        int // stage row under the cursor
	button     int // 0 Run, 1 Cancel
	message    string
}

var reviewButtons = []string{"Run", "Cancel"}

// modeHints say in a few words what each mode does.
var modeHints = map[domain.RunMode]string{
	domain.ModePlan:        "decide, don't execute",
	domain.ModeManual:      "confirm every step",
	domain.ModeAcceptEdits: "approve once, then hands-off",
	domain.ModeAuto:        "no questions",
}

// Review runs the review screen on in/out. It returns ErrReviewUnavailable when in is not
// a terminal it can drive, so the caller falls back to line prompts.
func (r *ConsoleReporter) Review(in io.Reader, input ReviewInput) (ReviewResult, error) {
	f, ok := in.(*os.File)
	if !r.Pretty || !ok || !tui.Supported(f) {
		return ReviewResult{}, ErrReviewUnavailable
	}
	r.stopSpin()
	if r.dock != nil {
		r.dock.hide()
		defer r.dock.show()
	}
	out := r.Out
	if r.rawOut != nil {
		out = r.rawOut
	}
	s, err := tui.Open(f, out)
	if err != nil {
		return ReviewResult{}, ErrReviewUnavailable
	}
	defer s.Close()

	st := r.newReviewState(input)
	for {
		s.Draw(st.render(r.st()))
		ev, err := s.Read()
		if err != nil {
			s.Erase()
			return ReviewResult{}, err
		}
		done, res, err := st.handle(ev)
		if err != nil {
			s.Erase()
			return ReviewResult{}, err
		}
		if done {
			s.Erase()
			r.printReviewAnswer(st, res)
			return res, nil
		}
	}
}

func (r *ConsoleReporter) newReviewState(in ReviewInput) *reviewState {
	st := &reviewState{in: in, stages: in.Decision.Stages.Stages, on: map[domain.Stage]bool{},
		complexity: in.Decision.Complexity.Selected}
	for _, so := range st.stages {
		st.on[so.Stage] = so.Enabled
	}
	if in.AskMode {
		st.modes = []domain.RunMode{domain.ModeAcceptEdits, domain.ModeManual, domain.ModeAuto, domain.ModePlan}
		for i, m := range st.modes {
			if m == in.DefaultMode {
				st.mode = i
			}
		}
	}
	if !in.Editable {
		st.focus = focusButtons
		if in.AskMode {
			st.focus = focusMode
		}
	}
	return st
}

func (st *reviewState) focusOrder() []reviewFocus {
	var f []reviewFocus
	if st.in.Editable {
		f = append(f, focusStages)
		if st.complexity != "" {
			f = append(f, focusComplexity)
		}
	}
	if st.in.AskMode {
		f = append(f, focusMode)
	}
	return append(f, focusButtons)
}

func (st *reviewState) moveFocus(delta int) {
	order := st.focusOrder()
	for i, f := range order {
		if f == st.focus {
			st.focus = order[(i+delta+len(order))%len(order)]
			return
		}
	}
	st.focus = order[0]
}

func (st *reviewState) enabledStages() []domain.Stage {
	var out []domain.Stage
	for _, so := range st.stages {
		if st.on[so.Stage] {
			out = append(out, so.Stage)
		}
	}
	return out
}

func (st *reviewState) forced() map[domain.Stage]bool {
	m := map[domain.Stage]bool{}
	for _, s := range st.in.Policy.ForceStages[st.complexity] {
		m[s] = true
	}
	return m
}

// setComplexity switches the complexity and turns on the stages policy forces for it -
// what the policy would have done had it picked this complexity itself.
func (st *reviewState) setComplexity(c domain.Complexity) {
	if c == st.complexity {
		return
	}
	st.complexity = c
	for _, s := range st.in.Policy.ForceStages[c] {
		st.on[s] = true
	}
}

func (st *reviewState) decision() domain.Decision {
	return st.in.Decision.WithEdit(st.complexity, st.enabledStages(), st.in.Policy.ForceStages[st.complexity])
}

func (st *reviewState) handle(ev tui.Event) (bool, ReviewResult, error) {
	st.message = ""
	order := domain.ComplexityOrder()
	switch ev.Key {
	case tui.KeyCtrlC:
		return false, ReviewResult{}, tui.ErrInterrupted
	case tui.KeyEsc:
		return true, ReviewResult{Approved: false}, nil
	case tui.KeyTab:
		st.moveFocus(1)
		return false, ReviewResult{}, nil
	case tui.KeyBackTab:
		st.moveFocus(-1)
		return false, ReviewResult{}, nil
	}

	// Digits toggle a stage from anywhere. There are no letter shortcuts: a letter that
	// cancels in one place and picks an option in another is how runs get cancelled by
	// accident. Enter runs, Esc cancels, arrows and Tab move.
	if ev.Key == tui.KeyRune && st.in.Editable && ev.Rune >= '1' && ev.Rune <= '9' {
		if i := int(ev.Rune - '1'); i < len(st.stages) {
			st.focus, st.row = focusStages, i
			st.toggle(i)
		}
		return false, ReviewResult{}, nil
	}

	switch st.focus {
	case focusStages:
		switch ev.Key {
		case tui.KeyUp:
			if st.row > 0 {
				st.row--
			}
		case tui.KeyDown:
			if st.row < len(st.stages)-1 {
				st.row++
			} else {
				st.moveFocus(1)
			}
		case tui.KeySpace:
			st.toggle(st.row)
		case tui.KeyEnter:
			return st.submit()
		}
	case focusComplexity:
		idx := 0
		for i, c := range order {
			if c == st.complexity {
				idx = i
			}
		}
		switch ev.Key {
		case tui.KeyLeft:
			st.setComplexity(order[max(idx-1, 0)])
		case tui.KeyRight, tui.KeySpace:
			st.setComplexity(order[min(idx+1, len(order)-1)])
		case tui.KeyUp:
			st.moveFocus(-1)
			st.row = len(st.stages) - 1
		case tui.KeyDown:
			st.moveFocus(1)
		case tui.KeyEnter:
			return st.submit()
		}
	case focusMode:
		switch ev.Key {
		case tui.KeyLeft:
			st.mode = (st.mode - 1 + len(st.modes)) % len(st.modes)
		case tui.KeyRight, tui.KeySpace:
			st.mode = (st.mode + 1) % len(st.modes)
		case tui.KeyUp:
			st.moveFocus(-1)
		case tui.KeyDown:
			st.moveFocus(1)
		case tui.KeyEnter:
			return st.submit()
		}
	case focusButtons:
		switch ev.Key {
		case tui.KeyLeft:
			st.button = max(st.button-1, 0)
		case tui.KeyRight:
			st.button = min(st.button+1, len(reviewButtons)-1)
		case tui.KeyUp:
			st.moveFocus(-1)
		case tui.KeyEnter, tui.KeySpace:
			if st.button == 1 {
				return true, ReviewResult{Approved: false}, nil
			}
			return st.submit()
		}
	}
	return false, ReviewResult{}, nil
}

func (st *reviewState) toggle(i int) {
	s := st.stages[i].Stage
	st.on[s] = !st.on[s]
	if !st.on[s] && st.forced()[s] {
		st.message = fmt.Sprintf("%s is forced for complexity=%s; running without it overrides the policy", s, st.complexity)
	}
}

func (st *reviewState) submit() (bool, ReviewResult, error) {
	if len(st.enabledStages()) == 0 {
		st.message = "select at least one stage"
		return false, ReviewResult{}, nil
	}
	res := ReviewResult{Approved: true, Decision: st.decision()}
	if st.in.AskMode {
		res.Mode = st.modes[st.mode]
	}
	return true, res, nil
}

// render draws the form. Every line fits one terminal row.
func (st *reviewState) render(s style) []string {
	width := termWidth() - 1
	var lines []string
	add := func(l string) {
		if visibleWidth(l) > width {
			l = truncateVisible(ansiRE.ReplaceAllString(l, ""), width)
		}
		lines = append(lines, l)
	}
	forced := st.forced()
	cursor := func(on bool) string {
		if on {
			return s.accent("❯")
		}
		return " "
	}

	if st.in.Editable {
		add(s.gray("  stages") + s.gray("   space toggles · 1-6 jump"))
		for i, so := range st.stages {
			focused := st.focus == focusStages && st.row == i
			box := s.gray("[ ]")
			if st.on[so.Stage] {
				box = s.green("[✓]")
			}
			name := fmt.Sprintf("%-12s", so.Stage)
			switch {
			case focused:
				name = s.bold(name)
			case st.on[so.Stage]:
				name = s.bold(name)
			default:
				name = s.gray(name)
			}
			prob := "    "
			if so.Probability != nil {
				prob = fmt.Sprintf("%.2f", *so.Probability)
			}
			note := ""
			switch {
			case forced[so.Stage] && st.on[so.Stage]:
				note = s.gray("forced")
			case forced[so.Stage]:
				note = s.yellow("forced · overridden")
			case so.Explored && st.on[so.Stage]:
				note = s.gray("explored")
			case so.Explored:
				note = s.yellow("explored · off")
			case so.Enabled != st.on[so.Stage]:
				note = s.yellow("changed")
			}
			add(fmt.Sprintf("  %s %s %s %s %s  %s", cursor(focused), box, name, miniBar(s, so), s.gray(prob), note))
		}

		if st.complexity != "" {
			var opts []string
			for _, c := range domain.ComplexityOrder() {
				opts = append(opts, chip(s, string(c), c == st.complexity, st.focus == focusComplexity))
			}
			note := ""
			if st.complexity != st.in.Decision.Complexity.Selected {
				note = " " + s.yellow("was "+string(st.in.Decision.Complexity.Selected))
			}
			add("")
			add(fmt.Sprintf("  %s %s %s%s", cursor(st.focus == focusComplexity), s.gray("complexity"), strings.Join(opts, " "), note))
		}
	}

	if st.in.AskMode {
		var opts []string
		for i, m := range st.modes {
			opts = append(opts, chip(s, string(m), i == st.mode, st.focus == focusMode))
		}
		add(fmt.Sprintf("  %s %s %s  %s", cursor(st.focus == focusMode), s.gray("mode      "), strings.Join(opts, " "),
			s.gray(modeHints[st.modes[st.mode]])))
	}

	// Preview of exactly what will run.
	add("")
	if st.in.Build != nil {
		sel := st.decision().Stages
		steps, err := st.in.Build(sel)
		if err != nil || len(steps) == 0 {
			add("    " + s.gray("workflow  ") + s.red("nothing selected"))
		} else {
			names := make([]string, len(steps))
			for i, a := range steps {
				names[i] = s.cyan(string(a))
			}
			add("    " + s.gray("workflow  ") + strings.Join(names, s.gray(" → ")))
		}
	}
	if st.message != "" {
		add("    " + s.yellow("⚠ "+st.message))
	}

	// Buttons.
	add("")
	var btns []string
	for i, b := range reviewButtons {
		label := b
		if i == 0 && st.in.AskMode && st.modes[st.mode] == domain.ModePlan {
			label = "Plan only"
		}
		btns = append(btns, button(s, label, i == st.button && st.focus == focusButtons, i == 0))
	}
	add("  " + strings.Join(btns, "  ") + "   " + s.gray("enter run · esc cancel · tab next"))
	return lines
}

// chip is one option of a selector: highlighted when selected, brighter when focused.
func chip(s style, label string, selected, focused bool) string {
	switch {
	case selected && focused:
		return s.sgr("1;30;48;5;209", " "+label+" ")
	case selected:
		return s.sgr("1;38;5;209", "‹"+label+"›")
	default:
		return s.gray(" " + label + " ")
	}
}

// button is a pill: filled when focused, outlined otherwise; primary is accent-coloured.
func button(s style, label string, focused, primary bool) string {
	switch {
	case focused && primary:
		return s.sgr("1;30;48;5;209", "  "+label+"  ")
	case focused:
		return s.sgr("1;30;47", "  "+label+"  ")
	case primary:
		return s.accent("[ " + label + " ]")
	default:
		return s.gray("[ " + label + " ]")
	}
}

// miniBar is the stage's probability as a short bar with its threshold marked.
func miniBar(s style, so domain.StageOutcome) string {
	const w = 10
	if so.Probability == nil {
		return s.gray(strings.Repeat("·", w))
	}
	filled := int(*so.Probability*w + 0.5)
	th := -1
	if so.Threshold != nil {
		th = min(int(*so.Threshold*w+0.5), w-1)
	}
	var b strings.Builder
	for i := 0; i < w; i++ {
		switch {
		case i == th:
			b.WriteString(s.gray("│"))
		case i < filled:
			b.WriteString(s.cyan("█"))
		default:
			b.WriteString(s.gray("░"))
		}
	}
	return b.String()
}

// printReviewAnswer leaves one line in the transcript saying what was chosen, in place of
// the form.
func (r *ConsoleReporter) printReviewAnswer(st *reviewState, res ReviewResult) {
	s := r.st()
	if !res.Approved {
		fmt.Fprintf(r.Out, "%s %s\n", s.red("✗"), s.gray("cancelled"))
		return
	}
	steps := st.enabledStages()
	names := make([]string, len(steps))
	for i, x := range steps {
		names[i] = string(x)
	}
	line := s.green("✓ Approved")
	if res.Decision.Edit != nil {
		line += " " + s.yellow("(edited)")
	}
	meta := []string{strings.Join(names, " → ")}
	if res.Decision.Complexity.Selected != "" {
		meta = append(meta, "complexity "+string(res.Decision.Complexity.Selected))
	}
	if res.Mode != "" {
		meta = append(meta, "mode "+string(res.Mode))
	}
	fmt.Fprintf(r.Out, "%s %s\n", line, s.gray(strings.Join(meta, " · ")))
}

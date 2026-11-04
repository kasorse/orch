package interaction

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

func clarifyPoints() []domain.ClarifyPoint {
	return []domain.ClarifyPoint{
		{Unclear: "format", Question: "Which export format is meant?", Answer: "CSV", By: domain.ClarifyByDocs, Source: "docs/export.md#Formats"},
		{Unclear: "users", Question: "Which users get the button?"},
		{Unclear: "locale", Question: "Which locale is the date in?"},
	}
}

func TestClarifier_ShowsThePointsAndAppliesTheAnswers(t *testing.T) {
	var out bytes.Buffer
	// Enter keeps the docs answer, text replaces, "-" leaves open.
	c := NewClarifier(&fakePrompter{answers: []string{"", "admins only", "-"}}, &out)

	got, err := c.Answer(context.Background(), 0.42, 0.80, clarifyPoints())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].By != domain.ClarifyByDocs || got[0].Answer != "CSV" || got[0].Source != "docs/export.md#Formats" {
		t.Errorf("kept point = %+v, want the docs answer", got[0])
	}
	if got[1].By != domain.ClarifyByOperator || got[1].Answer != "admins only" || got[1].Source != "" {
		t.Errorf("replaced point = %+v, want the operator's answer", got[1])
	}
	if got[2].By != "" || got[2].Answer != "" {
		t.Errorf("point left open = %+v", got[2])
	}
	for _, want := range []string{
		"The task is not clear enough (P(clear) 0.42 < 0.80). 3 questions:",
		"1. Which export format is meant?",
		"from docs/export.md#Formats: CSV",
		"2. Which users get the button?",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("screen lacks %q:\n%s", want, out.String())
		}
	}
}

func TestClarifier_EnterAndDashOnAPointWithoutAnAnswerLeaveItOpen(t *testing.T) {
	c := NewClarifier(&fakePrompter{answers: []string{"", "-"}}, &bytes.Buffer{})
	points := clarifyPoints()[1:]

	got, err := c.Answer(context.Background(), 0.1, 0.8, points)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range got {
		if p.By != "" || p.Answer != "" {
			t.Errorf("point %d = %+v, want it open", i, p)
		}
	}
}

func TestClarifier_TheOperatorsAnswerIsCutToTheFieldCap(t *testing.T) {
	long := strings.Repeat("я", domain.MaxClarifyFieldBytes) // two bytes each
	c := NewClarifier(&fakePrompter{answers: []string{long}}, &bytes.Buffer{})

	got, err := c.Answer(context.Background(), 0.1, 0.8, clarifyPoints()[1:2])
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got[0].Answer); n > domain.MaxClarifyFieldBytes || n < domain.MaxClarifyFieldBytes-1 {
		t.Errorf("answer is %d bytes, want it cut to at most %d on a character boundary", n, domain.MaxClarifyFieldBytes)
	}
}

func TestClarifier_AdditionEndsAtAnEmptyLine(t *testing.T) {
	var out bytes.Buffer
	p := &fakePrompter{answers: []string{"the dates are ISO 8601", "and in UTC", "", "never read"}}
	c := NewClarifier(p, &out)

	text, err := c.Add(context.Background(), 0.55, 0.80, clarifyPoints()[1:])
	if err != nil {
		t.Fatal(err)
	}
	if text != "the dates are ISO 8601\nand in UTC" || p.idx != 3 {
		t.Errorf("addition %q after %d reads, want both lines and the empty one consumed", text, p.idx)
	}
	for _, want := range []string{"still rated unclear (P(clear) 0.55 < 0.80)", "Open points:", "- Which locale is the date in?"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("screen lacks %q:\n%s", want, out.String())
		}
	}
}

func TestClarifier_EmptyAdditionIsEmpty(t *testing.T) {
	c := NewClarifier(&fakePrompter{answers: []string{""}}, &bytes.Buffer{})
	text, err := c.Add(context.Background(), 0.5, 0.8, nil)
	if err != nil || text != "" {
		t.Errorf("addition %q, err %v; want none", text, err)
	}
}

// EOF and a cancelled context end the run as a cancellation, never as an answer.
func TestClarifier_EOFAndCancellationCancel(t *testing.T) {
	c := NewClarifier(&fakePrompter{}, &bytes.Buffer{})
	if _, err := c.Answer(context.Background(), 0.1, 0.8, clarifyPoints()); !errors.Is(err, context.Canceled) {
		t.Errorf("Answer at EOF: %v, want a cancellation", err)
	}
	if _, err := c.Add(context.Background(), 0.1, 0.8, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Add at EOF: %v, want a cancellation", err)
	}
	// An addition that was begun and then hit EOF is cancelled too.
	c = NewClarifier(&fakePrompter{answers: []string{"half a thought"}}, &bytes.Buffer{})
	if _, err := c.Add(context.Background(), 0.1, 0.8, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Add cut by EOF: %v, want a cancellation", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &fakePrompter{answers: []string{"x"}}
	if _, err := NewClarifier(p, &bytes.Buffer{}).Answer(ctx, 0.1, 0.8, clarifyPoints()); !errors.Is(err, context.Canceled) || p.idx != 0 {
		t.Errorf("Answer on a cancelled context: %v after %d reads, want a cancellation before reading", err, p.idx)
	}
}

// blockingPrompter is an operator who never answers: Ask waits until release is closed.
type blockingPrompter struct {
	asked   chan struct{}
	release chan struct{}
}

func (b *blockingPrompter) Ask(string) (string, error) {
	close(b.asked)
	<-b.release
	return "too late", nil
}

// Ctrl-C while a question waits for its line cancels at once, without the operator pressing Enter.
func TestClarifier_CancellationDuringAPendingReadCancels(t *testing.T) {
	for name, call := range map[string]func(*Clarifier, context.Context) error{
		"answer": func(c *Clarifier, ctx context.Context) error {
			_, err := c.Answer(ctx, 0.1, 0.8, clarifyPoints())
			return err
		},
		"addition": func(c *Clarifier, ctx context.Context) error {
			_, err := c.Add(ctx, 0.1, 0.8, nil)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := &blockingPrompter{asked: make(chan struct{}), release: make(chan struct{})}
			defer close(p.release)
			ctx, cancel := context.WithCancel(context.Background())
			errc := make(chan error, 1)
			go func() { errc <- call(NewClarifier(p, &bytes.Buffer{}), ctx) }()

			<-p.asked
			cancel()
			select {
			case err := <-errc:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("err = %v, want a cancellation", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the pending read was not cancelled")
			}
		})
	}
}

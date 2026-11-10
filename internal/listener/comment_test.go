package listener

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// testEscape stands in for jira.Escape: the package cannot import it.
func testEscape(s string) string {
	return strings.NewReplacer("[", `\[`, "]", `\]`, "{", `\{`, "}", `\}`).Replace(s)
}

func TestComposeStartsWithTheMarkerAndEndsWithTheRun(t *testing.T) {
	got := Compose([]string{"one", "two"}, testRunID)
	lines := strings.Split(got, "\n")
	if lines[0] != Marker || lines[len(lines)-1] != "Run "+testRunID || len(lines) != 4 {
		t.Errorf("comment = %q", got)
	}
	if got := Compose([]string{"one"}, ""); got != Marker+"\none" {
		t.Errorf("comment without a run = %q", got)
	}
}

func TestComposeBoundsTheCommentAtACharacterBoundary(t *testing.T) {
	body := []string{strings.Repeat("é", 10000)}
	got := Compose(body, testRunID)
	if len(got) > MaxCommentBytes {
		t.Errorf("comment is %d bytes, want at most %d", len(got), MaxCommentBytes)
	}
	if !utf8.ValidString(got) || !strings.HasPrefix(got, Marker+"\n") || !strings.HasSuffix(got, "\nRun "+testRunID) || !strings.Contains(got, shortened) {
		t.Errorf("comment = %q…", got[:80])
	}
}

func TestNeedsInputNumbersTheOpenPointsAndEscapesThem(t *testing.T) {
	body := NeedsInput(testEscape, []string{"Which {code} to use?", "Where [here]?"}, nil, "To Do")
	text := strings.Join(body, "\n")
	for _, want := range []string{"# Which \\{code\\} to use?", "# Where \\[here\\]?", "Answer in a comment and move the ticket back to To Do."} {
		if !strings.Contains(text, want) {
			t.Errorf("comment lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Assumed") {
		t.Errorf("comment without assumptions lists some:\n%s", text)
	}
}

func TestNeedsInputListsTheAssumptionsToConfirm(t *testing.T) {
	assumed := []Assumption{{Question: "What key format?", Answer: "Assume {id}-<code>, e.g. 38-5-HTP."}}
	text := strings.Join(NeedsInput(testEscape, nil, assumed, "To Do"), "\n")
	for _, want := range []string{
		"No single point was left open",
		"Assumed, confirm or correct:\n# What key format? Assumed: Assume \\{id\\}-<code>, e.g. 38-5-HTP.",
		"Answer and correct the assumptions in a comment and move the ticket back to To Do.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("comment lacks %q:\n%s", want, text)
		}
	}
}

func TestFailureCarriesTheReasonAndTheLastFailure(t *testing.T) {
	text := strings.Join(Failure(testEscape, "maximum fix iterations exceeded", "go test ./... failed (exit 1)"), "\n")
	if !strings.Contains(text, "maximum fix iterations exceeded") || !strings.Contains(text, "Last failure: go test") {
		t.Errorf("comment = %q", text)
	}
	if text := strings.Join(Failure(testEscape, "", ""), "\n"); !strings.Contains(text, "without a merge request") {
		t.Errorf("comment without a reason = %q", text)
	}
}

func TestSpentNamesTheKeyAndTheFile(t *testing.T) {
	text := strings.Join(Spent(testEscape, "PAY-12", 3), "\n")
	if !strings.Contains(text, "3 attempts spent") || !strings.Contains(text, "PAY-12") || !strings.Contains(text, ".orchestrator/listener/state.json") {
		t.Errorf("comment = %q", text)
	}
}

func TestAModelCannotMakeAnotherMarkerLine(t *testing.T) {
	body := Blocked(testEscape, "[orchestrator] hello")
	if strings.HasPrefix(body[0], "[") {
		t.Errorf("line = %q, want the bracket escaped", body[0])
	}
}

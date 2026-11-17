package listener

import (
	"strings"
	"testing"
)

func TestRequirementsCommentWithQuestionsGolden(t *testing.T) {
	golden(t, "requirements_questions.golden", RequirementsComment(testEscape, "PMC-1", []string{"product-master", "pim-dictionaries"}, Requirements{
		Summary:   "Add a {categories} dictionary\n to sku-master and show it in the editor.",
		Decided:   []string{"The import runs once.", "Core publishes on save (found in sku-master/publish.go)."},
		Questions: []string{"Which countries come first?", "Is [the old list] kept?"},
		BackTo:    "To Do",
	}))
}

func TestRequirementsCommentWithoutQuestionsGolden(t *testing.T) {
	golden(t, "requirements.golden", RequirementsComment(testEscape, "PMC-1", nil, Requirements{
		Summary: "Add a categories dictionary to sku-master.",
		BackTo:  "To Do",
	}))
}

// A summary that would start a heading or a numbered list in wiki markup does not.
func TestRequirementsCommentSummaryIsNeverMarkup(t *testing.T) {
	for in, want := range map[string]string{
		"# Categories": `\# Categories`,
		"h1. Title":    "​h1. Title",
		"bq. quoted":   "​bq. quoted",
	} {
		got := RequirementsComment(testEscape, "PMC-1", nil, Requirements{Summary: in})
		if got[1] != want {
			t.Errorf("summary %q = %q, want %q", in, got[1], want)
		}
	}
}

// The longest summary and fifteen of the longest questions fit a comment whole. More questions are cut
// by Compose: the next plan asks again what was not answered.
func TestRequirementsCommentLongAnswerFits(t *testing.T) {
	a := Requirements{Summary: strings.Repeat("s", 1200), BackTo: "To Do"}
	for i := 0; i < 15; i++ {
		a.Questions = append(a.Questions, "question "+string(rune('a'+i))+" "+strings.Repeat("q", 388))
	}
	text := ComposeFor(testEscape, RequirementsComment(testEscape, "PMC-1", []string{"product-master"}, a), "")
	if len(text) > MaxCommentBytes || strings.Contains(text, shortened) || strings.Contains(text, "…") {
		t.Fatalf("the comment is %d bytes (limit %d) or was cut:\n%s", len(text), MaxCommentBytes, text)
	}
}

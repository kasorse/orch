package listener

import (
	"strings"
	"testing"
)

func TestRequirementsCommentWithQuestionsGolden(t *testing.T) {
	golden(t, "requirements_questions.golden", RequirementsComment(testEscape, "PMC-1", []string{"product-master", "pim-dictionaries"}, Requirements{
		Summary: "Add a categories dictionary\n to sku-master and show it in the editor.",
		Requirements: []string{
			"The dictionary holds the categories of product-master, by country.",
			"The editor shows the {category} of each SKU.",
		},
		Questions: []string{"Which countries come first?", "Is [the old list] kept?"},
		BackTo:    "To Do",
	}))
}

func TestRequirementsCommentWithoutQuestionsGolden(t *testing.T) {
	golden(t, "requirements.golden", RequirementsComment(testEscape, "PMC-1", nil, Requirements{
		Summary:      "Add a categories dictionary to sku-master.",
		Requirements: []string{"The dictionary holds the categories of product-master, by country.", "The editor shows the category of each SKU."},
		BackTo:       "To Do",
	}))
}

// A summary that would start a heading or a numbered list in wiki markup does not.
func TestRequirementsCommentSummaryIsNeverMarkup(t *testing.T) {
	for in, want := range map[string]string{
		"# Categories": `\# Categories`,
		"h1. Title":    "​h1. Title",
		"bq. quoted":   "​bq. quoted",
	} {
		got := RequirementsComment(testEscape, "PMC-1", nil, Requirements{Summary: in, Requirements: []string{"r"}})
		if got[1] != want {
			t.Errorf("summary %q = %q, want %q", in, got[1], want)
		}
	}
}

// The longest answer fits a comment: the requirements are cut from the end, and every question stays.
func TestRequirementsCommentCutsTheRequirementsAndKeepsTheQuestions(t *testing.T) {
	a := Requirements{Summary: strings.Repeat("s", 4000), BackTo: "To Do"}
	for i := 0; i < 30; i++ {
		a.Requirements = append(a.Requirements, strings.Repeat("r", 599)+string(rune('a'+i%26)))
	}
	for i := 0; i < 10; i++ {
		a.Questions = append(a.Questions, "question "+string(rune('0'+i))+" "+strings.Repeat("q", 580))
	}
	body := RequirementsComment(testEscape, "PMC-1", []string{"product-master"}, a)
	text := ComposeFor(testEscape, body, "")
	if len(text) > MaxCommentBytes || strings.Contains(text, shortened) {
		t.Fatalf("the comment is %d bytes (limit %d), shortened = %v", len(text), MaxCommentBytes, strings.Contains(text, shortened))
	}
	if !strings.Contains(text, "\n* …\n") {
		t.Errorf("the cut is not marked:\n%s", text)
	}
	for i := 0; i < 10; i++ {
		if !strings.Contains(text, "# question "+string(rune('0'+i))+" ") {
			t.Errorf("question %d is missing", i)
		}
	}
}

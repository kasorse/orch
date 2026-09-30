package domain

import "testing"

// ticketKeyCases is the ValidTicketKey table. internal/jira's ParseRef tests (J03) cover
// the same keys, which ParseRef upper-cases first.
var ticketKeyCases = []struct {
	key  string
	want bool
}{
	{"PMC-2789", true},
	{"AB-1", true},
	{"A1_B-10", true},
	{"PMC2-7", true},
	{"pmc-2789", false}, // keys are upper-cased before they are checked
	{"P-1", false},      // a project key has at least two characters
	{"1PM-1", false},
	{"_PM-1", false},
	{"PMC-0", false},
	{"PMC-01", false},
	{"PMC-", false},
	{"PMC", false},
	{"-1", false},
	{"PMC-1-2", false},
	{"PMC_1", false},
	{"PMC-1 ", false},
	{" PMC-1", false},
	{"PMC-1\n", false},
	{"../x", false},
	{"PMC-1/../x", false},
	{"", false},
}

func TestValidTicketKey(t *testing.T) {
	for _, tc := range ticketKeyCases {
		if got := ValidTicketKey(tc.key); got != tc.want {
			t.Errorf("ValidTicketKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

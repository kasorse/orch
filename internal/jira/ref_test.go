package jira

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
)

func TestParseRef(t *testing.T) {
	const cloud = "https://h.atlassian.net"
	const dc = "https://h/jira"
	cases := []struct {
		name, ref, base string
		want            string // "" means an error is expected
		errPart         string
	}{
		// Keys: the same cases as domain's ValidTicketKey table, upper-cased first.
		{"key", "PMC-1", cloud, "PMC-1", ""},
		{"lower case and spaces", " pmc-1 ", cloud, "PMC-1", ""},
		{"underscore and digit", "A_B2-10", cloud, "A_B2-10", ""},
		{"long number", "pmc-2789", cloud, "PMC-2789", ""},
		{"zero", "pmc-0", cloud, "", `--jira "pmc-0" is not an issue key`},
		{"leading zero", "PMC-01", cloud, "", "is not an issue key"},
		{"no number", "PMC", cloud, "", "is not an issue key"},
		{"digit first", "1PMC-1", cloud, "", "is not an issue key"},
		{"one letter project", "P-1", cloud, "", "is not an issue key"},
		{"path", "../x", cloud, "", "is not an issue key"},
		{"empty", "", cloud, "", "is not an issue key"},

		// URLs on the configured instance.
		{"browse", cloud + "/browse/PMC-1", cloud, "PMC-1", ""},
		{"browse lower-case key", cloud + "/browse/pmc-1", cloud, "PMC-1", ""},
		{"trailing slash", cloud + "/browse/PMC-1/", cloud, "PMC-1", ""},
		{"query and fragment", cloud + "/browse/PMC-1?focusedCommentId=5#c", cloud, "PMC-1", ""},
		{"explicit default port", "https://h.atlassian.net:443/browse/PMC-1", cloud, "PMC-1", ""},
		{"host case", "https://H.Atlassian.NET/browse/PMC-1", cloud, "PMC-1", ""},
		{"base with trailing slash", cloud + "/browse/PMC-1", cloud + "/", "PMC-1", ""},
		{"dc context path", "https://h/jira/browse/PMC-1", dc, "PMC-1", ""},
		{"loopback", "http://127.0.0.1:8080/browse/PMC-1", "http://127.0.0.1:8080", "PMC-1", ""},

		// URLs that do not name one issue on this instance.
		{"dc without context path", "https://h/browse/PMC-1", dc, "", "is not an issue link"},
		{"other host", "https://x.example/browse/PMC-1", cloud, "", `--jira URL host "x.example" does not match jira.base_url "h.atlassian.net"`},
		{"other host and scheme", "http://x.example/browse/PMC-1", cloud, "", `--jira URL host "x.example" does not match`},
		{"other scheme", "http://h.atlassian.net/browse/PMC-1", cloud, "", `--jira URL scheme "http" does not match jira.base_url's "https"`},
		{"other port", "https://h.atlassian.net:8443/browse/PMC-1", cloud, "", `--jira URL host "h.atlassian.net:8443" does not match`},
		{"board", cloud + "/jira/software/projects/PMC/boards/1?selectedIssue=PMC-1", cloud, "", "is not an issue link"},
		{"jql", cloud + "/issues/?jql=project%3DPMC", cloud, "", "is not an issue link"},
		{"browse project", cloud + "/browse/PMC", cloud, "", "is not an issue link"},
		{"browse sub-path", cloud + "/browse/PMC-1/extra", cloud, "", "is not an issue link"},
		{"encoded slash", cloud + "/browse/PMC-1%2F..%2Fx", cloud, "", "is not an issue link"},
		{"credentials", "https://user:pw@h.atlassian.net/browse/PMC-1", cloud, "", "must not contain credentials"},
		{"no host", "https:///browse/PMC-1", cloud, "", "is not a valid URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRef(tc.ref, tc.base)
			if tc.want != "" {
				if err != nil || got != tc.want {
					t.Fatalf("ParseRef(%q) = %q, %v; want %q", tc.ref, got, err, tc.want)
				}
				return
			}
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) {
				t.Fatalf("ParseRef(%q) = %q, %v; want a ConfigError", tc.ref, got, err)
			}
			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("error %q lacks %q", err, tc.errPart)
			}
			if strings.Contains(err.Error(), "pw") {
				t.Errorf("error shows the URL's password: %q", err)
			}
		})
	}
}

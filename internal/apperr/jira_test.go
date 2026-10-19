package apperr

import (
	"errors"
	"testing"
)

func TestJiraFailureKind_Transient(t *testing.T) {
	cases := map[JiraFailureKind]bool{
		JiraTimeout: true, JiraConnection: true, JiraRateLimited: true, JiraUnavailable: true,
		JiraAuth: false, JiraNotFound: false, JiraRedirect: false, JiraInvalidResponse: false, JiraCancelled: false,
		JiraRejected: false,
	}
	for kind, want := range cases {
		if got := kind.Transient(); got != want {
			t.Errorf("%s.Transient() = %v, want %v", kind, got, want)
		}
	}
}

func TestJiraError_Message(t *testing.T) {
	cause := errors.New("connection refused")
	cases := []struct {
		name string
		err  *JiraError
		want string
	}{
		{"auth", &JiraError{Key: "PMC-1", Kind: JiraAuth, StatusCode: 401, Attempts: 1, Detail: "JIRA_EMAIL/JIRA_API_TOKEN"},
			"Jira rejected the credentials in JIRA_EMAIL/JIRA_API_TOKEN (HTTP 401)"},
		{"not found", &JiraError{Key: "PMC-2789", Kind: JiraNotFound, StatusCode: 404, Detail: "JIRA_EMAIL"},
			"PMC-2789 does not exist, or the account behind JIRA_EMAIL cannot see it (HTTP 404)"},
		{"redirect", &JiraError{Key: "PMC-1", Kind: JiraRedirect, StatusCode: 302, Detail: "login.example"},
			"Jira answered with a redirect to login.example; check jira.base_url (HTTP 302)"},
		{"unavailable", &JiraError{Key: "PMC-1", Kind: JiraUnavailable, StatusCode: 503, Attempts: 2},
			"Jira is unavailable (HTTP 503 after 2 attempts)"},
		{"timeout", &JiraError{Key: "PMC-1", Kind: JiraTimeout, Detail: "10s"}, "Jira did not answer within 10s"},
		{"connection", &JiraError{Key: "PMC-1", Kind: JiraConnection, Attempts: 3, Err: cause},
			"Jira could not be reached (after 3 attempts): connection refused"},
		{"invalid", &JiraError{Key: "PMC-1", Kind: JiraInvalidResponse, StatusCode: 200,
			Detail: "fields.customfield_10035: not a text field; remove it from jira.fields"},
			"Jira answered PMC-1 with an unexpected response: fields.customfield_10035: not a text field; remove it from jira.fields (HTTP 200)"},
		{"cancelled", &JiraError{Key: "PMC-1", Kind: JiraCancelled}, "fetching PMC-1 from Jira was cancelled"},
		{"create rejected", &JiraError{Key: "PAY-1", Op: JiraOpCreate, Kind: JiraRejected, StatusCode: 400, Detail: "components: Component name 'x' is not valid"},
			"Jira rejected the child issue of PAY-1: components: Component name 'x' is not valid (HTTP 400)"},
		{"create invalid", &JiraError{Key: "PAY-1", Op: JiraOpCreate, Kind: JiraInvalidResponse, StatusCode: 201, Detail: "no key"},
			"Jira answered the creation of a child of PAY-1 with an unexpected response: no key (HTTP 201)"},
		{"create cancelled", &JiraError{Key: "PAY-1", Op: JiraOpCreate, Kind: JiraCancelled},
			"creating a child issue of PAY-1 was cancelled"},
		{"create timeout", &JiraError{Key: "PAY-1", Op: JiraOpCreate, Kind: JiraTimeout, Detail: "10s"}, "Jira did not answer within 10s"},
		{"body", &JiraError{Key: "PMC-1", Kind: JiraUnavailable, StatusCode: 500, Body: "oops"},
			"Jira is unavailable (HTTP 500)\n\nresponse body:\noops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestJiraError_Unwrap(t *testing.T) {
	cause := errors.New("x")
	if !errors.Is(&JiraError{Kind: JiraConnection, Err: cause}, cause) {
		t.Error("expected JiraError to unwrap to its cause")
	}
}

package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

const blocksType = `"type":{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"}`

func linksField(links ...string) string {
	return `,"issuelinks":[` + strings.Join(links, ",") + `]`
}

func TestSearch_LinksCloud(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"isLast":true,"issues":[`+searchIssue("PAY-5", linksField(
		`{"id":"1",`+blocksType+`,"inwardIssue":{"id":"7","key":"PAY-3","self":"x","fields":{"summary":"B","status":{"name":"Code Review","id":"3"},"issuetype":{"name":"Task"}}}}`,
		`{"id":"2",`+blocksType+`,"outwardIssue":{"id":"8","key":"PAY-9","fields":{"status":{"name":"To Do"}}}}`,
		`{"id":"3","type":{"name":"Relates","inward":"relates to","outward":"relates to"},"inwardIssue":{"key":"PAY-4","fields":{"status":{"name":"Done"}}}}`,
	))+`]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Search(context.Background(), "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.TicketRef{
		{Key: "PAY-3", Status: "Code Review", LinkType: "Blocks"},
		{Key: "PAY-4", Status: "Done", LinkType: "Relates"},
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].BlockedBy, want) {
		t.Errorf("BlockedBy = %+v, want %+v", got, want)
	}
}

func TestSearch_LinksDataCenter(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"startAt":0,"total":2,"issues":[`+
		searchIssue("PAY-5", linksField(
			`{"id":"1",`+blocksType+`,"inwardIssue":{"id":"7","key":"PAY-3","self":"x","fields":{"status":{"name":"Done"}}}}`))+`,`+
		searchIssue("PAY-6", linksField())+`]}`))
	c, _ := testClient(bearerOptions(srv.URL), NewCredentials(AuthBearer, "", testToken))
	got, err := c.Search(context.Background(), "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.TicketRef{{Key: "PAY-3", Status: "Done", LinkType: "Blocks"}}; !reflect.DeepEqual(got[0].BlockedBy, want) {
		t.Errorf("BlockedBy = %+v, want %+v", got[0].BlockedBy, want)
	}
	if got[1].BlockedBy != nil {
		t.Errorf("an empty issuelinks gave %+v", got[1].BlockedBy)
	}
}

func TestSearch_LinkWithoutStatusIsKept(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"isLast":true,"issues":[`+searchIssue("PAY-5", linksField(
		`{`+blocksType+`,"inwardIssue":{"key":"PAY-3"}}`,
		`{`+blocksType+`,"inwardIssue":{"key":"PAY-4","fields":{}}}`,
	))+`]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Search(context.Background(), "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.TicketRef{{Key: "PAY-3", LinkType: "Blocks"}, {Key: "PAY-4", LinkType: "Blocks"}}
	if !reflect.DeepEqual(got[0].BlockedBy, want) {
		t.Errorf("BlockedBy = %+v, want %+v", got[0].BlockedBy, want)
	}
}

func TestSearch_LinksNotUsableAreInvalid(t *testing.T) {
	for name, field := range map[string]string{
		"not an array": `,"issuelinks":{}`,
		"bad key":      linksField(`{` + blocksType + `,"inwardIssue":{"key":"nope"}}`),
		"no key":       linksField(`{` + blocksType + `,"inwardIssue":{}}`),
		"bad status":   linksField(`{` + blocksType + `,"inwardIssue":{"key":"PAY-3","fields":{"status":"Done"}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, `{"isLast":true,"issues":[`+searchIssue("PAY-5", field)+`]}`))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Search(context.Background(), "jql", 10)
			if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraInvalidResponse || !strings.Contains(jerr.Detail, "issuelinks") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestLink_Request(t *testing.T) {
	var log bodyLog
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		b, _ := io.ReadAll(r.Body)
		log.add(b)
		w.WriteHeader(http.StatusCreated)
	})
	c, _ := testClient(testOptions(srv.URL+"/jira"), basicCreds())
	if err := c.Link(context.Background(), "Blocks", "PAY-13", "PAY-12"); err != nil {
		t.Fatal(err)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	if r := reqs[0]; r.Method != http.MethodPost || r.URL.Path != "/jira/rest/api/2/issueLink" || r.URL.RawQuery != "" ||
		r.Header.Get("Authorization") != "Basic "+testBasic || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(log.all()[0], &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"type": {"name": "Blocks"}, "inwardIssue": {"key": "PAY-13"}, "outwardIssue": {"key": "PAY-12"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
}

func TestLink_RejectedIsNotMaybeWritten(t *testing.T) {
	srv := newServer(t, answer(http.StatusBadRequest, `{"errorMessages":["No issue link type with name 'Nope' found."],"errors":{}}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	err := c.Link(context.Background(), "Nope", "PAY-13", "PAY-12")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraRejected || jerr.Op != apperr.JiraOpLink || !strings.Contains(err.Error(), "No issue link type") {
		t.Errorf("err = %v (%+v)", err, jerr)
	}
	if MayExist(err) {
		t.Error("a 400 left a link that may exist")
	}
	if n := len(srv.requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestLink_ServerErrorAfterSendingIsNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := newServer(t, answer(status, ""))
			c, sleeps := testClient(testOptions(srv.URL), basicCreds())
			err := c.Link(context.Background(), "Blocks", "PAY-13", "PAY-12")
			jerr := asJiraError(t, err)
			if jerr.Op != apperr.JiraOpLink || jerr.StatusCode != status {
				t.Errorf("err = %v", err)
			}
			if !MayExist(err) {
				t.Error("a 5xx after sending must leave the link as possibly written")
			}
			if n := len(srv.requests()); n != 1 || len(*sleeps) != 0 {
				t.Errorf("%d requests, %d sleeps; want 1 and 0", n, len(*sleeps))
			}
		})
	}
}

func TestLink_RetriesOnlyAnAnswerThatWroteNothing(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	if err := c.Link(context.Background(), "Blocks", "PAY-13", "PAY-12"); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.requests()); n != 2 {
		t.Errorf("%d requests, want 2", n)
	}
}

func TestLink_RefusesWhatItWouldNotSend(t *testing.T) {
	srv := newServer(t, func(http.ResponseWriter, *http.Request, int) { t.Error("a request was sent") })
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	for _, tc := range []struct{ typ, in, out string }{
		{"", "PAY-13", "PAY-12"}, {"Blocks", "pay-13", "PAY-12"}, {"Blocks", "PAY-13", "x"}, {"Blocks", "PAY-1", "PAY-1"},
		{"Bl\nocks", "PAY-13", "PAY-12"},
	} {
		err := c.Link(context.Background(), tc.typ, tc.in, tc.out)
		if jerr := asJiraError(t, err); !jerr.NotSent || MayExist(err) {
			t.Errorf("%+v: err = %v", tc, err)
		}
	}
}

func TestChildren_ParentLink(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"isLast":true,"issues":[`+searchIssue("PAY-20", "")+`,`+searchIssue("PAY-21", "")+`]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	keys, err := c.Children(context.Background(), "PAY-1", "", "acc-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PAY-20", "PAY-21"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	if jql, want := reqs[0].URL.Query().Get("jql"), `parent = PAY-1 AND reporter = "acc-1"`; jql != want {
		t.Errorf("jql = %q, want %q", jql, want)
	}
}

func TestChildren_CustomFieldLink(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"isLast":true,"issues":[]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	keys, err := c.Children(context.Background(), "PAY-1", "customfield_10014", "jdoe")
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys = %v, err = %v", keys, err)
	}
	if jql, want := srv.requests()[0].URL.Query().Get("jql"), `cf[10014] = PAY-1 AND reporter = "jdoe"`; jql != want {
		t.Errorf("jql = %q, want %q", jql, want)
	}
}

func TestChildren_RefusesWhatItWouldNotAsk(t *testing.T) {
	srv := newServer(t, func(http.ResponseWriter, *http.Request, int) { t.Error("a request was sent") })
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	for _, tc := range []struct{ parent, field, reporter string }{
		{"pay-1", "", "acc"}, {"PAY-1", "", ""}, {"PAY-1", "", `a" OR 1=1 --`}, {"PAY-1", "10014", "acc"}, {"PAY-1", "customfield_1) OR (1", "acc"},
	} {
		if _, err := c.Children(context.Background(), tc.parent, tc.field, tc.reporter); err == nil {
			t.Errorf("%+v: no error", tc)
		}
	}
}

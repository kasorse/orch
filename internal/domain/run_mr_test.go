package domain

import (
	"strings"
	"testing"
)

func TestRunMRValidate(t *testing.T) {
	created := func() RunMR {
		return RunMR{State: MRCreated, Remote: "origin", Source: "orchestrator/r1", Target: "main", IID: 12,
			URL: "https://gitlab.com/group/sub/proj/-/merge_requests/12"}
	}
	cases := []struct {
		name   string
		mutate func(*RunMR)
		want   string // "" = valid
	}{
		{"created", func(m *RunMR) {}, ""},
		{"created on a port", func(m *RunMR) { m.URL = "https://gitlab.example.com:8443/g/p/-/merge_requests/12" }, ""},
		{"intent with an error", func(m *RunMR) {
			*m = RunMR{State: MRIntent, Remote: "origin", Source: "b", Target: "main", Error: "fatal: nope"}
		}, ""},
		{"pushed, declined", func(m *RunMR) {
			*m = RunMR{State: MRPushed, Remote: "origin", Source: "b", Target: "main", Skipped: "declined at merge request"}
		}, ""},
		{"skipped alone", func(m *RunMR) { *m = RunMR{Skipped: "declined at commit"} }, ""},
		{"error alone", func(m *RunMR) { *m = RunMR{Error: "the branch exists on origin"} }, ""},
		{"empty", func(m *RunMR) { *m = RunMR{} }, "skipped or error"},
		{"empty with names", func(m *RunMR) { *m = RunMR{Remote: "origin"} }, "skipped or error"},
		{"unknown state", func(m *RunMR) { m.State = "merged" }, "state"},
		{"no remote", func(m *RunMR) { m.Remote = "" }, "remote, source and target"},
		{"no source", func(m *RunMR) { m.Source = "" }, "remote, source and target"},
		{"no target", func(m *RunMR) { m.Target = "" }, "remote, source and target"},
		{"remote as an option", func(m *RunMR) { m.Remote = "-v" }, "remote"},
		{"source with a space", func(m *RunMR) { m.Source = "a b" }, "source"},
		{"url without created", func(m *RunMR) { m.State = MRPushed }, "only go with state created"},
		{"iid without created", func(m *RunMR) {
			*m = RunMR{State: MRPushed, Remote: "o", Source: "b", Target: "t", IID: 3}
		}, "only go with state created"},
		{"created without a url", func(m *RunMR) { m.URL = "" }, "needs iid and url"},
		{"created without an iid", func(m *RunMR) { m.IID = 0 }, "needs iid and url"},
		{"created with an error", func(m *RunMR) { m.Error = "x" }, "neither skipped nor error"},
		{"http", func(m *RunMR) { m.URL = "http://gitlab.com/g/p/-/merge_requests/12" }, "https"},
		{"userinfo", func(m *RunMR) { m.URL = "https://u:p@gitlab.com/g/p/-/merge_requests/12" }, "https"},
		{"query", func(m *RunMR) { m.URL = "https://gitlab.com/g/p/-/merge_requests/12?x=1" }, "https"},
		{"fragment", func(m *RunMR) { m.URL = "https://gitlab.com/g/p/-/merge_requests/12#note" }, "https"},
		{"another number", func(m *RunMR) { m.URL = "https://gitlab.com/g/p/-/merge_requests/13" }, "does not end"},
		{"not a merge request", func(m *RunMR) { m.URL = "https://gitlab.com/g/p/-/issues/12" }, "does not end"},
		{"no project", func(m *RunMR) { m.URL = "https://gitlab.com/-/merge_requests/12" }, "does not end"},
		{"url with a space", func(m *RunMR) { m.URL = "https://gitlab.com/g/p x/-/merge_requests/12" }, "plain address"},
		{"url too long", func(m *RunMR) {
			m.URL = "https://gitlab.com/" + strings.Repeat("a", 2100) + "/-/merge_requests/12"
		}, "plain address"},
		{"skipped with a line break", func(m *RunMR) { *m = RunMR{Skipped: "a\nb"} }, "skipped"},
		{"error with a control character", func(m *RunMR) { *m = RunMR{Error: "a\x1bb"} }, "error"},
		{"error too long", func(m *RunMR) { *m = RunMR{Error: strings.Repeat("e", 501)} }, "error"},
		{"error at the bound", func(m *RunMR) { *m = RunMR{Error: strings.Repeat("e", 500)} }, ""},
	}
	for _, c := range cases {
		m := created()
		c.mutate(&m)
		err := m.Validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: Validate() = %v, want nil", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Validate() = %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

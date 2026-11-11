package persistence

import (
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

func reviseSource() *domain.TaskSource {
	sha := strings.Repeat("c", 40)
	return &domain.TaskSource{Kind: domain.TaskSourceRevise, Key: "PAY-31", URL: "https://jira.example/browse/PAY-31", FetchedAt: time.Now(),
		Revise: &domain.ReviseSource{
			Target: "PAY-31", TargetKind: domain.ReviseTargetJira, Project: "acme/payments/billing", Mode: domain.ReviseUpdate,
			Branch: "fix/PAY-31", BaseRef: sha, BaseBranch: "main",
			MR: &domain.MergeRequest{Project: "acme/payments/billing", IID: 12, URL: "https://gitlab.example.com/acme/payments/billing/-/merge_requests/12",
				State: domain.MRStateOpened, SourceBranch: "fix/PAY-31", TargetBranch: "main", SHA: sha, Title: "PAY-31: Round"},
		}}
}

func TestValidateReviseSource(t *testing.T) {
	if err := validateTaskSource(*reviseSource()); err != nil {
		t.Fatalf("a valid revise source is refused: %v", err)
	}
	cases := map[string]func(*domain.TaskSource){
		"no revise":           func(s *domain.TaskSource) { s.Revise = nil },
		"story field":         func(s *domain.TaskSource) { s.Story = "x" },
		"bad key":             func(s *domain.TaskSource) { s.Key = "x" },
		"bad kind":            func(s *domain.TaskSource) { s.Revise.TargetKind = "url" },
		"bad project":         func(s *domain.TaskSource) { s.Revise.Project = "../x" },
		"bad mode":            func(s *domain.TaskSource) { s.Revise.Mode = "amend" },
		"update without base": func(s *domain.TaskSource) { s.Revise.BaseRef = "" },
		"branch not the mr's": func(s *domain.TaskSource) { s.Revise.Branch = "other" },
		"bad branch":          func(s *domain.TaskSource) { s.Revise.Branch, s.Revise.MR.SourceBranch = "-x", "-x" },
		"fork":                func(s *domain.TaskSource) { s.Revise.MR.Fork = true },
		"locked":              func(s *domain.TaskSource) { s.Revise.MR.State = domain.MRStateLocked },
		"new with a branch":   func(s *domain.TaskSource) { s.Revise.Mode = domain.ReviseNew },
		"from_run":            func(s *domain.TaskSource) { s.Revise.FromRun = "abc" },
		"target two lines":    func(s *domain.TaskSource) { s.Revise.Target = "a\nb" },
	}
	for name, mutate := range cases {
		src := reviseSource()
		mutate(src)
		if err := validateTaskSource(*src); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	newMR := reviseSource()
	newMR.Revise.Mode, newMR.Revise.Branch, newMR.Revise.BaseRef = domain.ReviseNew, "", ""
	newMR.Revise.MR.State = domain.MRStateMerged
	if err := validateTaskSource(*newMR); err != nil {
		t.Errorf("a new merge request is refused: %v", err)
	}
	bare := reviseSource()
	bare.Key, bare.URL, bare.FetchedAt = "", "", time.Time{}
	bare.Revise.MR, bare.Revise.TargetKind, bare.Revise.Target = nil, domain.ReviseTargetBranch, "fix/PAY-31"
	if err := validateTaskSource(*bare); err != nil {
		t.Errorf("a branch without a merge request is refused: %v", err)
	}
}

func TestValidateRun_ReviseAcceptance(t *testing.T) {
	run := &domain.Run{ID: "r", Status: domain.RunFailed}
	run.Task = domain.Task{Description: "text", Acceptance: []string{"Round half up"}, Source: reviseSource()}
	if err := validateRun(run, "r"); err != nil {
		t.Fatalf("a revise run with criteria is refused: %v", err)
	}
}

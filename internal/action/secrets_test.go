package action

import (
	"context"
	"strings"
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// testAWSKey is assembled at run time: no token-shaped literal in the repository.
var testAWSKey = "AK" + "IA" + "Z7Q2R5T8W1Y4B6C9"

// secretDispatcher is a dispatcher over a real repository with the secret scan in mode.
func secretDispatcher(t *testing.T, mode string, client provider.Client) (*Dispatcher, string) {
	t.Helper()
	repo, dir := guardRepo(t)
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Security.SecretScan = config.SecretScanConfig{Mode: mode, AllowPaths: []string{"testdata/**"}}
	d := New(cfg, registry, repo, nil)
	d.Guard = &fakeGuard{}
	return d, dir
}

const implementOK = `{"summary":"done","changed_files":[],"tests_added":[]}`

func TestScanSecrets_StopNamesWhereNeverWhat(t *testing.T) {
	d, dir := secretDispatcher(t, config.SecretScanStop, &fakeClient{output: implementOK})
	put(t, dir, "pkg/cfg.go", "package pkg\n\nconst id = \""+testAWSKey+"\"\n")

	_, err := d.Execute(context.Background(), domain.ActionImplement, &domain.Run{ID: "r", PreRunRef: "abc123"})
	if err == nil {
		t.Fatal("a new secret must stop the run")
	}
	for _, want := range []string{"implement added what looks like a secret", "pkg/cfg.go:3 (aws-access-key-id)", "Restore point: abc123", "allow_paths", "mode: warn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), testAWSKey) {
		t.Error("the message contains the value")
	}
}

func TestScanSecrets_BaselineAndAllowPaths(t *testing.T) {
	d, dir := secretDispatcher(t, config.SecretScanStop, &fakeClient{output: implementOK})
	put(t, dir, "old.go", "package main\n\nvar k = \""+testAWSKey+"\"\n")
	run := &domain.Run{ID: "r"}
	baseline, err := d.SecretBaseline(context.Background())
	if err != nil || len(baseline) != 1 || len(baseline[0]) != 64 {
		t.Fatalf("baseline = %v, %v", baseline, err)
	}
	run.SecretScanBaseline = baseline

	// The same secret moved within its file is still the baseline's; a fixture is allowed.
	put(t, dir, "old.go", "package main\n\n// moved\nvar k = \""+testAWSKey+"\"\n")
	put(t, dir, "internal/x/testdata/key.txt", testAWSKey+"\n")
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatalf("baseline or allowed findings stopped the run: %v", err)
	}

	// The same value in another file is new.
	put(t, dir, "copy.go", "package main\n\nvar c = \""+testAWSKey+"\"\n")
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err == nil || !strings.Contains(err.Error(), "copy.go:3") {
		t.Fatalf("a copy in another file must count, got %v", err)
	}
}

func TestScanSecrets_WarnRecordsOnceAndRedactsReview(t *testing.T) {
	client := &fakeClient{output: implementOK}
	d, dir := secretDispatcher(t, config.SecretScanWarn, client)
	var told []string
	d.OnSecret = func(step domain.ActionType, file string, line int, rule string) {
		told = append(told, string(step)+" "+file+" "+rule)
	}
	put(t, dir, "pkg/cfg.go", "package pkg\n\nconst id = \""+testAWSKey+"\"\n")
	run := &domain.Run{ID: "r", Executions: []domain.StepExecution{{Index: 0, Action: domain.ActionResearch}}}

	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatalf("warn must not stop the run: %v", err)
	}
	want := domain.SecretFinding{StepIndex: 1, File: "pkg/cfg.go", Line: 3, Rule: "aws-access-key-id"}
	if len(run.SecretFindings) != 1 {
		t.Fatalf("findings = %+v", run.SecretFindings)
	}
	got := run.SecretFindings[0]
	if got.StepIndex != want.StepIndex || got.File != want.File || got.Line != want.Line || got.Rule != want.Rule || len(got.Fingerprint) != 64 {
		t.Errorf("finding = %+v, want %+v", got, want)
	}
	if strings.Join(told, "|") != "implement pkg/cfg.go aws-access-key-id" {
		t.Errorf("OnSecret calls = %v", told)
	}

	// Seen again by a later step: not recorded twice.
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil || len(run.SecretFindings) != 1 {
		t.Errorf("a known finding was recorded again: %+v, %v", run.SecretFindings, err)
	}

	client.output = `{"status":"pass","summary":"ok","issues":[]}`
	if _, err := d.Execute(context.Background(), domain.ActionReview, run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.lastReq.Prompt, testAWSKey) || !strings.Contains(client.lastReq.Prompt, "[REDACTED:aws-access-key-id]") {
		t.Errorf("the review prompt was not redacted")
	}
}

func TestScanSecrets_Off(t *testing.T) {
	client := &fakeClient{output: implementOK}
	d, dir := secretDispatcher(t, config.SecretScanOff, client)
	put(t, dir, "pkg/cfg.go", "package pkg\n\nconst id = \""+testAWSKey+"\"\n")
	run := &domain.Run{ID: "r"}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil || run.SecretFindings != nil {
		t.Errorf("mode off scanned: %v %+v", err, run.SecretFindings)
	}
}

package execution

import (
	"context"
	"testing"
	"time"
)

func TestOSProcessExecutor_Success(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), "echo", []string{"hello"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.Stdout != "hello\n" {
		t.Errorf("expected stdout %q, got %q", "hello\n", res.Stdout)
	}
}

func TestOSProcessExecutor_NonZeroExit(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), "sh", []string{"-c", "exit 7"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("expected exit code 7, got %d", res.ExitCode)
	}
}

func TestOSProcessExecutor_Stderr(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), "sh", []string{"-c", "echo oops 1>&2"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stderr != "oops\n" {
		t.Errorf("expected stderr %q, got %q", "oops\n", res.Stderr)
	}
}

func TestOSProcessExecutor_Cancellation(t *testing.T) {
	exec := NewOSProcessExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := exec.Run(ctx, "sleep", []string{"5"}, "", "")
	if err == nil {
		t.Fatal("expected an error when the context is cancelled mid-execution")
	}
}

func TestOSProcessExecutor_Stdin(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), "cat", []string{}, "piped input", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "piped input" {
		t.Errorf("expected stdout %q, got %q", "piped input", res.Stdout)
	}
}

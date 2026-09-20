package execution

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestOSProcessExecutor_Success(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), Spec{Executable: "echo", Args: []string{"hello"}})
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
	res, err := exec.Run(context.Background(), Spec{Executable: "sh", Args: []string{"-c", "exit 7"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("expected exit code 7, got %d", res.ExitCode)
	}
}

func TestOSProcessExecutor_Stderr(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), Spec{Executable: "sh", Args: []string{"-c", "echo oops 1>&2"}})
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

	res, err := exec.Run(ctx, Spec{Executable: "sleep", Args: []string{"5"}})
	if err == nil {
		t.Fatal("expected an error when the context is cancelled mid-execution")
	}
	// -1 is Go's documented ExitCode() value for "terminated by signal" - it must not be
	// left at the zero-value, which would look identical to a clean, successful exit.
	if res.ExitCode != -1 {
		t.Errorf("expected ExitCode -1 for a signal-killed process, got %d", res.ExitCode)
	}
}

func TestOSProcessExecutor_Stdin(t *testing.T) {
	exec := NewOSProcessExecutor()
	res, err := exec.Run(context.Background(), Spec{Executable: "cat", Stdin: "piped input"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "piped input" {
		t.Errorf("expected stdout %q, got %q", "piped input", res.Stdout)
	}
}

func TestOSProcessExecutor_StreamsToSinks(t *testing.T) {
	exec := NewOSProcessExecutor()
	var stdout, stderr bytes.Buffer
	res, err := exec.Run(context.Background(), Spec{
		Executable: "sh",
		Args:       []string{"-c", "echo out; echo err 1>&2"},
		StdoutSink: &stdout,
		StderrSink: &stderr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "out\n" || res.Stdout != "out\n" {
		t.Errorf("expected stdout to reach both the sink and the result, got sink=%q result=%q", stdout.String(), res.Stdout)
	}
	if stderr.String() != "err\n" || res.Stderr != "err\n" {
		t.Errorf("expected stderr to reach both the sink and the result, got sink=%q result=%q", stderr.String(), res.Stderr)
	}
}

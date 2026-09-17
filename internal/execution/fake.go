package execution

import "context"

// RecordedCall captures the arguments of one Run invocation, for assertions in
// adapter/action unit tests.
type RecordedCall struct {
	Executable string
	Args       []string
	Stdin      string
	WorkingDir string
}

// FakeProcessExecutor is a test double: each call to Run consumes the next scripted
// Result/Err pair (or returns a zero ProcessResult if the script is exhausted). If the
// spec provides sinks, the scripted result's stdout/stderr is written to them so
// --verbose streaming is exercisable without spawning a real process.
type FakeProcessExecutor struct {
	Results []ProcessResult
	Errs    []error
	Calls   []RecordedCall
	callNum int
}

func (f *FakeProcessExecutor) Run(ctx context.Context, spec Spec) (ProcessResult, error) {
	f.Calls = append(f.Calls, RecordedCall{
		Executable: spec.Executable,
		Args:       spec.Args,
		Stdin:      spec.Stdin,
		WorkingDir: spec.WorkingDir,
	})

	idx := f.callNum
	f.callNum++

	var result ProcessResult
	if idx < len(f.Results) {
		result = f.Results[idx]
	}

	if spec.StdoutSink != nil && result.Stdout != "" {
		_, _ = spec.StdoutSink.Write([]byte(result.Stdout))
	}
	if spec.StderrSink != nil && result.Stderr != "" {
		_, _ = spec.StderrSink.Write([]byte(result.Stderr))
	}

	var err error
	if idx < len(f.Errs) {
		err = f.Errs[idx]
	}

	return result, err
}

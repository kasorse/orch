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
// Result/Err pair (or returns a zero ProcessResult if the script is exhausted).
type FakeProcessExecutor struct {
	Results []ProcessResult
	Errs    []error
	Calls   []RecordedCall
	callNum int
}

func (f *FakeProcessExecutor) Run(ctx context.Context, executable string, args []string, stdin string, workingDir string) (ProcessResult, error) {
	f.Calls = append(f.Calls, RecordedCall{Executable: executable, Args: args, Stdin: stdin, WorkingDir: workingDir})

	idx := f.callNum
	f.callNum++

	var result ProcessResult
	if idx < len(f.Results) {
		result = f.Results[idx]
	}

	var err error
	if idx < len(f.Errs) {
		err = f.Errs[idx]
	}

	return result, err
}

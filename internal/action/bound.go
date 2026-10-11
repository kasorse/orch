package action

import "orchestrator/internal/testexec"

// BoundTestOutput lives in package testexec so the verify server can bound its output the
// same way without importing this package (specs/quality/SPEC.md §6.1). It keeps its old
// names here.
type Omitted = testexec.Omitted

// BoundTestOutput cuts a failing test's stdout and stderr to fit budget bytes; see
// testexec.BoundTestOutput.
var BoundTestOutput = testexec.BoundTestOutput

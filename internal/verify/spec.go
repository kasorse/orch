// Package verify is the in-session verify server (specs/quality/SPEC.md §6): a stdio MCP
// server, started as `orchestrator mcp-verify --dir <dir>`, that serves two tools to an
// implement session, run_tests and run_check. Each call checks the guard, runs a command
// chosen in Go from verify.json in the sandbox, and records the call.
//
// The server reads no config file, opens no run directory and makes no network call. Its
// inputs are dir/verify.json and its stdin; its outputs are its stdout (the MCP channel,
// written only by the JSON-RPC writer) and, inside dir, calls.jsonl, violation and the
// per-call log files. The protocol is implemented with the standard library alone.
package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/guard"
	"orchestrator/internal/testexec"
)

// SpecFile is the name of the server's input inside its directory, and SpecVersion the one
// layout of it this code reads.
const (
	SpecFile    = "verify.json"
	SpecVersion = 1
)

// maxSpecBytes bounds what ReadSpec reads; a real spec is a few KiB.
const maxSpecBytes = 8 << 20

// Check is one operator-defined check the model may run by name.
type Check struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Spec is everything the server needs (specs/quality/SPEC.md §6.4). Go computes it before
// the implement call; the model cannot write the file it is stored in.
type Spec struct {
	Version int `json:"version"`
	// RepoRoot is the directory every command runs in.
	RepoRoot    string  `json:"repo_root"`
	TestCommand string  `json:"test_command"`
	Checks      []Check `json:"checks"`
	// PreRunRef becomes ORCH_BASE_REF of every run.
	PreRunRef string `json:"pre_run_ref"`
	// Sandbox is required: verify never runs unsandboxed.
	Sandbox *testexec.Sandbox `json:"sandbox"`
	// TestEnv is the computed test environment (execution.TestEnv()) as KEY=value pairs. The
	// server does not rebuild the allowlist from a config file. It never holds an ORCH_*
	// name: those are set per call, ORCH_TEST_FILTER only from a validated filter.
	TestEnv []string `json:"test_env"`
	// Before is the guard's before-state of the implement step, with the patterns it was
	// taken with.
	Before guard.State `json:"before"`
	// MaxCalls counts both tools, refused calls included.
	MaxCalls    int           `json:"max_calls"`
	CallTimeout time.Duration `json:"call_timeout"`
	// OutputBytes is the bound on the output of one answer; 0 keeps everything.
	OutputBytes int `json:"output_bytes"`
}

// envNameRE is what the name of a TestEnv pair must look like.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate checks the spec the way ReadSpec does after decoding.
func (s *Spec) Validate() error {
	if s.Version != SpecVersion {
		return fmt.Errorf("unsupported version %d, want %d", s.Version, SpecVersion)
	}
	if !filepath.IsAbs(s.RepoRoot) {
		return fmt.Errorf("repo_root %q is not an absolute path", s.RepoRoot)
	}
	if strings.TrimSpace(s.TestCommand) == "" {
		return errors.New("test_command is empty")
	}
	if err := s.validateSandbox(); err != nil {
		return err
	}
	if len(s.Checks) > domain.MaxChecks {
		return fmt.Errorf("%d checks, at most %d", len(s.Checks), domain.MaxChecks)
	}
	seen := map[string]bool{}
	for _, c := range s.Checks {
		switch {
		case !domain.CheckNameRE.MatchString(c.Name):
			return fmt.Errorf("check name %q is invalid", c.Name)
		case seen[c.Name]:
			return fmt.Errorf("check %q appears twice", c.Name)
		case strings.TrimSpace(c.Command) == "":
			return fmt.Errorf("check %q has no command", c.Name)
		}
		seen[c.Name] = true
	}
	for _, kv := range s.TestEnv {
		name, _, ok := strings.Cut(kv, "=")
		switch {
		case !ok || !envNameRE.MatchString(name):
			return fmt.Errorf("test_env entry %q is not NAME=value", name)
		case strings.HasPrefix(name, "ORCH_"):
			return fmt.Errorf("test_env must not carry %s: ORCH_* variables are set per call", name)
		}
	}
	if len(s.Before.Patterns()) == 0 {
		return errors.New("before: the guard state is empty")
	}
	if s.MaxCalls < 1 || s.MaxCalls > domain.MaxVerifyCalls {
		return fmt.Errorf("max_calls %d is outside 1..%d", s.MaxCalls, domain.MaxVerifyCalls)
	}
	if s.CallTimeout <= 0 {
		return fmt.Errorf("call_timeout %s must be positive", s.CallTimeout)
	}
	if s.OutputBytes < 0 {
		return fmt.Errorf("output_bytes %d is negative", s.OutputBytes)
	}
	return nil
}

func (s *Spec) validateSandbox() error {
	sb := s.Sandbox
	if sb == nil {
		return errors.New("sandbox is missing: verify never runs unsandboxed")
	}
	paths := append([]string{sb.Home, sb.RepoRoot}, sb.GitDirs...)
	paths = append(paths, sb.ReadOnlyFiles...)
	paths = append(paths, sb.ReadOnly...)
	paths = append(paths, sb.ReadWrite...)
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("sandbox path %q is not absolute", p)
		}
	}
	return nil
}

// WriteSpec validates spec and stores it as dir/verify.json with mode 0600, through a
// temporary file in dir and a rename, so a reader never sees half a file.
func WriteSpec(dir string, spec *Spec) (err error) {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("%s: %w", SpecFile, err)
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: %w", SpecFile, err)
	}
	tmp, err := os.CreateTemp(dir, ".verify-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: %w", SpecFile, err)
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, SpecFile))
	}
	if err != nil {
		return fmt.Errorf("%s: %w", SpecFile, err)
	}
	return nil
}

// ReadSpec reads dir/verify.json strictly: it must be a regular file that only its owner
// can use, hold one JSON object with no unknown field, and pass Validate. Every error names
// verify.json.
func ReadSpec(dir string) (*Spec, error) {
	spec, err := readSpec(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SpecFile, err)
	}
	return spec, nil
}

func readSpec(dir string) (*Spec, error) {
	f, err := os.OpenFile(filepath.Join(dir, SpecFile), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("mode %04o lets others read or write it, want 0600", info.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSpecBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSpecBytes {
		return nil, errors.New("file is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var spec Spec
	if err := dec.Decode(&spec); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the object")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

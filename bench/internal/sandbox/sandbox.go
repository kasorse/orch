// Package sandbox runs one shell command under bubblewrap (specs/benchmark/SPEC.md §7.4): the
// filesystem read-only, the workspace writable, $HOME and /tmp private and empty, no network
// unless asked for, and an allowlisted environment. It is the policy of the core's
// testexec.Sandbox and execution.TestEnv, written again here because bench cannot import the
// core, and kept small.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"orchbench/internal/task"
)

// Spec is one command to run.
type Spec struct {
	// Dir is the workspace: writable, and the working directory.
	Dir string
	// Command is run as `sh -c Command`.
	Command string
	// RoBinds and RwBinds are the task's sandbox.ro_binds and sandbox.rw_binds (caches, module
	// stores). A leading ~ is the home directory. One that does not exist is skipped.
	RoBinds []string
	RwBinds []string
	// Network leaves the network namespace alone. Only a trusted setup_command gets it.
	Network bool
	// Timeout kills the command's process group; zero is no limit.
	Timeout time.Duration
	// Stdout and Stderr, when set, also receive the output as it is produced, for a log file
	// or -v. Run returns the output either way. The same writer may be given for both.
	Stdout io.Writer
	Stderr io.Writer
}

// ForTask is the Spec every caller needs for a task's commands: the workspace and the task's
// binds. The caller sets Command and the writers.
func ForTask(dir string, t task.Task, network bool, timeout time.Duration) Spec {
	return Spec{
		Dir:     dir,
		RoBinds: slices.Clone(t.Sandbox.RoBinds),
		RwBinds: slices.Clone(t.Sandbox.RwBinds),
		Network: network,
		Timeout: timeout,
	}
}

// ErrTimeout is returned by Run, with exit code -1, when the command ran into Spec.Timeout.
var ErrTimeout = errors.New("sandbox: command timed out")

// waitDelay is how long Run waits for the output pipes after the process group is killed.
var waitDelay = 2 * time.Second

// baseMounts is what every sandboxed process sees before any bind: the whole filesystem
// read-only, fresh /dev and /proc, a private writable /tmp.
var baseMounts = []string{"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp"}

// isolation: own user, pid, ipc and uts namespaces and no network unless asked for; dies with
// orchbench, and a new session so the command cannot push input into its terminal.
func isolation(network bool) []string {
	argv := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts"}
	if !network {
		argv = append(argv, "--unshare-net")
	}
	return append(argv, "--die-with-parent", "--new-session")
}

// args builds the bwrap argv. Mount order is load-bearing: bwrap applies mounts in order,
// later ones over earlier ones. The $HOME tmpfs comes before the workspace and the binds (a
// workspace under $HOME would be hidden otherwise), and the workspace's .git after the
// workspace (or it would be writable again: a test that wrote a hook or core.fsmonitor there
// would run code outside the sandbox on orchbench's next git call).
func (s Spec) args(home string) ([]string, error) {
	if s.Dir == "" {
		return nil, errors.New("sandbox: Spec.Dir is empty")
	}
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return nil, err
	}

	argv := slices.Clone(baseMounts)
	if filepath.IsAbs(home) {
		argv = append(argv, "--tmpfs", home)
	}
	argv = append(argv, "--bind", dir, dir)
	if gitDir := filepath.Join(dir, ".git"); exists(gitDir) {
		argv = append(argv, "--ro-bind", gitDir, gitDir)
	}
	for _, p := range s.RoBinds {
		if p, ok := resolveBind(p, home); ok {
			argv = append(argv, "--ro-bind", p, p)
		}
	}
	for _, p := range s.RwBinds {
		if p, ok := resolveBind(p, home); ok {
			argv = append(argv, "--bind", p, p)
		}
	}
	argv = append(argv, isolation(s.Network)...)
	return append(argv, "--chdir", dir, "sh", "-c", s.Command), nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// resolveBind makes a bind path absolute, expanding a leading ~, and reports whether it
// exists: a cache directory that is not there yet is skipped, as the core does.
func resolveBind(p, home string) (string, bool) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home == "" {
			return "", false
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil || !exists(abs) {
		return "", false
	}
	return abs, true
}

// The environment of every sandboxed command: the variables of the core's test allowlist
// that are set. Everything else is dropped, because the command is a shell and whatever
// reaches it, the code under test can read, print or send. Proxies are absent on purpose: a
// proxy URL can carry credentials. TMPDIR is set separately.
var (
	envBase = []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL",
		"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "LC_COLLATE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
		"TERM", "TZ", "CI",
	}
	envToolchain = []string{
		"GOPATH", "GOROOT", "GOFLAGS", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "CGO_ENABLED",
		"CARGO_HOME", "RUSTUP_HOME", "JAVA_HOME", "VIRTUAL_ENV", "PYTHONPATH", "NODE_OPTIONS", "XDG_CACHE_HOME",
	}
)

// env is the allowlisted part of environ with TMPDIR pointing at the private /tmp: an
// inherited TMPDIR under /tmp does not exist inside the sandbox.
func env(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(envBase, name) || slices.Contains(envToolchain, name) {
			out = append(out, kv)
		}
	}
	return append(out, "TMPDIR=/tmp")
}

// tee collects output and forwards it to an optional writer. One mutex serves stdout and
// stderr, so a writer given for both is never written concurrently. A failing writer (a
// closed log) does not stop the command.
type tee struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
	w   io.Writer
}

func (t *tee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if t.w != nil {
		t.w.Write(p)
	}
	return len(p), nil
}

// Run runs the command and returns its exit code and output. A command that ran and failed
// is not an error: its code is returned with a nil error. A timeout is exit -1 and
// ErrTimeout; a cancelled ctx is -1 and ctx.Err(). Both kill the whole process group.
// bwrap not starting at all is an error too.
func Run(ctx context.Context, s Spec) (exit int, stdout, stderr []byte, err error) {
	home, _ := os.UserHomeDir()
	argv, err := s.args(home)
	if err != nil {
		return -1, nil, nil, err
	}

	runCtx := ctx
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}

	var mu sync.Mutex
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(runCtx, "bwrap", argv...)
	cmd.Env = env(os.Environ())
	cmd.Stdout = &tee{mu: &mu, buf: &out, w: s.Stdout}
	cmd.Stderr = &tee{mu: &mu, buf: &errb, w: s.Stderr}
	// Its own process group, so that a timeout kills the command's children too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay

	runErr := cmd.Run()

	mu.Lock()
	stdout, stderr = bytes.Clone(out.Bytes()), bytes.Clone(errb.Bytes())
	mu.Unlock()

	switch {
	case runErr == nil:
		return 0, stdout, stderr, nil
	case ctx.Err() != nil:
		return -1, stdout, stderr, ctx.Err()
	case runCtx.Err() != nil:
		return -1, stdout, stderr, ErrTimeout
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitStatus(exitErr), stdout, stderr, nil
	}
	return -1, stdout, stderr, fmt.Errorf("sandbox: %w", runErr)
}

// exitStatus is the exit code, or 128+signal for a process a signal ended, as a shell reports.
func exitStatus(e *exec.ExitError) int {
	if code := e.ExitCode(); code >= 0 {
		return code
	}
	if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return -1
}

// ProbeError says that bwrap cannot create a sandbox on this host.
type ProbeError struct {
	// Detail is what bwrap printed, or why it could not be started.
	Detail string
}

func (e *ProbeError) Error() string {
	return fmt.Sprintf("bubblewrap cannot create a sandbox on this host:\n  %s\n\n"+
		"On Ubuntu 24.04 and later AppArmor blocks unprivileged user namespaces; allow them for\n"+
		"bwrap with a profile (see Claude Code's sandboxing docs, \"Ubuntu 24.04 and later\"):\n\n"+
		"  /etc/apparmor.d/bwrap:\n"+
		"    abi <abi/4.0>,\n"+
		"    include <tunables/global>\n"+
		"    profile bwrap /usr/bin/bwrap flags=(unconfined) {\n"+
		"      userns,\n"+
		"    }\n"+
		"  then: sudo systemctl reload apparmor", e.Detail)
}

// probeArgs is the smallest bwrap run with the mounts and isolation of a real one, so a host
// that cannot sandbox fails before any repository is cloned.
func probeArgs() []string {
	argv := append(slices.Clone(baseMounts), isolation(false)...)
	return append(argv, "true")
}

// Probe returns nil when bwrap can create a sandbox, a *ProbeError when it cannot, and ctx's
// error when ctx ends first.
func Probe(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "bwrap", probeArgs()...)
	cmd.Env = env(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		detail = err.Error()
	}
	return &ProbeError{Detail: detail}
}

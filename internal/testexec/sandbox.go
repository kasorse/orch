package testexec

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

// Sandbox runs the test command under bubblewrap (specs/permissions/SPEC.md §8): the test
// step executes code a model may have written, so it gets the filesystem read-only, the
// repository writable, $HOME hidden, a private /tmp, and no network. Every path is
// absolute and already resolved.
type Sandbox struct {
	// Home is hidden behind an empty tmpfs: ~/.ssh, ~/.aws, ~/.config/gh and the rest are
	// not visible unless re-exposed through ReadOnly/ReadWrite.
	Home string
	// RepoRoot is the one writable tree.
	RepoRoot string
	// GitDirs are re-bound read-only over RepoRoot: a test that wrote a hook or
	// core.fsmonitor into .git would run code outside the sandbox on the orchestrator's
	// next git call.
	GitDirs []string
	// ReadOnly and ReadWrite are test.ro_binds / test.rw_binds (caches, module stores).
	ReadOnly  []string
	ReadWrite []string
}

// baseMounts is what every sandboxed process sees before any bind: the whole filesystem
// read-only, fresh /dev and /proc, a private writable /tmp.
var baseMounts = []string{"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp"}

// isolation: no network, own user/pid/ipc/uts namespaces, dies with the orchestrator,
// and a new session so the test cannot push input into the orchestrator's terminal.
var isolation = []string{
	"--unshare-user", "--unshare-net", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
	"--die-with-parent", "--new-session",
}

// args builds the bwrap argv. Mount order is load-bearing - bwrap applies mounts in
// order, later ones over earlier ones: the $HOME tmpfs must come before the repository
// and cache binds (a repository under $HOME would otherwise be hidden), and the git
// directories after the repository bind (or they would be writable again).
func (s *Sandbox) args(workingDir, command string) []string {
	argv := append([]string(nil), baseMounts...)
	argv = append(argv, "--tmpfs", s.Home, "--bind", s.RepoRoot, s.RepoRoot)
	for _, d := range s.GitDirs {
		argv = append(argv, "--ro-bind", d, d)
	}
	for _, d := range s.ReadOnly {
		argv = append(argv, "--ro-bind", d, d)
	}
	for _, d := range s.ReadWrite {
		argv = append(argv, "--bind", d, d)
	}
	argv = append(argv, isolation...)
	return append(argv, "--chdir", workingDir, "sh", "-c", command)
}

// env is the test environment (execution.TestEnv) with TMPDIR pointing at the private
// /tmp: an inherited TMPDIR under /tmp no longer exists inside the sandbox.
func (s *Sandbox) env() []string {
	var env []string
	for _, kv := range execution.TestEnv() {
		if !strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	return append(env, "TMPDIR=/tmp")
}

// ProbeArgs is the smallest bwrap run using the same mounts and isolation as a real test
// run, so a host that cannot sandbox fails here, before any AI call, instead of at the
// first test step.
func ProbeArgs() []string {
	argv := append(append([]string(nil), baseMounts...), isolation...)
	return append(argv, "true")
}

// Probe runs ProbeArgs and turns a failure into a DependencyError naming the usual cause.
func Probe(ctx context.Context, executor execution.ProcessExecutor) error {
	res, err := executor.Run(ctx, execution.Spec{Executable: "bwrap", Args: ProbeArgs(), Env: execution.TestEnv()})
	if err == nil && res.ExitCode == 0 {
		return nil
	}
	detail := strings.TrimSpace(res.Stderr)
	if err != nil {
		detail = err.Error()
	}
	return apperr.NewDependencyError(fmt.Sprintf(
		"the test command runs under bwrap (test.sandbox: bwrap, the default), but bubblewrap cannot\n"+
			"create a sandbox on this host:\n  %s\n\n"+
			"On Ubuntu 24.04 and later AppArmor blocks unprivileged user namespaces; allow them for\n"+
			"bwrap with a profile (see Claude Code's sandboxing docs, \"Ubuntu 24.04 and later\"):\n\n"+
			"  /etc/apparmor.d/bwrap:\n"+
			"    abi <abi/4.0>,\n"+
			"    include <tunables/global>\n"+
			"    profile bwrap /usr/bin/bwrap flags=(unconfined) {\n"+
			"      userns,\n"+
			"    }\n"+
			"  then: sudo systemctl reload apparmor\n\n"+
			"or set test.sandbox: none to run tests unsandboxed: the code under test, which a model\n"+
			"wrote, then reaches the network, $HOME and your files.", detail), err)
}

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"orchbench/internal/task"
)

// TestMain doubles as the program the network test runs inside the sandbox: given an address
// it dials it and exits 0 when the connection is made.
func TestMain(m *testing.M) {
	if addr := os.Getenv("ORCHBENCH_SANDBOX_DIAL"); addr != "" {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dial:", err)
			os.Exit(3)
		}
		conn.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var (
	probeOnce sync.Once
	probeErr  error
)

// requireBwrap skips the test where bwrap cannot create a sandbox; the probe runs once.
func requireBwrap(t *testing.T) {
	t.Helper()
	probeOnce.Do(func() { probeErr = Probe(context.Background()) })
	if probeErr != nil {
		detail := probeErr.Error()
		var pe *ProbeError
		if errors.As(probeErr, &pe) {
			detail = pe.Detail
		}
		t.Skipf("bwrap cannot create a sandbox here: %s (on Ubuntu 24.04 and later AppArmor may block it; see Probe)", detail)
	}
}

// outsideDir is a host directory that is neither under /tmp nor under $HOME, so that inside
// the sandbox it is part of the read-only root.
func outsideDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "orchbench-sandbox-")
	if err != nil {
		t.Skipf("no writable /var/tmp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func workspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, s Spec) (exit int, stdout, stderr string) {
	t.Helper()
	if s.Timeout == 0 {
		s.Timeout = time.Minute
	}
	exit, out, errb, err := Run(context.Background(), s)
	if err != nil {
		t.Fatalf("Run(%q): %v", s.Command, err)
	}
	return exit, string(out), string(errb)
}

func TestArgsOrder(t *testing.T) {
	ws := workspace(t)
	ro, rw := t.TempDir(), t.TempDir()
	s := Spec{
		Dir:     ws,
		Command: "echo hi",
		RoBinds: []string{ro, filepath.Join(ws, "does-not-exist")},
		RwBinds: []string{rw},
	}

	got, err := s.args("/home/u")
	if err != nil {
		t.Fatal(err)
	}

	var want []string
	want = append(want, baseMounts...)
	want = append(want, "--tmpfs", "/home/u", "--bind", ws, ws)
	want = append(want, "--ro-bind", filepath.Join(ws, ".git"), filepath.Join(ws, ".git"))
	want = append(want, "--ro-bind", ro, ro, "--bind", rw, rw)
	want = append(want, isolation(false)...)
	want = append(want, "--chdir", ws, "sh", "-c", "echo hi")
	if !slices.Equal(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
}

func TestArgsNetwork(t *testing.T) {
	ws := t.TempDir()

	for _, network := range []bool{false, true} {
		got, err := Spec{Dir: ws, Command: "true", Network: network}.args("")
		if err != nil {
			t.Fatal(err)
		}
		if has := slices.Contains(got, "--unshare-net"); has == network {
			t.Errorf("Network %v: --unshare-net present = %v", network, has)
		}
		for _, flag := range []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session"} {
			if !slices.Contains(got, flag) {
				t.Errorf("Network %v: %s is missing", network, flag)
			}
		}
		if slices.Contains(got, "--tmpfs") && !slices.Contains(got, "/tmp") {
			t.Error("no tmpfs for /tmp")
		}
	}
}

func TestArgsExpandsHome(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Spec{Dir: ws, Command: "true", RoBinds: []string{"~/.cache", "~/missing"}}.args(home)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(home, ".cache")
	if !slices.Contains(got, cache) {
		t.Errorf("~/.cache was not bound: %q", got)
	}
	if slices.Contains(got, filepath.Join(home, "missing")) {
		t.Errorf("a bind that does not exist was kept: %q", got)
	}
}

func TestArgsNeedsADirectory(t *testing.T) {
	if _, err := (Spec{Command: "true"}).args(""); err == nil {
		t.Fatal("a Spec without Dir was accepted")
	}
}

func TestEnvIsAnAllowlist(t *testing.T) {
	got := env([]string{
		"PATH=/bin", "HOME=/h", "LANG=C", "GOFLAGS=-mod=mod", "TMPDIR=/tmp/elsewhere",
		"ORCHBENCH_SECRET=s", "HTTPS_PROXY=http://user:pass@proxy", "CLAUDECODE=1", "ANTHROPIC_API_KEY=k",
	})

	want := []string{"PATH=/bin", "HOME=/h", "LANG=C", "GOFLAGS=-mod=mod", "TMPDIR=/tmp"}
	if !slices.Equal(got, want) {
		t.Fatalf("env = %q, want %q", got, want)
	}
}

func TestExitCodeIsPassedThrough(t *testing.T) {
	requireBwrap(t)
	for _, code := range []int{0, 1, 3, 42} {
		exit, _, _ := run(t, Spec{Dir: workspace(t), Command: fmt.Sprintf("exit %d", code)})
		if exit != code {
			t.Errorf("exit %d reported as %d", code, exit)
		}
	}
}

func TestOutputIsCollectedAndTeed(t *testing.T) {
	requireBwrap(t)

	var log bytes.Buffer
	exit, stdout, stderr := run(t, Spec{
		Dir:     workspace(t),
		Command: "echo out; echo err >&2",
		Stdout:  &log,
		Stderr:  &log,
	})
	if exit != 0 || stdout != "out\n" || stderr != "err\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if got := log.String(); !strings.Contains(got, "out\n") || !strings.Contains(got, "err\n") {
		t.Fatalf("tee got %q", got)
	}
}

func TestRunsInTheWorkspace(t *testing.T) {
	requireBwrap(t)

	ws := workspace(t)
	exit, stdout, _ := run(t, Spec{Dir: ws, Command: "pwd"})
	if exit != 0 || strings.TrimSpace(stdout) != ws {
		t.Fatalf("exit %d, pwd %q, want %q", exit, stdout, ws)
	}
}

func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	requireBwrap(t)

	start := time.Now()
	exit, _, _, err := Run(context.Background(), Spec{
		Dir:     workspace(t),
		Command: "sleep 30 & sleep 30; wait",
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) || exit != -1 {
		t.Fatalf("exit %d, err %v, want -1 and ErrTimeout", exit, err)
	}
	// A background child that survived the kill would hold the output pipes open until
	// waitDelay ran out.
	if elapsed >= waitDelay {
		t.Fatalf("took %s: the process group was not killed", elapsed)
	}
}

func TestCancelStopsTheCommand(t *testing.T) {
	requireBwrap(t)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)

	start := time.Now()
	exit, _, _, err := Run(ctx, Spec{Dir: workspace(t), Command: "sleep 30", Timeout: time.Minute})
	if !errors.Is(err, context.Canceled) || exit != -1 {
		t.Fatalf("exit %d, err %v, want -1 and context.Canceled", exit, err)
	}
	if elapsed := time.Since(start); elapsed >= waitDelay {
		t.Fatalf("took %s", elapsed)
	}
}

func TestHomeIsEmpty(t *testing.T) {
	requireBwrap(t)

	home := outsideDir(t)
	if err := os.WriteFile(filepath.Join(home, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	exit, stdout, _ := run(t, Spec{Dir: workspace(t), Command: `test -n "$HOME" && ls -A "$HOME"`})
	if exit != 0 || stdout != "" {
		t.Fatalf("exit %d, $HOME holds %q", exit, stdout)
	}
}

func TestWritesOutsideTheWorkspaceFail(t *testing.T) {
	requireBwrap(t)

	outside, rw := outsideDir(t), outsideDir(t)
	ws := workspace(t)
	hostTmp := fmt.Sprintf("/tmp/orchbench-sandbox-%d", os.Getpid())
	t.Cleanup(func() { os.Remove(hostTmp) })

	tests := []struct {
		name    string
		command string
		rw      []string
		exit0   bool   // the command must succeed
		file    string // exists on the host afterwards
	}{
		{"outside the workspace", "touch '" + outside + "/f'", nil, false, filepath.Join(outside, "f")},
		{"workspace", "touch f", nil, true, filepath.Join(ws, "f")},
		{"rw bind", "touch '" + rw + "/f'", []string{rw}, true, filepath.Join(rw, "f")},
		{"git directory", "touch .git/f", nil, false, filepath.Join(ws, ".git", "f")},
		{"private /tmp", "touch '" + hostTmp + "'", nil, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, _, stderr := run(t, Spec{Dir: ws, Command: tt.command, RwBinds: tt.rw})
			if (exit == 0) != tt.exit0 {
				t.Fatalf("exit %d, stderr %q", exit, stderr)
			}
			if tt.file != "" {
				if _, err := os.Stat(tt.file); (err == nil) != tt.exit0 {
					t.Fatalf("host file %s: exists = %v, want %v", tt.file, err == nil, tt.exit0)
				}
			}
		})
	}

	if _, err := os.Stat(hostTmp); err == nil {
		t.Errorf("a write to /tmp in the sandbox reached the host: %s", hostTmp)
	}
}

func TestEnvironmentInsideTheSandbox(t *testing.T) {
	requireBwrap(t)
	// Before TMPDIR changes: t.TempDir creates its directory under it.
	ws := workspace(t)
	t.Setenv("ORCHBENCH_SECRET", "s3cret")
	t.Setenv("TMPDIR", "/tmp/elsewhere")

	exit, stdout, _ := run(t, Spec{Dir: ws, Command: "env"})
	if exit != 0 {
		t.Fatalf("exit %d", exit)
	}
	if strings.Contains(stdout, "ORCHBENCH_SECRET") || strings.Contains(stdout, "s3cret") {
		t.Errorf("a variable outside the allowlist reached the command:\n%s", stdout)
	}
	if !slices.Contains(strings.Split(stdout, "\n"), "TMPDIR=/tmp") {
		t.Errorf("TMPDIR is not /tmp:\n%s", stdout)
	}
}

func TestNetwork(t *testing.T) {
	requireBwrap(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener: %v", err)
	}
	defer listener.Close()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("ORCHBENCH_SANDBOX_DIAL=%s '%s'", listener.Addr(), exe)

	for _, network := range []bool{false, true} {
		exit, _, stderr := run(t, Spec{
			Dir: workspace(t),
			// The test binary lives under /tmp, which is a private tmpfs inside.
			RoBinds: []string{filepath.Dir(exe)},
			Command: command,
			Network: network,
		})
		if connected := exit == 0; connected != network {
			t.Errorf("Network %v: exit %d (%s), connected = %v", network, exit, strings.TrimSpace(stderr), connected)
		}
	}
}

func TestForTaskCopiesTheBinds(t *testing.T) {
	tk := task.Task{Sandbox: task.Sandbox{RoBinds: []string{"/a"}, RwBinds: []string{"/b"}}}

	s := ForTask("/ws", tk, true, time.Minute)
	if s.Dir != "/ws" || !s.Network || s.Timeout != time.Minute || s.Command != "" {
		t.Fatalf("spec = %+v", s)
	}
	s.RoBinds[0] = "/changed"
	if tk.Sandbox.RoBinds[0] != "/a" {
		t.Error("ForTask shares the task's slices")
	}
	if !slices.Equal(s.RwBinds, []string{"/b"}) {
		t.Errorf("RwBinds = %v", s.RwBinds)
	}
}

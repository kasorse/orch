package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	orchestrator "github.com/kasorse/orch"
)

// orchestrator.Listen with Options.Dir runs the listener in that directory
// (specs/embed/SPEC.md §4.1): the state directory appears under Dir, and the process's working
// directory, which the listener never reads, stays where it was.
func TestListenLibrary_DirIsTheListenersWorkingDirectory(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.resetListenerProcessEnv()

	here := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(here); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	var stderr syncBuffer
	err = orchestrator.Listen(context.Background(), orchestrator.Options{Dir: env.wd, Config: env.cfg, Once: true, Stderr: &stderr})
	if err != nil {
		t.Fatalf("Listen returned %v\nstderr:\n%s", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(env.wd, ".orchestrator", "listener", "lock")); err != nil {
		t.Errorf("no listener state under Dir: %v\nstderr:\n%s", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(here, ".orchestrator")); err == nil {
		t.Errorf("the listener wrote under the process's working directory")
	}
}

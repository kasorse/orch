package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/config"
)

// The service's own file wins, then <configs_dir>/<service>/orchestrator.yaml; with neither (and
// no default_config) the error names the path that was looked for.
func TestConfigFor_ConfigsDir(t *testing.T) {
	wd := t.TempDir()
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("version: 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := filepath.Join(wd, "services", "sku-master")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &listenRunner{wd: wd, lc: &config.ListenerConfig{ConfigsDir: "configs"}}

	want := filepath.Join(wd, "configs", "sku-master", "orchestrator.yaml")
	if _, err := r.configFor(svc); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("no config anywhere: got %v, want an error naming %s", err, want)
	}

	write(want)
	if got, err := r.configFor(svc); err != nil || got != want {
		t.Fatalf("configs_dir: got %q, %v; want %q", got, err, want)
	}

	own := filepath.Join(svc, "orchestrator.yaml")
	write(own)
	if got, err := r.configFor(svc); err != nil || got != own {
		t.Fatalf("own file: got %q, %v; want %q", got, err, own)
	}
}

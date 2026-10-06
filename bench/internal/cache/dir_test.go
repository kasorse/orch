package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDir(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()

	tests := []struct {
		name string
		xdg  string
		want Dir
	}{
		{"XDG_CACHE_HOME", xdg, Dir(filepath.Join(xdg, "orchbench"))},
		{"unset", "", Dir(filepath.Join(home, ".cache", "orchbench"))},
		{"relative is ignored", "relative/cache", Dir(filepath.Join(home, ".cache", "orchbench"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_CACHE_HOME", tt.xdg)
			if got := DefaultDir(); got != tt.want {
				t.Fatalf("DefaultDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDirPaths(t *testing.T) {
	d := Dir("/c")
	for got, want := range map[string]string{
		d.Mirrors():       "/c/mirrors",
		d.Work():          "/c/work",
		d.Logs():          "/c/logs",
		d.ValidatedFile(): "/c/validated.json",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestEnsure(t *testing.T) {
	d := Dir(filepath.Join(t.TempDir(), "cache"))
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{d.Mirrors(), d.Work(), d.Logs()} {
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("%s was not created: %v", p, err)
		}
	}
	if err := Dir("").Ensure(); err == nil {
		t.Error("an empty cache directory was accepted")
	}
}

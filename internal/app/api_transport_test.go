package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/provider/anthropic"
	"github.com/kasorse/orch/internal/provider/httpapi"
)

// providers.<p>.prices: the cache rates default to 0.1× and 1.25× input, an explicit 0 is kept.
func TestAPIPrices(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	got := apiPrices(map[string]config.ModelPrice{
		"defaults": {Input: f(4), Output: f(20)},
		"explicit": {Input: f(4), Output: f(20), CacheRead: f(0), CacheWrite: f(8)},
	})
	want := map[string]httpapi.Price{
		"defaults": {Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5},
		"explicit": {Input: 4, Output: 20, CacheRead: 0, CacheWrite: 8},
	}
	for model, w := range want {
		if g := got[model]; g.Input != w.Input || g.Output != w.Output || g.CacheRead != w.CacheRead || g.CacheWrite != w.CacheWrite || g.Long != nil {
			t.Errorf("%s = %+v, want %+v", model, g, w)
		}
	}
	if apiPrices(nil) != nil {
		t.Error("no prices must stay nil")
	}
}

// Skills are read only when an action with user_settings runs over the API transport (SPEC §5.4):
// otherwise the home directory is not even asked for.
func TestRegisterSkills(t *testing.T) {
	home := t.TempDir()
	skill := filepath.Join(home, ".claude", "skills", "graphify", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: graphify\ndescription: a graph\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.Actions.Investigate.Provider = "claude"
	cfg.Providers.Claude.Transport = config.TransportAPI
	asked := 0
	homeDir := func() (string, error) { asked++; return home, nil }
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, anthropic.New("", true, nil))
	if notes := registerSkills(registry, cfg, homeDir); notes != nil || asked != 0 {
		t.Fatalf("without user_settings: notes %v, home asked %d times", notes, asked)
	}
	cfg.Extensions.UserSettings = true
	cfg.Providers.Claude.Transport = ""
	if notes := registerSkills(registry, cfg, homeDir); notes != nil || asked != 0 {
		t.Fatalf("user_settings on the CLI: notes %v, home asked %d times", notes, asked)
	}
	cfg.Providers.Claude.Transport = config.TransportAPI
	notes := registerSkills(registry, cfg, homeDir)
	if asked != 1 || len(notes) != 1 || !strings.HasSuffix(notes[0], "the skill tool offers graphify") {
		t.Errorf("notes %v, home asked %d times", notes, asked)
	}
}

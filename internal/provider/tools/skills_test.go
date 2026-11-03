package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// home is an operator's ~/.claude: two skills of their own (one without front matter), a directory
// that is not a skill, a duplicate name, an enabled and a disabled plugin, and a hook.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	c := filepath.Join(h, ".claude")
	write(t, filepath.Join(c, "skills", "graphify", "SKILL.md"), "---\nname: graphify\ndescription: >\n  Turn a folder into\n  a knowledge graph.\n---\n\n# graphify\nRun graphify query.\n")
	write(t, filepath.Join(c, "skills", "graphify", "references", "query.md"), "query help\n")
	write(t, filepath.Join(c, "skills", "graphify", ".env"), "TOKEN=1\n")
	write(t, filepath.Join(c, "skills", "tailor-cv", "SKILL.md"), "# Tailor CV\n\nGenerate a CV.\n")
	write(t, filepath.Join(c, "skills", "synced", "notes.txt"), "not a skill\n")
	write(t, filepath.Join(c, "skills", "zz-copy", "SKILL.md"), "---\nname: graphify\ndescription: a copy\n---\n")
	write(t, filepath.Join(h, "outside.md"), "the operator's other file\n")
	on := filepath.Join(c, "plugins", "cache", "pony", "1.0")
	off := filepath.Join(c, "plugins", "cache", "off", "1.0")
	write(t, filepath.Join(on, "skills", "pony", "SKILL.md"), "---\nname: pony\ndescription: \"Lazy mode.\"\n---\nbody\n")
	write(t, filepath.Join(on, "hooks", "hooks.json"), "{}\n")
	write(t, filepath.Join(off, "skills", "secret", "SKILL.md"), "---\nname: secret\ndescription: off\n---\n")
	installed, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{
		"pony@market": []map[string]string{{"scope": "user", "installPath": on}},
		"off@market":  []map[string]string{{"scope": "user", "installPath": off}},
	}})
	write(t, filepath.Join(c, "plugins", "installed_plugins.json"), string(installed))
	write(t, filepath.Join(c, "settings.json"), `{"enabledPlugins": {"pony@market": true, "off@market": false}}`)
	return h
}

func TestDiscoverSkills(t *testing.T) {
	skills, notes := DiscoverSkills(home(t))
	got := map[string]string{}
	var names []string
	for _, s := range skills {
		got[s.Name] = s.Description
		names = append(names, s.Name)
	}
	if want := "graphify,tailor-cv,pony:pony"; strings.Join(names, ",") != want {
		t.Fatalf("skills = %v, want %s", names, want)
	}
	if got["graphify"] != "Turn a folder into a knowledge graph." || got["tailor-cv"] != "Tailor CV" || got["pony:pony"] != "Lazy mode." {
		t.Errorf("descriptions = %v", got)
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{`the name "graphify" is taken`, "hooks of ~/.claude"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes lack %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "secret") || strings.Contains(joined, "synced") {
		t.Errorf("a disabled plugin or a non-skill directory is noted:\n%s", joined)
	}
}

func TestDiscoverSkills_EmptyHome(t *testing.T) {
	if skills, notes := DiscoverSkills(t.TempDir()); len(skills) != 0 || len(notes) != 0 {
		t.Errorf("an empty home gave %v %v", skills, notes)
	}
}

func TestSkillTool(t *testing.T) {
	skills, _ := DiscoverSkills(home(t))
	h := filepath.Dir(filepath.Dir(filepath.Dir(skills[0].Dir)))
	// A symlink inside the skill pointing out of it.
	if err := os.Symlink(filepath.Join(h, "outside.md"), filepath.Join(skills[0].Dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	tool, ok := SkillTool(skills, []string{".env*"})
	if !ok || tool.Name != SkillName {
		t.Fatal("no skill tool")
	}
	if !strings.Contains(tool.Description, "- pony:pony: Lazy mode.") {
		t.Errorf("the description does not list the skills:\n%s", tool.Description)
	}
	set := NewSet([]Tool{tool})
	call := func(input string) (string, bool) {
		return set.Call(context.Background(), SkillName, json.RawMessage(input))
	}
	if out, isErr := call(`{"name":"graphify","path":null}`); isErr || !strings.Contains(out, "Run graphify query.") {
		t.Errorf("SKILL.md: %q %v", out, isErr)
	}
	if out, isErr := call(`{"name":"graphify","path":"references/query.md"}`); isErr || out != "query help\n" {
		t.Errorf("a reference: %q %v", out, isErr)
	}
	for _, in := range []string{
		`{"name":"graphify","path":"../tailor-cv/SKILL.md"}`,
		`{"name":"graphify","path":"../../../outside.md"}`,
		`{"name":"graphify","path":"` + filepath.Join(h, "outside.md") + `"}`,
		`{"name":"graphify","path":"link.md"}`,
		`{"name":"graphify","path":".env"}`,
		`{"name":"graphify","path":"references"}`,
		`{"name":"secret","path":null}`,
	} {
		if out, isErr := call(in); !isErr || strings.Contains(out, "TOKEN") || strings.Contains(out, "other file") {
			t.Errorf("%s: %q %v", in, out, isErr)
		}
	}
	if _, ok := SkillTool(nil, nil); ok {
		t.Error("a skill tool without skills")
	}
}

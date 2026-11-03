package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillName is the name of the skill tool (SPEC §5.4).
const SkillName = "skill"

// maxSkillDescription bounds one skill's description in the tool's definition.
const maxSkillDescription = 1024

// skillNameRE is what a skill may be called in the tool's enum: a plugin's skill is
// <plugin>:<skill>, as the claude CLI names it.
var skillNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}(:[A-Za-z0-9_.-]{1,64})?$`)

// Skill is one of the operator's skills: its name in the skill tool's enum, the description the
// model chooses by, and its directory, the root of what the tool reads for it.
type Skill struct {
	Name        string
	Description string
	Dir         string
}

// DiscoverSkills finds the operator's skills under home/.claude (SPEC §5.4): skills/*/SKILL.md, then
// the skills/*/SKILL.md of every plugin settings.json enables, at its user-scope install in
// plugins/installed_plugins.json, named <plugin>:<skill>. A skill's name and description come from
// its SKILL.md front matter (the directory's name and the first line of text without one). A skill
// whose name is taken or invalid, or whose SKILL.md cannot be read, is skipped with a note; so is an
// unreadable settings file. notes also says once when hooks are configured: nothing runs them over
// the API transport.
func DiscoverSkills(home string) (skills []Skill, notes []string) {
	claude := filepath.Join(home, ".claude")
	seen := map[string]bool{}
	add := func(prefix, dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				notes = append(notes, fmt.Sprintf("the skills in %s are skipped: the directory cannot be read", dir))
			}
			return
		}
		for _, e := range entries {
			skillDir := filepath.Join(dir, e.Name())
			if info, err := os.Stat(skillDir); err != nil || !info.IsDir() {
				continue
			}
			name, desc, err := readSkill(filepath.Join(skillDir, "SKILL.md"), e.Name())
			if errors.Is(err, fs.ErrNotExist) {
				continue // a directory without SKILL.md is not a skill
			}
			if err != nil {
				notes = append(notes, fmt.Sprintf("the skill in %s is skipped: %v", skillDir, err))
				continue
			}
			if prefix != "" {
				name = prefix + ":" + name
			}
			switch {
			case !skillNameRE.MatchString(name):
				notes = append(notes, fmt.Sprintf("the skill in %s is skipped: %q is not a usable name", skillDir, name))
			case seen[name]:
				notes = append(notes, fmt.Sprintf("the skill in %s is skipped: the name %q is taken", skillDir, name))
			default:
				seen[name] = true
				skills = append(skills, Skill{Name: name, Description: desc, Dir: skillDir})
			}
		}
	}
	add("", filepath.Join(claude, "skills"))

	hooks := false
	var settings struct {
		EnabledPlugins map[string]any  `json:"enabledPlugins"`
		Hooks          json.RawMessage `json:"hooks"`
	}
	if err := readJSON(filepath.Join(claude, "settings.json"), &settings); err != nil {
		notes = append(notes, fmt.Sprintf("the plugins' skills are skipped: %v", err))
	}
	if h := strings.TrimSpace(string(settings.Hooks)); h != "" && h != "null" && h != "{}" {
		hooks = true
	}
	var enabled []string
	for id, on := range settings.EnabledPlugins {
		if on == true {
			enabled = append(enabled, id)
		}
	}
	slices.Sort(enabled)
	if len(enabled) > 0 {
		var installed struct {
			Plugins map[string][]struct {
				Scope       string `json:"scope"`
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		if err := readJSON(filepath.Join(claude, "plugins", "installed_plugins.json"), &installed); err != nil {
			notes = append(notes, fmt.Sprintf("the plugins' skills are skipped: %v", err))
		}
		for _, id := range enabled {
			path := ""
			for _, inst := range installed.Plugins[id] {
				if (inst.Scope == "user" || inst.Scope == "") && filepath.IsAbs(inst.InstallPath) {
					path = inst.InstallPath
					break
				}
			}
			if path == "" {
				notes = append(notes, fmt.Sprintf("plugin %s is enabled but has no user install: its skills are skipped", id))
				continue
			}
			// shortcut: only the plugin's skills/ directory, not custom skill paths of its manifest;
			// read .claude-plugin/plugin.json when a plugin needs them.
			plugin, _, _ := strings.Cut(id, "@")
			add(plugin, filepath.Join(path, "skills"))
			if _, err := os.Stat(filepath.Join(path, "hooks", "hooks.json")); err == nil {
				hooks = true
			}
		}
	}
	if hooks {
		notes = append(notes, "the hooks of ~/.claude (settings.json, enabled plugins) do not run over the API transport")
	}
	return skills, notes
}

// readJSON decodes a settings file; a missing one is empty.
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s cannot be read", path)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s is not valid JSON", path)
	}
	return nil
}

// readSkill reads a SKILL.md's name and description: the YAML front matter's, else dirName and the
// first line of text.
func readSkill(path, dirName string) (name, desc string, err error) {
	if _, err := os.Stat(path); err != nil {
		return "", "", err
	}
	text, err := readText(path, "SKILL.md")
	if err != nil {
		return "", "", err
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		head, after, found := strings.Cut(rest, "\n---")
		if !found {
			return "", "", errors.New("SKILL.md's front matter is not closed")
		}
		var front struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(head), &front); err != nil {
			return "", "", errors.New("SKILL.md's front matter is not valid YAML")
		}
		name, desc, body = strings.TrimSpace(front.Name), front.Description, after
	}
	if name == "" {
		name = dirName
	}
	if strings.TrimSpace(desc) == "" {
		for _, l := range strings.Split(body, "\n") {
			if l = strings.TrimSpace(strings.TrimLeft(l, "#-")); l != "" {
				desc = l
				break
			}
		}
	}
	desc = strings.Join(strings.Fields(desc), " ")
	if r := []rune(desc); len(r) > maxSkillDescription {
		desc = string(r[:maxSkillDescription]) + "…"
	}
	return name, desc, nil
}

// SkillTool is the skill tool over skills (SPEC §5.4): name picks a skill, path a file inside its
// directory (null for its SKILL.md). The file is read under the rules of the repository tools with
// the skill's directory as the root and the call's deny list. ok is false without skills.
func SkillTool(skills []Skill, deny []string) (tool Tool, ok bool) {
	if len(skills) == 0 {
		return Tool{}, false
	}
	byName := map[string]Skill{}
	names := make([]string, 0, len(skills))
	var list strings.Builder
	for _, s := range skills {
		byName[s.Name] = s
		names = append(names, s.Name)
		fmt.Fprintf(&list, "\n- %s: %s", s.Name, s.Description)
	}
	return Tool{
		Name: SkillName,
		Description: "Load one of the operator's skills: instructions for a kind of task. When the task matches a " +
			"skill's description, call this with its name and follow what it says with the tools you have. You " +
			"cannot run commands: a step of a skill that needs one is not done, and you say so in your answer. " +
			"path reads another file of the skill's directory, relative to it (null for its SKILL.md).\n\nSkills:" +
			list.String(),
		Schema: object(map[string]any{
			"name": map[string]any{"type": "string", "enum": names, "description": "The skill's name"},
			"path": prop([]string{"string", "null"}, "A file of the skill's directory, relative to it; null for SKILL.md"),
		}),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Name string  `json:"name"`
				Path *string `json:"path"`
			}
			if err := decode(input, &in); err != nil {
				return "", err
			}
			s, ok := byName[in.Name]
			if !ok {
				return "", refuse("there is no skill %q", in.Name)
			}
			root, err := NewRoot(s.Dir, deny)
			if err != nil {
				return "", refuse("skill %s cannot be read", s.Name)
			}
			p := "SKILL.md"
			if in.Path != nil && strings.TrimSpace(*in.Path) != "" {
				p = *in.Path
			}
			abs, rel, err := root.Resolve(p, false)
			if err != nil {
				return "", refuse("skill %s: %s is not a readable file of the skill; use a path relative to its directory", s.Name, p)
			}
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				return "", refuse("skill %s: %s is a directory; name a file", s.Name, rel)
			}
			text, err := readText(abs, rel)
			if err != nil {
				return "", err
			}
			return bound(text), nil
		},
	}, true
}

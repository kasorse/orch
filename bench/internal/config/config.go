// Package config is the benchmark's own configuration (specs/benchmark/SPEC.md §8, §13.7): one
// file per config in bench/configs/, a shared common.yaml, and the rendering of an orchestrator
// config template for one task. It is not the orchestrator's configuration: that one is the
// text this package renders, and is read only by the orchestrator.
//
// Only a task's test command, its sandbox binds and its timeout reach a rendering, and the
// model from common.yaml. The oracle never does.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"orchbench/internal/lang"
	"orchbench/internal/task"
)

// The two kinds of config: bare Claude Code (A) and everything run through the orchestrator.
const (
	KindClaude       = "claude"
	KindOrchestrator = "orchestrator"
)

// CommonName is the file shared by all configs; it is not itself a config.
const CommonName = "common"

// Stages are the names `--stage` accepts, in the orchestrator's fixed order.
var Stages = []string{"research", "analysis", "investigation", "development", "testing", "review"}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Common is common.yaml: what every config shares.
type Common struct {
	// Model is the code-writing model: config A's --model and the orchestrator's implement.
	Model string `yaml:"model"`
}

// file is the YAML of one config.
type file struct {
	Kind         string   `yaml:"kind"`
	Stages       []string `yaml:"stages"`
	Config       string   `yaml:"config"`
	Tools        []string `yaml:"tools"`
	PromptSuffix string   `yaml:"prompt_suffix"`
	Setup        string   `yaml:"setup"`
}

// Config is one loaded benchmark config.
type Config struct {
	// Name is the file name without .yaml: what `--configs` and the result line call it.
	Name string
	Kind string
	// Stages become the --stage flags; empty means the decision chooses (B).
	Stages []string
	// Template is the orchestrator config template, kind orchestrator only.
	Template string
	// Tools and PromptSuffix are config A's --allowed-tools and the template of the line that
	// names the test command, kind claude only.
	Tools        []string
	PromptSuffix string
	// Setup is run in the workspace's sandbox, without network, after the task's
	// setup_command and before the config: what the config needs in the tree (a code graph).
	// Empty runs nothing.
	Setup string
	// Model is common.yaml's model.
	Model string
	// SHA256 is the hash of the config file's bytes.
	SHA256 string

	config *template.Template // Template, parsed
	suffix *template.Template // PromptSuffix, parsed
}

// Data is what a template is filled from. The same field names serve both templates of a
// config, but not the same text: see ConfigData and PromptData.
type Data struct {
	Model       string
	TestCommand string
	// RoBinds and RwBinds are YAML flow lists of strings, `[]` when empty.
	RoBinds string
	RwBinds string
	Timeout string
}

// Load reads <dir>/<name>.yaml and <dir>/common.yaml strictly: an unknown key, an empty file or
// more than one YAML document is an error, and so is a config that does not fit its kind.
func Load(dir, name string) (Config, error) {
	if !namePattern.MatchString(name) || name == CommonName {
		return Config{}, fmt.Errorf("config name %q must match [A-Za-z0-9_-]+ and not be %q", name, CommonName)
	}

	var common Common
	if _, err := decodeStrict(filepath.Join(dir, CommonName+".yaml"), &common); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(common.Model) == "" {
		return Config{}, fmt.Errorf("%s: model is required", filepath.Join(dir, CommonName+".yaml"))
	}

	path := filepath.Join(dir, name+".yaml")
	var f file
	data, err := decodeStrict(path, &f)
	if err != nil {
		return Config{}, err
	}

	c := Config{
		Name:         name,
		Kind:         f.Kind,
		Stages:       f.Stages,
		Template:     f.Config,
		Tools:        f.Tools,
		PromptSuffix: f.PromptSuffix,
		Setup:        f.Setup,
		Model:        common.Model,
		SHA256:       task.SHA256Bytes(data),
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Names lists the configs in dir (every .yaml but common.yaml), sorted.
func Names(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if e.IsDir() || !ok || name == CommonName {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func decodeStrict(path string, v any) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: empty file", path)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return nil, fmt.Errorf("%s: more than one YAML document", path)
	case !errors.Is(err, io.EOF):
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}

// probe is a task-shaped Data used to run the templates once at load time, so that a
// misspelled variable fails when the config is loaded and not in the middle of a benchmark.
var probe = Data{Model: "m", TestCommand: "t", RoBinds: "[]", RwBinds: "[]", Timeout: "1m0s"}

func (c *Config) validate() error {
	switch c.Kind {
	case KindClaude:
		switch {
		case len(c.Tools) == 0:
			return errors.New("kind claude needs tools")
		case len(c.Stages) > 0:
			return errors.New("kind claude takes no stages")
		case c.Template != "":
			return errors.New("kind claude takes no config template")
		}
		for _, tool := range c.Tools {
			if strings.TrimSpace(tool) == "" || strings.ContainsAny(tool, ", \t\n") {
				return fmt.Errorf("tools entry %q must be one tool name", tool)
			}
		}
		suffix, err := parse("prompt_suffix", c.PromptSuffix)
		if err != nil {
			return err
		}
		c.suffix = suffix
		return execute(suffix, probe, "prompt_suffix")

	case KindOrchestrator:
		switch {
		case strings.TrimSpace(c.Template) == "":
			return errors.New("kind orchestrator needs a config template")
		case len(c.Tools) > 0:
			return errors.New("kind orchestrator takes no tools")
		case c.PromptSuffix != "":
			return errors.New("kind orchestrator takes no prompt_suffix")
		}
		seen := map[string]bool{}
		for _, s := range c.Stages {
			if !slices.Contains(Stages, s) {
				return fmt.Errorf("unknown stage %q (known: %s)", s, strings.Join(Stages, ", "))
			}
			if seen[s] {
				return fmt.Errorf("stage %q is listed twice", s)
			}
			seen[s] = true
		}
		tmpl, err := parse("config", c.Template)
		if err != nil {
			return err
		}
		c.config = tmpl
		return execute(tmpl, probe, "config")

	case "":
		return errors.New("kind is required (claude or orchestrator)")
	default:
		return fmt.Errorf("kind %q must be claude or orchestrator", c.Kind)
	}
}

func parse(name, text string) (*template.Template, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("%s template: %w", name, err)
	}
	return t, nil
}

func execute(t *template.Template, data Data, name string) error {
	_, err := render(t, data)
	if err != nil {
		return fmt.Errorf("%s template: %w", name, err)
	}
	return nil
}

func render(t *template.Template, data Data) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// quoteYAML escapes s for the inside of a double-quoted YAML scalar, so that a template writes
// "{{.TestCommand}}" and a command with quotes, backslashes or newlines stays one string. JSON
// string escapes are valid YAML double-quoted escapes.
func quoteYAML(s string) string {
	j := marshalJSON(s)
	return j[1 : len(j)-1]
}

func flowList(items []string) string {
	if items == nil {
		items = []string{}
	}
	return marshalJSON(items)
}

// marshalJSON is json.Marshal without the HTML escaping, which would turn `&&` into `&&`
// in a test command. A string or a list of strings never fails to marshal.
func marshalJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// withTestCommand returns t with the test command of its language when the task leaves it out.
func withTestCommand(t task.Task) (task.Task, error) {
	if t.TestCommand == "" {
		d, _ := lang.Lookup(t.Language)
		t.TestCommand = d.TestCommand
	}
	if strings.TrimSpace(t.TestCommand) == "" {
		return t, fmt.Errorf("task %s has no test_command and language %q has no default", t.ID, t.Language)
	}
	return t, nil
}

// ConfigData is the data of the config template for t. The strings are escaped to be written
// inside double quotes (`command: "{{.TestCommand}}"`); the lists are complete YAML values
// (`ro_binds: {{.RoBinds}}`).
func (c Config) ConfigData(t task.Task) (Data, error) {
	t, err := withTestCommand(t)
	if err != nil {
		return Data{}, err
	}
	if t.Timeout <= 0 {
		return Data{}, fmt.Errorf("task %s has no timeout", t.ID)
	}
	return Data{
		Model:       quoteYAML(c.Model),
		TestCommand: quoteYAML(t.TestCommand),
		RoBinds:     flowList(t.Sandbox.RoBinds),
		RwBinds:     flowList(t.Sandbox.RwBinds),
		Timeout:     quoteYAML(t.Timeout.Std().String()),
	}, nil
}

// PromptData is the data of config A's prompt suffix for t: the plain text, not escaped.
func (c Config) PromptData(t task.Task) (Data, error) {
	t, err := withTestCommand(t)
	if err != nil {
		return Data{}, err
	}
	return Data{
		Model:       c.Model,
		TestCommand: t.TestCommand,
		RoBinds:     flowList(t.Sandbox.RoBinds),
		RwBinds:     flowList(t.Sandbox.RwBinds),
		Timeout:     t.Timeout.Std().String(),
	}, nil
}

// RenderConfig fills the orchestrator config template for t and returns the YAML with its
// SHA-256. The result is parsed as YAML before it is returned, so a value that broke the
// document is an error here and not an orchestrator exit code later.
func (c Config) RenderConfig(t task.Task) (text []byte, sha string, err error) {
	if c.Kind != KindOrchestrator || c.config == nil {
		return nil, "", fmt.Errorf("config %s is not an orchestrator config", c.Name)
	}
	data, err := c.ConfigData(t)
	if err != nil {
		return nil, "", err
	}
	out, err := render(c.config, data)
	if err != nil {
		return nil, "", fmt.Errorf("config %s: %w", c.Name, err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		return nil, "", fmt.Errorf("config %s rendered for task %s is not valid YAML: %w", c.Name, t.ID, err)
	}
	if len(doc) == 0 {
		return nil, "", fmt.Errorf("config %s rendered for task %s is empty", c.Name, t.ID)
	}
	return []byte(out), task.SHA256Bytes([]byte(out)), nil
}

// RenderPrompt is what config A is sent on stdin: the task text, then the rendered suffix on a
// line of its own.
func (c Config) RenderPrompt(t task.Task) (string, error) {
	if c.Kind != KindClaude || c.suffix == nil {
		return "", fmt.Errorf("config %s is not a claude config", c.Name)
	}
	data, err := c.PromptData(t)
	if err != nil {
		return "", err
	}
	suffix, err := render(c.suffix, data)
	if err != nil {
		return "", fmt.Errorf("config %s: %w", c.Name, err)
	}
	prompt := strings.TrimRight(t.Task, "\n")
	if suffix = strings.TrimSpace(suffix); suffix != "" {
		prompt += "\n\n" + suffix
	}
	return prompt + "\n", nil
}

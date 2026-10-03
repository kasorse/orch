package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"orchestrator/internal/apperr"
)

// Load reads and parses the YAML config file at path. It does not validate contents -
// callers must call Validate and ValidateDependencies before using the result.
//
// A version-2 file is decoded strictly: a key this build does not know - a typo such as
// "timout", or a key in the wrong section - is an error rather than silently ignored. Any
// other version is decoded leniently, because an older file is expected to carry keys
// that no longer exist, and Validate explains what changed.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, apperr.NewConfigError(fmt.Sprintf("failed to read config file %q", path), err)
	}

	var probe struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return Config{}, apperr.NewConfigError("failed to parse configuration YAML", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(probe.Version == CurrentVersion)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		if msg, ok := unknownKeysMessage(err); ok {
			return Config{}, apperr.NewConfigError(msg, nil)
		}
		return Config{}, apperr.NewConfigError("failed to parse configuration YAML", err)
	}

	return cfg, nil
}

// unknownFieldRE matches yaml.v3's report of a key the target type has no field for.
var unknownFieldRE = regexp.MustCompile(`^line (\d+): field (.+) not found in type `)

// unknownKeysMessage renders a decode error whose every problem is an unknown key as one
// readable list. yaml.v3 names the Go type the key was not found in, which means nothing
// to an operator, so only the line and the key are kept. ok is false for any other error.
func unknownKeysMessage(err error) (string, bool) {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) || len(typeErr.Errors) == 0 {
		return "", false
	}
	lines := make([]string, 0, len(typeErr.Errors))
	for _, e := range typeErr.Errors {
		m := unknownFieldRE.FindStringSubmatch(e)
		if m == nil {
			return "", false
		}
		lines = append(lines, fmt.Sprintf("  line %s: %s", m[1], m[2]))
	}
	return "configuration validation failed:\n\nunknown configuration keys (a typo, or a key in the wrong section):\n" +
		strings.Join(lines, "\n") + "\n\nsee orchestrator.example.yaml for every supported key", true
}

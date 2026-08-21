package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"orchestrator/internal/apperr"
)

// Load reads and parses the YAML config file at path. It does not validate contents -
// callers must call Validate and ValidateDependencies before using the result.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, apperr.NewConfigError(fmt.Sprintf("failed to read config file %q", path), err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, apperr.NewConfigError("failed to parse configuration YAML", err)
	}

	return cfg, nil
}

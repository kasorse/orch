// This file is the organization config layer (specs/governance/SPEC.md §6): an org file sets
// floors, ceilings and additions that the repository config can only tighten.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// OrgConfigEnv names the org file when the /etc one does not exist (SPEC §6.1).
const OrgConfigEnv = "ORCHESTRATOR_ORG_CONFIG"

// OrgVersion is the only org config version this build accepts.
const OrgVersion = 1

// OrgConfig is the org file: its own strict schema with only the keys of SPEC §6.2. Every
// section has an org-only struct, so a key the table does not list - thresholds,
// auto_commit, anything - is an unknown key.
type OrgConfig struct {
	Version       int         `yaml:"version"`
	Security      OrgSecurity `yaml:"security"`
	Test          OrgTest     `yaml:"test"`
	Policy        OrgPolicy   `yaml:"policy"`
	Limits        OrgLimits   `yaml:"limits"`
	AllowedModels []string    `yaml:"allowed_models"`
}

// OrgSecurity is security in the org file. The two booleans are pointers so an absent key is
// no floor; the only value either accepts is the strict one.
type OrgSecurity struct {
	RequireCleanWorktree *bool    `yaml:"require_clean_worktree"`
	ProtectedPaths       []string `yaml:"protected_paths"`
	AllowUnverifiedCLI   *bool    `yaml:"allow_unverified_cli"`
	// SecretScan is a pointer: the org owns allow_paths once it writes the section.
	SecretScan *OrgSecretScan `yaml:"secret_scan"`
}

// OrgSecretScan is security.secret_scan in the org file.
type OrgSecretScan struct {
	Mode       string   `yaml:"mode"`
	AllowPaths []string `yaml:"allow_paths"`
}

// OrgTest is test in the org file.
type OrgTest struct {
	Sandbox string `yaml:"sandbox"`
}

// OrgPolicy is policy in the org file: path rules and forced stages, no thresholds.
type OrgPolicy struct {
	Paths  []PathRuleConfig `yaml:"paths"`
	Stages OrgStages        `yaml:"stages"`
}

// OrgStages is policy.stages in the org file.
type OrgStages struct {
	Force map[string][]string `yaml:"force"`
}

// OrgLimits is limits in the org file.
type OrgLimits struct {
	MaxCostUSD *float64 `yaml:"max_cost_usd"`
}

// OrgSource is the org file a config was merged with, as the run records it.
type OrgSource struct {
	// Path is absolute.
	Path string
	// SHA256 is the hex digest of the file's bytes.
	SHA256 string
}

// FindOrgConfig returns the org file to load: etcPath if it exists, else the path named by
// ORCHESTRATOR_ORG_CONFIG, else none. A stat error other than "does not exist" counts as
// existing, so loading fails instead of falling back to the variable.
func FindOrgConfig(etcPath string, getenv func(string) string) (path string, ok bool) {
	if _, err := os.Stat(etcPath); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return etcPath, true
	}
	if p := getenv(OrgConfigEnv); p != "" {
		return p, true
	}
	return "", false
}

// LoadLayered loads the repository config and, when orgPath is not empty, the org file, and
// merges them per SPEC §6.2. Every conflict is reported at once in one ConfigError naming both
// files. An org file that cannot be read or parsed is an error: there is no fallback to "no
// org layer". It does not validate the merged config - callers call Validate, which also
// checks allowed_models.
func LoadLayered(repoPath, orgPath string) (Config, OrgSource, error) {
	cfg, err := Load(repoPath)
	if err != nil {
		return Config{}, OrgSource{}, err
	}
	if orgPath == "" {
		return cfg, OrgSource{}, nil
	}
	org, sum, err := loadOrg(orgPath)
	if err != nil {
		return Config{}, OrgSource{}, err
	}
	abs, err := filepath.Abs(orgPath)
	if err != nil {
		return Config{}, OrgSource{}, apperr.NewConfigError(fmt.Sprintf("org config %q", orgPath), err)
	}

	var problems []string
	add := func(p string) {
		if p != "" {
			problems = append(problems, p)
		}
	}
	add(mergeCleanWorktree(&cfg, org, repoPath, abs))
	mergeProtectedPaths(&cfg, org)
	add(mergeAllowUnverified(cfg, org, repoPath, abs))
	add(mergeSecretScanMode(cfg, org, repoPath, abs))
	add(mergeSecretScanAllowPaths(&cfg, org, repoPath, abs))
	add(mergeSandbox(cfg, org, repoPath, abs))
	mergePolicyPaths(&cfg, org)
	mergeForce(&cfg, org)
	add(mergeMaxCostUSD(&cfg, org, repoPath, abs))
	if len(problems) > 0 {
		return Config{}, OrgSource{}, apperr.NewConfigError(
			fmt.Sprintf("%s conflicts with the org config %s:\n\n%s", repoPath, abs, strings.Join(problems, "\n")), nil)
	}

	cfg.AllowedModels = slices.Clone(org.AllowedModels)
	if len(cfg.AllowedModels) > 0 {
		cfg.AllowedModelsFrom = abs
	}
	return cfg, OrgSource{Path: abs, SHA256: sum}, nil
}

// loadOrg reads and strictly decodes the org file at path, checks its own values, and returns
// it with the sha256 of its bytes. Every failure is a ConfigError naming path.
func loadOrg(path string) (OrgConfig, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OrgConfig{}, "", apperr.NewConfigError(fmt.Sprintf("failed to read org config file %q", path), err)
	}
	var probe struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return OrgConfig{}, "", apperr.NewConfigError(fmt.Sprintf("failed to parse org config %q", path), err)
	}
	if probe.Version != OrgVersion {
		return OrgConfig{}, "", apperr.NewConfigError(
			fmt.Sprintf("org config %q: version must be %d, got %d", path, OrgVersion, probe.Version), nil)
	}

	var org OrgConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&org); err != nil && !errors.Is(err, io.EOF) {
		if msg, ok := unknownKeysMessage(err); ok {
			return OrgConfig{}, "", apperr.NewConfigError(fmt.Sprintf("org config %q: %s", path, msg), nil)
		}
		return OrgConfig{}, "", apperr.NewConfigError(fmt.Sprintf("failed to parse org config %q", path), err)
	}
	if problems := orgProblems(org); len(problems) > 0 {
		return OrgConfig{}, "", apperr.NewConfigError(
			fmt.Sprintf("org config %q is invalid:\n\n%s", path, strings.Join(problems, "\n")), nil)
	}
	sum := sha256.Sum256(data)
	return org, hex.EncodeToString(sum[:]), nil
}

// orgProblems checks the org file's own values: it may only state the strict side of a floor.
func orgProblems(org OrgConfig) []string {
	var problems []string
	if v := org.Security.RequireCleanWorktree; v != nil && !*v {
		problems = append(problems, "security.require_clean_worktree may only be true: it is a floor, and false lowers it")
	}
	if v := org.Security.AllowUnverifiedCLI; v != nil && *v {
		problems = append(problems, "security.allow_unverified_cli may only be false: it is a floor, and true lowers it")
	}
	for i, p := range org.Security.ProtectedPaths {
		if err := domain.ValidateProtectedPattern(p); err != nil {
			problems = append(problems, fmt.Sprintf("security.protected_paths[%d] %q: %v", i, p, err))
		}
	}
	if s := org.Security.SecretScan; s != nil {
		if secretScanRank(s.Mode) == rankUnknown {
			problems = append(problems, fmt.Sprintf("security.secret_scan.mode must be off, warn or stop, got %q", s.Mode))
		}
		for i, p := range s.AllowPaths {
			if err := domain.ValidateProtectedPattern(p); err != nil {
				problems = append(problems, fmt.Sprintf("security.secret_scan.allow_paths[%d] %q: %v", i, p, err))
			}
		}
	}
	if s := org.Test.Sandbox; s != "" && s != SandboxBwrap {
		problems = append(problems, fmt.Sprintf("test.sandbox may only be %s: it is a floor, got %q", SandboxBwrap, s))
	}
	problems = append(problems, pathRuleProblems(Config{Policy: PolicyConfig{Paths: org.Policy.Paths}})...)
	for key, stages := range org.Policy.Stages.Force {
		if _, err := domain.ParseComplexity(key); err != nil {
			problems = append(problems, fmt.Sprintf("policy.stages.force has unknown complexity %q", key))
			continue
		}
		for _, s := range stages {
			if msg := forceStageProblem(key, s); msg != "" {
				problems = append(problems, msg)
			}
		}
	}
	if v := org.Limits.MaxCostUSD; v != nil && (*v <= 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
		problems = append(problems, fmt.Sprintf("limits.max_cost_usd must be positive and finite, got %v", *v))
	}
	if org.AllowedModels != nil {
		if len(org.AllowedModels) == 0 {
			problems = append(problems, "allowed_models must name at least one model")
		}
		seen := map[string]bool{}
		for i, m := range org.AllowedModels {
			switch {
			case strings.TrimSpace(m) == "":
				problems = append(problems, fmt.Sprintf("allowed_models[%d] must not be empty", i))
			case seen[m]:
				problems = append(problems, fmt.Sprintf("allowed_models[%d] %q is listed twice", i, m))
			}
			seen[m] = true
		}
	}
	return problems
}

// mergeCleanWorktree: an org true is a floor, so the repository may not set it false.
func mergeCleanWorktree(cfg *Config, org OrgConfig, repoPath, orgPath string) string {
	if org.Security.RequireCleanWorktree == nil || !*org.Security.RequireCleanWorktree {
		return ""
	}
	if r := cfg.Security.RequireCleanWorktree; r != nil && !*r {
		return fmt.Sprintf("%s: security.require_clean_worktree: false is weaker than %s", repoPath, orgPath)
	}
	t := true
	cfg.Security.RequireCleanWorktree = &t
	return ""
}

// mergeProtectedPaths: the org's patterns come first, then the repository's, without repeats.
func mergeProtectedPaths(cfg *Config, org OrgConfig) {
	cfg.Security.ProtectedPaths = unionStrings(org.Security.ProtectedPaths, cfg.Security.ProtectedPaths)
}

// mergeAllowUnverified: an org false is a floor, so the repository may not set it true.
func mergeAllowUnverified(cfg Config, org OrgConfig, repoPath, orgPath string) string {
	if org.Security.AllowUnverifiedCLI == nil || *org.Security.AllowUnverifiedCLI {
		return ""
	}
	if cfg.Security.AllowUnverifiedCLI {
		return fmt.Sprintf("%s: security.allow_unverified_cli: true is weaker than %s", repoPath, orgPath)
	}
	return ""
}

// rankUnknown is the rank of a secret_scan.mode that is not a mode.
const rankUnknown = -1

// secretScanRank orders the modes off < warn < stop; an empty mode is the default, stop.
func secretScanRank(mode string) int {
	switch mode {
	case SecretScanOff:
		return 0
	case SecretScanWarn:
		return 1
	case "", SecretScanStop:
		return 2
	}
	return rankUnknown
}

// mergeSecretScanMode: the org's mode is a floor; the repository may set it equal or
// stricter. A repository value that is not a mode is left to Validate.
func mergeSecretScanMode(cfg Config, org OrgConfig, repoPath, orgPath string) string {
	if org.Security.SecretScan == nil || org.Security.SecretScan.Mode == "" {
		return ""
	}
	floor, got := secretScanRank(org.Security.SecretScan.Mode), secretScanRank(cfg.Security.SecretScan.Mode)
	if got == rankUnknown || got >= floor {
		return ""
	}
	return fmt.Sprintf("%s: security.secret_scan.mode: %s is weaker than %s (%s)",
		repoPath, cfg.Security.SecretScan.Mode, orgPath, org.Security.SecretScan.Mode)
}

// mergeSecretScanAllowPaths: once the org writes secret_scan, it owns allow_paths: the
// repository may not set its own, and the merged list is the org's.
func mergeSecretScanAllowPaths(cfg *Config, org OrgConfig, repoPath, orgPath string) string {
	if org.Security.SecretScan == nil {
		return ""
	}
	if len(cfg.Security.SecretScan.AllowPaths) > 0 {
		return fmt.Sprintf("%s: security.secret_scan.allow_paths is set by the org config %s", repoPath, orgPath)
	}
	cfg.Security.SecretScan.AllowPaths = slices.Clone(org.Security.SecretScan.AllowPaths)
	return ""
}

// mergeSandbox: an org bwrap is a floor, so the repository may not set none.
func mergeSandbox(cfg Config, org OrgConfig, repoPath, orgPath string) string {
	if org.Test.Sandbox != SandboxBwrap {
		return ""
	}
	if cfg.Test.Sandbox == SandboxNone {
		return fmt.Sprintf("%s: test.sandbox: none is weaker than %s", repoPath, orgPath)
	}
	return ""
}

// mergePolicyPaths: the org's rules first, then the repository's.
func mergePolicyPaths(cfg *Config, org OrgConfig) {
	if len(org.Policy.Paths) == 0 {
		return
	}
	cfg.Policy.Paths = append(slices.Clone(org.Policy.Paths), cfg.Policy.Paths...)
}

// mergeForce: per complexity, the org's stages then the repository's, without repeats.
func mergeForce(cfg *Config, org OrgConfig) {
	if len(org.Policy.Stages.Force) == 0 {
		return
	}
	merged := make(map[string][]string, len(cfg.Policy.Stages.Force)+len(org.Policy.Stages.Force))
	for k, v := range cfg.Policy.Stages.Force {
		merged[k] = v
	}
	for k, v := range org.Policy.Stages.Force {
		merged[k] = unionStrings(v, cfg.Policy.Stages.Force[k])
	}
	cfg.Policy.Stages.Force = merged
}

// mergeMaxCostUSD: the org's budget is a ceiling. A repository without one inherits it; one
// above it is a conflict; a lower one is kept.
func mergeMaxCostUSD(cfg *Config, org OrgConfig, repoPath, orgPath string) string {
	ceiling := org.Limits.MaxCostUSD
	if ceiling == nil {
		return ""
	}
	switch r := cfg.Limits.MaxCostUSD; {
	case r == nil:
		v := *ceiling
		cfg.Limits.MaxCostUSD = &v
	case *r > *ceiling:
		return fmt.Sprintf("%s: limits.max_cost_usd %v is above the org's %v (%s)", repoPath, *r, *ceiling, orgPath)
	}
	return ""
}

// unionStrings is first then second, each value once, in order of appearance.
func unionStrings(first, second []string) []string {
	var out []string
	for _, s := range slices.Concat(first, second) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

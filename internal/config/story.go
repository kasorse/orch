// This file defines and validates the optional story section (specs/story/SPEC.md §5): where
// the checkouts of a story's services live and how large a story may grow.
package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Defaults and ranges of the story section.
const (
	DefaultStoryMaxServices = 5
	DefaultStorySpecDir     = "specs"
	DefaultStoryMaxTasks    = 12
	// DefaultStoryMaxParallel is one slot: the tasks run one at a time.
	DefaultStoryMaxParallel = 1
	// DefaultStoryMaxAcceptanceChars and MaxStoryMaxAcceptanceChars are story.MaxAcceptanceRunes
	// and story.MaxAcceptanceRunesCeiling (story_test.go keeps them equal).
	DefaultStoryMaxAcceptanceChars = 300
	MaxStoryMaxAcceptanceChars     = 2000
	minStoryMaxAcceptanceChars     = 50

	minStoryMaxServices = 1
	maxStoryMaxServices = 20
	minStoryMaxTasks    = 2
	maxStoryMaxTasks    = 30

	// MinStoryMaxParallel and MaxStoryMaxParallel bound story.max_parallel and story run --parallel.
	MinStoryMaxParallel = 1
	MaxStoryMaxParallel = 8
)

// StoryConfig is the story section. Absent keys take their defaults; an explicit 0 stays 0 and
// is refused by Validate. The section is optional: Config.Story is nil without it.
type StoryConfig struct {
	// Roots are the directories whose immediate subdirectories are the services' checkouts:
	// absolute, "~/"-prefixed, or relative to the story home. Read them through ResolveRoots.
	Roots       []string
	MaxServices int
	// SpecDir is where the spec of a story is written, relative to the story home.
	SpecDir  string
	MaxTasks int
	// MaxParallel is how many ready tasks story run runs at once.
	MaxParallel int
	// MaxAcceptanceChars bounds one acceptance item of a task file story run reads.
	MaxAcceptanceChars int
	// MaxCostUSD bounds the whole story, every call and run; nil means no budget.
	MaxCostUSD *float64
	// JiraIssueTypes are the Jira issue types run --jira refuses in favour of story plan.
	JiraIssueTypes []string
	// Push is false to end every task of a story at its commit, with no push and no merge
	// request, and to land the result on the base branch (specs/delivery-push, specs/story-land).
	// Read it through PushEnabled.
	Push *bool
}

// PushEnabled is story.push, true when absent (and without a story section).
func (s *StoryConfig) PushEnabled() bool { return s == nil || s.Push == nil || *s.Push }

// UnmarshalYAML applies the defaults for absent keys.
func (s *StoryConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Roots          []string `yaml:"roots"`
		MaxServices    *int     `yaml:"max_services"`
		SpecDir        *string  `yaml:"spec_dir"`
		MaxTasks       *int     `yaml:"max_tasks"`
		MaxParallel    *int     `yaml:"max_parallel"`
		MaxAcceptance  *int     `yaml:"max_acceptance_chars"`
		MaxCostUSD     *float64 `yaml:"max_cost_usd"`
		JiraIssueTypes []string `yaml:"jira_issue_types"`
		Push           *bool    `yaml:"push"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*s = StoryConfig{
		Roots:              raw.Roots,
		MaxServices:        DefaultStoryMaxServices,
		SpecDir:            DefaultStorySpecDir,
		MaxTasks:           DefaultStoryMaxTasks,
		MaxParallel:        DefaultStoryMaxParallel,
		MaxAcceptanceChars: DefaultStoryMaxAcceptanceChars,
		MaxCostUSD:         raw.MaxCostUSD,
		JiraIssueTypes:     raw.JiraIssueTypes,
		Push:               raw.Push,
	}
	if raw.MaxServices != nil {
		s.MaxServices = *raw.MaxServices
	}
	if raw.SpecDir != nil {
		s.SpecDir = *raw.SpecDir
	}
	if raw.MaxTasks != nil {
		s.MaxTasks = *raw.MaxTasks
	}
	if raw.MaxParallel != nil {
		s.MaxParallel = *raw.MaxParallel
	}
	if raw.MaxAcceptance != nil {
		s.MaxAcceptanceChars = *raw.MaxAcceptance
	}
	return nil
}

// ResolveRoots turns story.roots into existing directories: "~/" against the user's home
// directory, a relative root against the story home, symlinks resolved. A root that does not
// exist or is not a directory is an error naming it.
func (s StoryConfig) ResolveRoots(home string) ([]string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}
	return s.resolveRoots(home, userHome)
}

func (s StoryConfig) resolveRoots(storyHome, userHome string) ([]string, error) {
	out := make([]string, 0, len(s.Roots))
	var problems []string
	for i, r := range s.Roots {
		p := r
		switch {
		case p == "~" || strings.HasPrefix(p, "~/"):
			if userHome == "" {
				problems = append(problems, fmt.Sprintf("story.roots[%d] %q: the home directory is unknown", i, r))
				continue
			}
			p = filepath.Join(userHome, strings.TrimPrefix(p, "~"))
		case !filepath.IsAbs(p):
			p = filepath.Join(storyHome, p)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(p))
		if err != nil {
			problems = append(problems, fmt.Sprintf("story.roots[%d] %q: %v", i, r, err))
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			problems = append(problems, fmt.Sprintf("story.roots[%d] %q is not a directory", i, r))
			continue
		}
		out = append(out, resolved)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return out, nil
}

// validateStory checks the story section's shape (SPEC.md §5).
func validateStory(s StoryConfig) []string {
	var problems []string
	if s.MaxServices < minStoryMaxServices || s.MaxServices > maxStoryMaxServices {
		problems = append(problems, fmt.Sprintf("story.max_services must be between %d and %d, got %d",
			minStoryMaxServices, maxStoryMaxServices, s.MaxServices))
	}
	if s.MaxTasks < minStoryMaxTasks || s.MaxTasks > maxStoryMaxTasks {
		problems = append(problems, fmt.Sprintf("story.max_tasks must be between %d and %d, got %d",
			minStoryMaxTasks, maxStoryMaxTasks, s.MaxTasks))
	}
	if s.MaxParallel < MinStoryMaxParallel || s.MaxParallel > MaxStoryMaxParallel {
		problems = append(problems, fmt.Sprintf("story.max_parallel must be between %d and %d, got %d",
			MinStoryMaxParallel, MaxStoryMaxParallel, s.MaxParallel))
	}
	if s.MaxAcceptanceChars < minStoryMaxAcceptanceChars || s.MaxAcceptanceChars > MaxStoryMaxAcceptanceChars {
		problems = append(problems, fmt.Sprintf("story.max_acceptance_chars must be between %d and %d, got %d",
			minStoryMaxAcceptanceChars, MaxStoryMaxAcceptanceChars, s.MaxAcceptanceChars))
	}
	if v := s.MaxCostUSD; v != nil && (*v <= 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
		problems = append(problems, fmt.Sprintf("story.max_cost_usd must be positive and finite, got %v", *v))
	}
	for i, r := range s.Roots {
		if strings.TrimSpace(r) == "" {
			problems = append(problems, fmt.Sprintf("story.roots[%d] must not be empty", i))
		}
	}
	dir := filepath.ToSlash(filepath.Clean(s.SpecDir))
	if strings.TrimSpace(s.SpecDir) == "" || filepath.IsAbs(s.SpecDir) || dir == ".." || strings.HasPrefix(dir, "../") {
		problems = append(problems, fmt.Sprintf("story.spec_dir %q must be a relative path inside the story home", s.SpecDir))
	}
	for i, t := range s.JiraIssueTypes {
		if strings.TrimSpace(t) == "" {
			problems = append(problems, fmt.Sprintf("story.jira_issue_types[%d] must not be empty", i))
		}
	}
	return problems
}

// storyBudgetProblems refuses story.max_cost_usd when a provider a story's own calls route to
// reports no cost: the survey and specify run on actions.investigate, so the
// budget could then never be reached. The children's routes are their repositories' own.
func storyBudgetProblems(cfg Config, checker CostChecker) []string {
	if cfg.Story == nil || cfg.Story.MaxCostUSD == nil {
		return nil
	}
	var problems []string
	for _, a := range []struct{ name, provider string }{
		{"investigate", cfg.Actions.Investigate.Provider},
	} {
		if a.provider == "" || (checker != nil && checker.ReportsCost(a.provider)) {
			continue
		}
		problems = append(problems, fmt.Sprintf("story.max_cost_usd cannot be enforced: %s (actions.%s) reports no cost",
			a.provider, a.name))
	}
	return problems
}

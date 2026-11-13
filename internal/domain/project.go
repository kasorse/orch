package domain

// Project is one GitLab project of the catalog a listener's plan chooses its services from
// (specs/listener-story/SPEC.md §5.1).
type Project struct {
	// Path is the full path, "group/subgroup/name".
	Path string
	Name string
	// Description is one line, as GitLab gave it with its control characters removed.
	Description   string
	DefaultBranch string
}

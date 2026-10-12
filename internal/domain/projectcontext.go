package domain

// ProjectContext is the compact repository summary collected when a task's context
// depth is project (specs/JEV integration/SPEC.md §5). It holds summaries and
// constraints, never unrestricted source: it is sent to an external decision
// service, so secrets are filtered out before any of these fields are filled.
type ProjectContext struct {
	Language      string         `json:"language,omitempty"`
	ProjectType   string         `json:"project_type,omitempty"`
	Module        string         `json:"module,omitempty"`
	Rules         []string       `json:"rules,omitempty"`
	Docs          []DocExcerpt   `json:"docs,omitempty"`
	RelevantFiles []RelevantFile `json:"relevant_files,omitempty"`
	Dependencies  []string       `json:"dependencies,omitempty"`
	TestInfo      TestInfo       `json:"test_info"`
	Git           GitContext     `json:"git"`
	// Truncated is set when the context budget forced content to be dropped.
	Truncated bool `json:"truncated,omitempty"`
}

// DocExcerpt is the opening of one documentation section that mentions the request's
// terms (README.md, docs/*.md). Whole documents are never sent.
type DocExcerpt struct {
	Path    string `json:"path"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text"`
}

// Purpose values for RelevantFile. Path-based matching (T08) can only tell tests apart;
// the symbol-based roles are filled in by T17.
const (
	PurposeTest           = "test"
	PurposeInterface      = "interface"
	PurposeImplementation = "implementation"
	PurposeDependent      = "dependent"
)

// RelevantFile is a repository file judged related to the request.
type RelevantFile struct {
	Path string `json:"path"`
	// Purpose is the file's role relative to the request, e.g. "interface",
	// "implementation", "test", "dependent".
	Purpose string `json:"purpose,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Content is the file's text, clipped, only for a file the request names by its
	// path: the request then points at it instead of describing the task.
	Content string `json:"content,omitempty"`
}

type TestInfo struct {
	Exists           bool `json:"exists"`
	UnitTests        bool `json:"unit_tests"`
	IntegrationTests bool `json:"integration_tests"`
}

// GitContext carries repository state as paths only - never file contents.
type GitContext struct {
	Branch       string   `json:"branch,omitempty"`
	Dirty        bool     `json:"dirty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

package domain

// Authority is the fixed set of tools one provider call may use (specs/permissions/SPEC.md
// §5). It is chosen by Go code per call site - AuthorityFor for workflow actions,
// AuthorityNone passed explicitly by the translator and the LLM decision provider - never
// by a model, a provider default or config. No authority includes a shell: a step is
// restricted by the tools it is given, not by rules about how it may use them.
type Authority string

const (
	// AuthorityNone grants no tools: text in, text out.
	AuthorityNone Authority = "none"
	// AuthorityWeb grants web search and fetch, and no file access at all, so a step that
	// reads untrusted web content has no repository data to leak.
	AuthorityWeb Authority = "web"
	// AuthorityRead grants reading and searching files under the working directory.
	AuthorityRead Authority = "read"
	// AuthorityEdit grants AuthorityRead plus creating and modifying files under the
	// working directory. Compiling and testing is the test step's job, not the model's.
	AuthorityEdit Authority = "edit"
)

// Valid reports whether a is one of the four defined authorities. The zero value "" is not
// valid: a request that forgot to set its authority must be refused, not run with
// whatever "" happens to translate to.
func (a Authority) Valid() bool {
	switch a {
	case AuthorityNone, AuthorityWeb, AuthorityRead, AuthorityEdit:
		return true
	}
	return false
}

// actionAuthority is the fixed action -> authority table (SPEC.md §5.1). ActionTest has no
// row: the orchestrator runs the test command itself and makes no AI call for it.
var actionAuthority = map[ActionType]Authority{
	ActionResearch:    AuthorityWeb,
	ActionInvestigate: AuthorityRead,
	ActionReview:      AuthorityRead,
	ActionImplement:   AuthorityEdit,
}

// AuthorityFor returns the authority a workflow action's provider call runs with. An action
// without a row gets AuthorityNone, so an action added later without deciding its
// authority fails closed instead of inheriting access.
func AuthorityFor(action ActionType) Authority {
	if a, ok := actionAuthority[action]; ok {
		return a
	}
	return AuthorityNone
}

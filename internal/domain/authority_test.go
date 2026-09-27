package domain

import "testing"

// TestAuthorityFor pins the whole table, so widening a step's authority shows up in review
// as a test change.
func TestAuthorityFor(t *testing.T) {
	cases := map[ActionType]Authority{
		ActionResearch:  AuthorityWeb,
		ActionAnalyze:   AuthorityRead,
		ActionPlan:      AuthorityRead,
		ActionReview:    AuthorityRead,
		ActionImplement: AuthorityEdit,
		ActionTest:      AuthorityNone,
		"deploy":        AuthorityNone, // unknown actions fail closed
		"":              AuthorityNone,
	}
	for action, want := range cases {
		if got := AuthorityFor(action); got != want {
			t.Errorf("AuthorityFor(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestAuthorityValid(t *testing.T) {
	for _, a := range []Authority{AuthorityNone, AuthorityWeb, AuthorityRead, AuthorityEdit} {
		if !a.Valid() {
			t.Errorf("%q.Valid() = false, want true", a)
		}
	}
	for _, a := range []Authority{"", "all", "Read", "bypass"} {
		if a.Valid() {
			t.Errorf("%q.Valid() = true, want false", a)
		}
	}
}

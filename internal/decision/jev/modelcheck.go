package jev

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
)

// versionedModelRe matches a pinned model version. The API accepts versioned IDs but does
// not list them, so such a model passes the check whatever the list holds.
var versionedModelRe = regexp.MustCompile(`^jev-\d+\.\d+\.\d+$`)

// CheckModel asks Jev which models it serves (GET /v1/models) and verifies that model is
// one of them or a pinned version (TASKS.md T14). It runs after the fail-fast chain and
// before anything is decided, so a model Jev does not serve - and, as a side effect, a
// rejected key or an unreachable service - is reported before any AI call. A transport
// failure is returned as the client's *apperr.DecisionError; an unserved model is an
// *apperr.ConfigError.
func CheckModel(ctx context.Context, c Client, model string) error {
	models, err := c.listModels(ctx)
	if err != nil {
		return err
	}
	if versionedModelRe.MatchString(model) {
		return nil
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	if slices.Contains(names, model) {
		return nil
	}
	available := "none"
	if len(names) > 0 {
		available = strings.Join(names, ", ")
	}
	return &apperr.ConfigError{Msg: fmt.Sprintf(
		"jev decision provider: configured model %q is unavailable\n\n"+
			"Jev serves: %s, or a pinned version such as jev-1.13.0.\n"+
			"Set decision.model to one of them.", model, available)}
}

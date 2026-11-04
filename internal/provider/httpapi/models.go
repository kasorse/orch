package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
)

// maxModelsBody bounds one page of GET /v1/models; maxModelPages bounds the paging.
const (
	maxModelsBody = 4 << 20
	maxModelPages = 50
)

// CheckModels asks the endpoint which models it serves (GET /v1/models, every page) and
// verifies that each of ids is one of them (SPEC §9). The first-party APIs and LiteLLM all
// answer {"data": [{"id": ...}]}; Anthropic's pages go on with has_more and last_id. It runs in
// the fail-fast chain, so a model the endpoint does not serve - and, as a side effect, a
// refused key or an unreachable endpoint - is reported before any run. A missing model is an
// *apperr.ConfigError; anything else is the round trip's error or an invalid answer.
func CheckModels(ctx context.Context, ep Endpoint, provider string, ids []string) error {
	var served []string
	path := "/v1/models"
	for page := 0; ; page++ {
		if page == maxModelPages {
			return fmt.Errorf("%s: GET /v1/models: more than %d pages", provider, maxModelPages)
		}
		var answer struct {
			Data *[]struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := ep.getJSON(ctx, path, &answer); err != nil {
			return fmt.Errorf("%s: GET /v1/models: %w", provider, err)
		}
		if answer.Data == nil {
			return fmt.Errorf("%s: GET /v1/models: the answer has no data list", provider)
		}
		for i, m := range *answer.Data {
			if m.ID == "" {
				return fmt.Errorf("%s: GET /v1/models: data[%d] has no id", provider, i)
			}
			served = append(served, m.ID)
		}
		if !answer.HasMore {
			break
		}
		if answer.LastID == "" {
			return fmt.Errorf("%s: GET /v1/models: has_more without last_id", provider)
		}
		path = "/v1/models?limit=1000&after_id=" + url.QueryEscape(answer.LastID)
	}
	var missing []string
	for _, id := range ids {
		if !slices.Contains(served, id) && !slices.Contains(missing, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(served)
	list := "none"
	if len(served) > 0 {
		list = truncate(strings.Join(served, ", "), 2000)
	}
	return &apperr.ConfigError{Msg: fmt.Sprintf(
		"%s (transport: api): the endpoint does not serve %s\n\nIt serves: %s\nName one of them in the route, or fix providers.%s.base_url.",
		provider, strings.Join(missing, ", "), list, provider)}
}

func (ep Endpoint) getJSON(ctx context.Context, path string, out any) error {
	resp, err := ep.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody+1))
	if err != nil {
		return fmt.Errorf("reading the answer: %s", ep.Redact(err.Error()))
	}
	if len(raw) > maxModelsBody {
		return fmt.Errorf("the answer is over %d bytes", maxModelsBody)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the answer is not the expected JSON: %s", ep.Redact(err.Error()))
	}
	return nil
}

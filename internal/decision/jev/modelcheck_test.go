package jev

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
)

// modelsClient is a Client whose only working method is listModels.
type modelsClient struct {
	Client
	models []modelInfo
	err    error
}

func (c modelsClient) listModels(context.Context) ([]modelInfo, error) { return c.models, c.err }

func TestCheckModel(t *testing.T) {
	served := modelsClient{models: []modelInfo{{Name: "jev-latest"}, {Name: "jev-fast"}}}
	for _, tc := range []struct {
		name    string
		client  modelsClient
		model   string
		wantErr string
	}{
		{"listed alias", served, "jev-fast", ""},
		{"pinned version, not listed", served, "jev-1.13.0", ""},
		{"pinned version, empty list", modelsClient{}, "jev-2.0.10", ""},
		{"unlisted alias", served, "jev-turbo", `"jev-turbo" is unavailable`},
		{"not quite a version", served, "jev-1.13", `"jev-1.13" is unavailable`},
		{"empty list", modelsClient{}, "jev-latest", "Jev serves: none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckModel(context.Background(), tc.client, tc.model)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckModel = %v, want nil", err)
				}
				return
			}
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckModel = %v, want a ConfigError containing %q", err, tc.wantErr)
			}
		})
	}

	// A transport failure comes back unchanged, even for a pinned version: the request is
	// also the check that Jev can be reached with this key.
	auth := &apperr.DecisionError{Provider: ProviderName, Kind: apperr.DecisionAuth}
	if err := CheckModel(context.Background(), modelsClient{err: auth}, "jev-1.13.0"); err != auth {
		t.Errorf("CheckModel = %v, want the client's error", err)
	}
}

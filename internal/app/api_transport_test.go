package app

import (
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/provider/httpapi"
)

// providers.<p>.prices: the cache rates default to 0.1× and 1.25× input, an explicit 0 is kept.
func TestAPIPrices(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	got := apiPrices(map[string]config.ModelPrice{
		"defaults": {Input: f(4), Output: f(20)},
		"explicit": {Input: f(4), Output: f(20), CacheRead: f(0), CacheWrite: f(8)},
	})
	want := map[string]httpapi.Price{
		"defaults": {Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5},
		"explicit": {Input: 4, Output: 20, CacheRead: 0, CacheWrite: 8},
	}
	for model, w := range want {
		if g := got[model]; g.Input != w.Input || g.Output != w.Output || g.CacheRead != w.CacheRead || g.CacheWrite != w.CacheWrite || g.Long != nil {
			t.Errorf("%s = %+v, want %+v", model, g, w)
		}
	}
	if apiPrices(nil) != nil {
		t.Error("no prices must stay nil")
	}
}

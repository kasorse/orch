package story

import (
	"slices"
	"testing"
)

func TestNamed(t *testing.T) {
	cands := []string{"billing", "gateway", "api"}
	for _, tc := range []struct {
		text       string
		components []string
		want       []string
	}{
		{"… в сервисах billing и gateway.", nil, []string{"billing", "gateway"}},
		{"Billing, then more", nil, []string{"billing"}},
		{"billing-v2 only", nil, nil},
		{"prebilling", nil, nil},
		{"billing_old", nil, nil},
		{"billing2", nil, nil},
		{"биllingа", nil, nil},
		{"nothing", []string{"Gateway"}, []string{"gateway"}},
		{"(api)", nil, []string{"api"}},
	} {
		if got := Named(tc.text, tc.components, cands); !slices.Equal(got, tc.want) {
			t.Errorf("Named(%q, %v) = %v, want %v", tc.text, tc.components, got, tc.want)
		}
	}
}

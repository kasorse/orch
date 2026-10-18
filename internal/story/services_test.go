package story

import (
	"reflect"
	"testing"
)

func TestNamed(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		components []string
		candidates []string
		want       []string
	}{
		{"cyrillic sentence", "Добавить частичный возврат в сервисах billing и gateway.", nil, []string{"billing", "gateway", "ledger"}, []string{"billing", "gateway"}},
		{"capitalised, comma after", "Billing, then the rest", nil, []string{"billing"}, []string{"billing"}},
		{"hyphen after", "change billing-v2 only", nil, []string{"billing"}, nil},
		{"hyphen before", "the pre-billing step", nil, []string{"billing"}, nil},
		{"prefix letters", "prebilling is not it", nil, []string{"billing"}, nil},
		{"suffix letters", "billings", nil, []string{"billing"}, nil},
		{"underscore after", "billing_old", nil, []string{"billing"}, nil},
		{"underscore before", "old_billing", nil, []string{"billing"}, nil},
		{"digit after", "billing2", nil, []string{"billing"}, nil},
		{"digit before", "2billing", nil, []string{"billing"}, nil},
		{"cyrillic letter next to it", "биллингbilling", nil, []string{"billing"}, nil},
		{"cyrillic letter after it", "billingсервис", nil, []string{"billing"}, nil},
		{"the second occurrence counts", "prebilling and billing", nil, []string{"billing"}, []string{"billing"}},
		{"at the start and the end", "billing", nil, []string{"billing"}, []string{"billing"}},
		{"in quotes and brackets", `("billing") [gateway]`, nil, []string{"billing", "gateway"}, []string{"billing", "gateway"}},
		{"a dot ends the word", "billing.", nil, []string{"billing"}, []string{"billing"}},
		{"a dotted name", "see my.svc and more", nil, []string{"my.svc"}, []string{"my.svc"}},
		{"a hyphenated name", "see api-gateway now", nil, []string{"api-gateway", "api"}, []string{"api-gateway"}},
		{"a component names a candidate", "no services here", []string{"Gateway"}, []string{"gateway", "billing"}, []string{"gateway"}},
		{"a component of another spelling", "", []string{" BILLING "}, []string{"billing"}, []string{"billing"}},
		{"a component is equality, not a word", "", []string{"billing platform"}, []string{"billing"}, nil},
		{"text and component name the same one once", "billing", []string{"Billing"}, []string{"billing"}, []string{"billing"}},
		{"the candidate's own casing is returned", "BILLING", nil, []string{"Billing"}, []string{"Billing"}},
		{"duplicates collapse", "billing gateway", nil, []string{"gateway", "billing", "billing", "gateway"}, []string{"billing", "gateway"}},
		{"sorted", "zeta alpha mid", nil, []string{"zeta", "mid", "alpha"}, []string{"alpha", "mid", "zeta"}},
		{"an empty candidate is never named", "anything", []string{""}, []string{""}, nil},
		{"no text", "", nil, []string{"billing"}, nil},
		{"no candidates", "billing", []string{"billing"}, nil, nil},
		{"non-ASCII names fold", "Сервис БИЛЛИНГ работает", nil, []string{"биллинг"}, []string{"биллинг"}},
		{"regexp characters are plain", "a+b and a.b", nil, []string{"a.b"}, []string{"a.b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Named(tc.text, tc.components, tc.candidates)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Named(%q, %q, %q) = %q, want %q", tc.text, tc.components, tc.candidates, got, tc.want)
			}
		})
	}
}

func TestNamedIsDeterministic(t *testing.T) {
	candidates := []string{"gamma", "alpha", "beta", "delta"}
	first := Named("alpha beta gamma delta", nil, candidates)
	for i := 0; i < 20; i++ {
		if got := Named("alpha beta gamma delta", nil, candidates); !reflect.DeepEqual(got, first) {
			t.Fatalf("%v != %v", got, first)
		}
	}
	if !reflect.DeepEqual(first, []string{"alpha", "beta", "delta", "gamma"}) {
		t.Errorf("got %v", first)
	}
}

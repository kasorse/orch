package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// Price is a model's price in USD per million tokens. Long, when set, replaces it for a
// request whose prompt (uncached, cache read and cache write tokens) is over LongAbove.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
	LongAbove                            int64
	Long                                 *Price
}

// Cost is the price of one response's usage. Reasoning tokens are output tokens.
func (p Price) Cost(u domain.Usage) float64 {
	if p.Long != nil && u.InputTokens+u.CacheReadTokens+u.CacheCreationTokens > p.LongAbove {
		p = *p.Long
	}
	return (float64(u.InputTokens)*p.Input + float64(u.CacheReadTokens)*p.CacheRead +
		float64(u.CacheCreationTokens)*p.CacheWrite + float64(u.OutputTokens+u.ReasoningTokens)*p.Output) / 1e6
}

// PricesTaken is when the built-in table was copied from the providers' pricing pages.
const PricesTaken = "2026-11-01"

// anthropic is a first-party Claude price: cache reads as listed, cache writes 1.25× input
// (the 5-minute cache the transport writes).
func anthropic(in, out, read float64) Price {
	return Price{Input: in, Output: out, CacheRead: read, CacheWrite: 1.25 * in}
}

// openai is a first-party OpenAI price; a model whose page lists no cache write is billed no
// extra for one (write = input).
func openai(in, read, write, out float64) Price {
	return Price{Input: in, Output: out, CacheRead: read, CacheWrite: write}
}

func long(short Price, above int64, p Price) Price {
	short.LongAbove, short.Long = above, &p
	return short
}

// builtin holds the first-party models of the two providers' pricing pages (PricesTaken).
// A gateway's names (bedrock/…) are not here: their prices differ, so the operator writes
// them in providers.<p>.prices.
var builtin = map[string]Price{
	"claude-fable-5-1":  anthropic(10, 50, 0.25),
	"claude-mythos-5-1": anthropic(10, 50, 0.25),
	"claude-fable-5":    anthropic(10, 50, 1),
	"claude-opus-5-5":   anthropic(4, 20, 0.20),
	"claude-opus-5":     anthropic(5, 25, 0.50),
	"claude-opus-4-8":   anthropic(5, 25, 0.50),
	"claude-opus-4-7":   anthropic(5, 25, 0.50),
	"claude-opus-4-6":   anthropic(5, 25, 0.50),
	"claude-sonnet-5-5": anthropic(2, 10, 0.20),
	"claude-sonnet-5":   anthropic(2, 10, 0.20),
	"claude-sonnet-4-6": anthropic(3, 15, 0.30),
	"claude-haiku-5-5":  long(anthropic(0.10, 0.50, 0.01), 100_000, anthropic(0.50, 2.50, 0.05)),
	"claude-haiku-4-5":  anthropic(1, 5, 0.10),

	"gpt-6-astra":   long(openai(10, 1, 12.5, 50), 272_000, openai(20, 2, 25, 75)),
	"gpt-6.1-sol":   long(openai(2, 0.10, 2.5, 10), 272_000, openai(4, 0.20, 5, 15)),
	"gpt-6.1-terra": long(openai(1, 0.05, 1.25, 5), 272_000, openai(2, 0.10, 2.5, 7.5)),
	"gpt-6-luna":    long(openai(0.10, 0.01, 0.125, 0.50), 272_000, openai(0.20, 0.02, 0.25, 0.75)),
	"gpt-6-sol":     long(openai(2, 0.20, 2.5, 10), 272_000, openai(4, 0.40, 5, 15)),
	"gpt-5.6-sol":   long(openai(4, 0.40, 5, 20), 272_000, openai(8, 0.80, 10, 30)),
	"gpt-5.6-terra": long(openai(2, 0.20, 2.5, 12), 272_000, openai(4, 0.40, 5, 18)),
	"gpt-5.6-luna":  long(openai(0.20, 0.02, 0.25, 1.20), 272_000, openai(0.40, 0.04, 0.50, 1.80)),
	"gpt-5.5":       long(openai(5, 0.50, 5, 30), 272_000, openai(10, 1, 10, 45)),
	"gpt-5.5-pro":   long(openai(30, 30, 30, 180), 272_000, openai(60, 60, 60, 270)),
	"gpt-5.4":       long(openai(2.5, 0.25, 2.5, 15), 272_000, openai(5, 0.50, 5, 22.5)),
	"gpt-5.4-pro":   long(openai(30, 30, 30, 180), 272_000, openai(60, 60, 60, 270)),
	"gpt-5.4-mini":  openai(0.75, 0.075, 0.75, 4.50),
	"gpt-5.4-nano":  openai(0.20, 0.02, 0.20, 1.25),
	"gpt-5.3-codex": openai(1.75, 0.175, 1.75, 14),
	"gpt-5.2":       openai(1.75, 0.175, 1.75, 14),
	"gpt-5.1":       openai(1.25, 0.125, 1.25, 10),
	"gpt-5":         openai(1.25, 0.125, 1.25, 10),
	"gpt-5-mini":    openai(0.25, 0.025, 0.25, 2),
	"gpt-5-nano":    openai(0.05, 0.005, 0.05, 0.40),
}

// Lookup is the price of model: the operator's override (providers.<p>.prices) first, then
// the built-in table.
func Lookup(overrides map[string]Price, model string) (Price, bool) {
	if p, ok := overrides[model]; ok {
		return p, true
	}
	p, ok := builtin[model]
	return p, ok
}

// GatewayCost is the cost a LiteLLM gateway reports for a response (x-litellm-response-cost),
// when the header holds a finite number >= 0.
func GatewayCost(h http.Header) (float64, bool) {
	v := strings.TrimSpace(h.Get("x-litellm-response-cost"))
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

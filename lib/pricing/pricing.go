// Package pricing turns the tokens a model call used into what it costs at
// the model provider's list prices: the API-equivalent cost, which a
// subscription doesn't charge.
//
// The built-in prices (prices.json) are Anthropic's and OpenAI's list prices
// per million tokens as Pi 1.3.0's model registry has them (October 2026).
// A prices file (--prices) adds models or replaces their prices.
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

//go:embed prices.json
var builtinJSON []byte

// Price is a model's price per million tokens.
type Price struct {
	// InputTokensAbove is set on a tier: the price applies to a call whose
	// input (uncached, cached and written to the cache) is above it.
	InputTokensAbove int     `json:"input_tokens_above,omitempty"`
	Input            float64 `json:"input" doc:"Uncached input tokens."`
	Output           float64 `json:"output" doc:"Output tokens, thinking included."`
	CacheRead        float64 `json:"cache_read" doc:"Input tokens read from the prompt cache."`
	CacheWrite       float64 `json:"cache_write" doc:"Input tokens written to the prompt cache (Anthropic's 5-minute cache)."`
	// Tiers are the prices of calls with a long input, the lowest first.
	Tiers []Price `json:"tiers,omitempty"`
}

// Tokens is the tokens one model call used.
type Tokens struct {
	Input      int `json:"input" doc:"Uncached input tokens."`
	CacheRead  int `json:"cache_read" doc:"Input tokens read from the prompt cache."`
	CacheWrite int `json:"cache_write" doc:"Input tokens written to the prompt cache."`
	// CacheWrite1h is Anthropic's one-hour cache, written at twice the
	// input price.
	CacheWrite1h int `json:"cache_write_1h,omitempty" doc:"Input tokens written to Anthropic's one-hour prompt cache."`
	Output       int `json:"output" doc:"Output tokens, thinking included."`
}

// Add returns the sum of u and o.
func (u Tokens) Add(o Tokens) Tokens {
	return Tokens{
		Input:        u.Input + o.Input,
		CacheRead:    u.CacheRead + o.CacheRead,
		CacheWrite:   u.CacheWrite + o.CacheWrite,
		CacheWrite1h: u.CacheWrite1h + o.CacheWrite1h,
		Output:       u.Output + o.Output,
	}
}

// IsZero reports whether no token was used.
func (u Tokens) IsZero() bool { return u == Tokens{} }

// contextTokens is the input of the call, which picks the price tier.
func (u Tokens) contextTokens() int {
	return u.Input + u.CacheRead + u.CacheWrite + u.CacheWrite1h
}

// Cost is what one call with usage u costs at p, in USD.
func (p Price) Cost(u Tokens) float64 {
	tier := p
	for _, t := range p.Tiers {
		if u.contextTokens() > t.InputTokensAbove {
			tier = t
		}
	}
	return (float64(u.Input)*tier.Input +
		float64(u.CacheRead)*tier.CacheRead +
		float64(u.CacheWrite)*tier.CacheWrite +
		float64(u.CacheWrite1h)*2*tier.Input +
		float64(u.Output)*tier.Output) / 1e6
}

// Table maps model IDs to their prices.
type Table struct {
	prices map[string]Price
}

// Builtin returns the built-in prices.
func Builtin() *Table {
	t, err := parse(builtinJSON)
	if err != nil {
		panic(fmt.Sprintf("pricing: the built-in prices don't parse: %v", err))
	}
	return t
}

func parse(data []byte) (*Table, error) {
	prices := map[string]Price{}
	if err := json.Unmarshal(data, &prices); err != nil {
		return nil, err
	}
	normalized := make(map[string]Price, len(prices))
	for id, p := range prices {
		slices.SortFunc(p.Tiers, func(a, b Price) int { return a.InputTokensAbove - b.InputTokensAbove })
		normalized[strings.ToLower(id)] = p
	}
	return &Table{prices: normalized}, nil
}

// WithFile returns t with the prices in the JSON file at path added, in
// the format of the built-in prices: a model ID's price replaces t's.
func (t *Table) WithFile(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	extra, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	merged := make(map[string]Price, len(t.prices)+len(extra.prices))
	for id, p := range t.prices {
		merged[id] = p
	}
	for id, p := range extra.prices {
		merged[id] = p
	}
	return &Table{prices: merged}, nil
}

// dateSuffix ends a model ID pinned to a release: -20251001, -2026-09-01.
var dateSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// Lookup returns the price of model: its own, else that of the model it
// pins or extends (claude-opus-4-5-20251101 is claude-opus-4-5,
// gpt-6.1-sol-mini-x a gpt-6.1-sol if nothing closer is known). A provider
// prefix (anthropic/) and a context suffix ([1m]) are ignored.
func (t *Table) Lookup(model string) (Price, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.Index(id, "["); i >= 0 {
		id = id[:i]
	}
	if p, ok := t.prices[id]; ok {
		return p, true
	}
	if p, ok := t.prices[dateSuffix.ReplaceAllString(id, "")]; ok {
		return p, true
	}
	best := ""
	for known := range t.prices {
		if strings.HasPrefix(id, known+"-") && len(known) > len(best) {
			best = known
		}
	}
	if best == "" {
		return Price{}, false
	}
	return t.prices[best], true
}

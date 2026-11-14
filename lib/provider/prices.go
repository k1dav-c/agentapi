package provider

import (
	"fmt"
	"math"
	"strings"

	"github.com/coder/agentapi/lib/pricing"
)

// SourceManual is the source of a price written in the config file.
const SourceManual = "manual"

// ModelPrice is a provider model's price per million tokens, and where it
// came from: SourceManual, or the model it is the list price of, such as
// openrouter:moonshotai/kimi-k3.
type ModelPrice struct {
	Input      float64 `json:"input" yaml:"input" doc:"USD per million uncached input tokens."`
	Output     float64 `json:"output" yaml:"output" doc:"USD per million output tokens, thinking included."`
	CacheRead  float64 `json:"cache_read" yaml:"cache_read,omitempty" doc:"USD per million input tokens read from the prompt cache."`
	CacheWrite float64 `json:"cache_write" yaml:"cache_write,omitempty" doc:"USD per million input tokens written to the prompt cache."`
	Source     string  `json:"source,omitempty" yaml:"-" doc:"Where the price comes from: manual, or openrouter:<model> for an OpenRouter model's list price."`
}

// Pricing is p as lib/pricing prices a call with it.
func (p ModelPrice) Pricing() pricing.Price {
	return pricing.Price{Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite}
}

// validate returns an ErrInvalid for a price below zero or not a number.
func (p ModelPrice) validate(providerID, model string) error {
	for _, v := range [...]float64{p.Input, p.Output, p.CacheRead, p.CacheWrite} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: the price of %s's model %s must be numbers of 0 or more", ErrInvalid, providerID, model)
		}
	}
	return nil
}

// Price returns the price of the provider's model, if it has one: the
// model's own ID first, else one that differs only in case.
func (p Provider) Price(model string) (ModelPrice, bool) {
	if price, ok := p.Prices[model]; ok {
		return price, true
	}
	for id, price := range p.Prices {
		if strings.EqualFold(id, model) {
			return price, true
		}
	}
	return ModelPrice{}, false
}

// PriceTable is the model prices of the providers, by provider ID.
type PriceTable map[string]map[string]ModelPrice

// Lookup returns the price of the model of provider id, if it has one.
func (t PriceTable) Lookup(id, model string) (ModelPrice, bool) {
	prices, ok := t[id]
	if !ok {
		return ModelPrice{}, false
	}
	return Provider{Prices: prices}.Price(model)
}

// Prices returns the model prices of the providers the source offers (as
// List has them): those written in the config file and those the hub
// sends.
func (s *Source) Prices() PriceTable {
	var table PriceTable
	for _, p := range s.List() {
		if len(p.Prices) == 0 {
			continue
		}
		if table == nil {
			table = PriceTable{}
		}
		table[p.ID] = p.Prices
	}
	return table
}

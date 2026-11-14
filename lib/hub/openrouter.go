package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/provider"
)

// OpenRouterModelsURL is OpenRouter's public list of models with their
// prices, which needs no key.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// openRouterTimeout bounds a fetch of OpenRouter's list.
const openRouterTimeout = 30 * time.Second

// maxOpenRouterList is the largest list the hub reads (about 1 MB in
// November 2026).
const maxOpenRouterList = 32 << 20

// openRouterSource prefixes the source of a price that is an OpenRouter
// model's (provider.ModelPrice.Source).
const openRouterSource = "openrouter:"

// openRouterModel is a model of OpenRouter's list, with its list price.
type openRouterModel struct {
	ID    string              `json:"id" doc:"The OpenRouter model ID, e.g. moonshotai/kimi-k3."`
	Name  string              `json:"name" doc:"OpenRouter's name of the model."`
	Price provider.ModelPrice `json:"price" doc:"The model's list price on OpenRouter, in USD per million tokens."`
}

// fetchOpenRouter gets OpenRouter's models from url with client, within
// openRouterTimeout.
func fetchOpenRouter(ctx context.Context, client *http.Client, url string) ([]openRouterModel, error) {
	ctx, cancel := context.WithTimeout(ctx, openRouterTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterList+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxOpenRouterList {
		return nil, errors.New("OpenRouter's list of models is too large")
	}
	return parseOpenRouter(data)
}

// parseOpenRouter reads OpenRouter's list of models: pricing has USD per
// token as strings (prompt, completion, input_cache_read,
// input_cache_write), "0" or missing for none. A model without a prompt
// and completion price, or with one below zero (a router, whose price
// depends on the model it picks), is left out.
func parseOpenRouter(data []byte) ([]openRouterModel, error) {
	var body struct {
		Data []struct {
			ID      string                     `json:"id"`
			Name    string                     `json:"name"`
			Pricing map[string]json.RawMessage `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("read OpenRouter's models: %w", err)
	}
	models := make([]openRouterModel, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID == "" {
			continue
		}
		input, inputOK := perMillion(m.Pricing["prompt"])
		output, outputOK := perMillion(m.Pricing["completion"])
		cacheRead, readOK := perMillion(m.Pricing["input_cache_read"])
		cacheWrite, writeOK := perMillion(m.Pricing["input_cache_write"])
		if !inputOK || !outputOK || input < 0 || output < 0 || (readOK && cacheRead < 0) || (writeOK && cacheWrite < 0) {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		models = append(models, openRouterModel{ID: m.ID, Name: name, Price: provider.ModelPrice{
			Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
		}})
	}
	if len(models) == 0 {
		return nil, errors.New("OpenRouter listed no models with prices")
	}
	return models, nil
}

// perMillion is a price per token, a JSON string or number, per million
// tokens, without the float's noise (0.000000192 is 0.192, not
// 0.19200000000000003).
func perMillion(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		text = string(raw)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	v, _ = strconv.ParseFloat(strconv.FormatFloat(v*1e6, 'g', 12, 64), 64)
	return v, true
}

// precisionSuffixes end a model name that is another's quantized, or at
// another precision: a gateway's Kimi-K3-NVFP4 is OpenRouter's kimi-k3.
var precisionSuffixes = []string{"nvfp4", "fp8", "fp4", "int4", "int8", "awq", "gptq", "bf16", "fp16"}

// normalizeModel is the name a model is matched by: lowercase, without a
// vendor prefix up to the last '/' and a trailing precision suffix (-fp8,
// _awq).
func normalizeModel(id string) string {
	name := strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	for trimmed := true; trimmed; {
		trimmed = false
		for _, suffix := range precisionSuffixes {
			for _, sep := range []string{"-", "_"} {
				if rest, ok := strings.CutSuffix(name, sep+suffix); ok && rest != "" {
					name, trimmed = rest, true
				}
			}
		}
	}
	return name
}

// autoMatchable reports whether an OpenRouter model can be matched by its
// name alone: not a variant (moonshotai/kimi-k3:batch, …:free) nor an
// alias that follows the latest model (~moonshotai/kimi-latest).
func autoMatchable(id string) bool {
	return !strings.Contains(id, ":") && !strings.HasPrefix(id, "~")
}

// openRouterIndex finds the models of a list by their ID and by the name
// they are matched by.
type openRouterIndex struct {
	models []openRouterModel
	byID   map[string]int
	byName map[string][]int
}

func newOpenRouterIndex(models []openRouterModel) openRouterIndex {
	index := openRouterIndex{models: models, byID: make(map[string]int, len(models)), byName: make(map[string][]int, len(models))}
	for i, m := range models {
		index.byID[m.ID] = i
		if autoMatchable(m.ID) {
			name := normalizeModel(m.ID)
			index.byName[name] = append(index.byName[name], i)
		}
	}
	return index
}

// lookup returns the model with the ID.
func (x openRouterIndex) lookup(id string) (openRouterModel, bool) {
	i, ok := x.byID[id]
	if !ok {
		return openRouterModel{}, false
	}
	return x.models[i], true
}

// match returns the one model whose name matches model's, normalized;
// none when several do.
func (x openRouterIndex) match(model string) (openRouterModel, bool) {
	found := x.byName[normalizeModel(model)]
	if len(found) != 1 {
		return openRouterModel{}, false
	}
	return x.models[found[0]], true
}

// maxSearchResults is how many models a search answers.
const maxSearchResults = 50

// search returns the models whose ID or name has every word of query, in
// the list's order, at most limit.
func (x openRouterIndex) search(query string, limit int) []openRouterModel {
	words := strings.Fields(strings.ToLower(query))
	out := []openRouterModel{}
	for _, m := range x.models {
		text := strings.ToLower(m.ID + " " + m.Name)
		all := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				all = false
				break
			}
		}
		if all {
			out = append(out, m)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

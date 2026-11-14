package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/quartz"
	"golang.org/x/sync/singleflight"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/provider"
)

// The hub prices its providers' models at OpenRouter's list prices: it
// gets OpenRouter's list once a day (and when the editor asks), keeps the
// last good one in openrouter.json beside providers.json so that it works
// offline, and matches each model to an OpenRouter model by name, unless
// providers.json has a choice for it (an OpenRouter model, or none). The
// providers' models are those they define, or, for a provider that lists
// its own, those it listed at the last refresh.

// priceRefreshEvery is how old OpenRouter's list gets before the hub gets
// it again; priceRetryEvery how long it waits after a failed try.
const (
	priceRefreshEvery = 24 * time.Hour
	priceRetryEvery   = time.Hour
)

// openRouterNone is the choice of no price for a model.
const openRouterNone = "none"

// The limits of a provider's price choices.
const (
	maxPriceChoices  = 500
	maxPriceModelID  = 256
	maxOpenRouterID  = 200
	priceCacheFormat = 1
)

// priceChoice is how a model's price is picked.
type priceChoice string

const (
	// priceAuto: the OpenRouter model whose name matches (match).
	priceAuto priceChoice = "auto"
	// priceChosen: the OpenRouter model providers.json names.
	priceChosen priceChoice = "chosen"
	// priceNone: no price, as providers.json says.
	priceNone priceChoice = "none"
)

// providerModelPrice is the price the hub has for a provider's model.
type providerModelPrice struct {
	Model          string               `json:"model" doc:"The provider's model ID."`
	Name           string               `json:"name,omitempty" doc:"The model's name to show, if the provider gives it one."`
	Choice         string               `json:"choice" enum:"auto,chosen,none" doc:"auto: the OpenRouter model whose name matches; chosen: the one picked in the editor; none: no price, as picked."`
	OpenRouter     string               `json:"openrouter,omitempty" doc:"The OpenRouter model whose price it is; missing without one (auto found none, or none was picked)."`
	OpenRouterName string               `json:"openrouter_name,omitempty" doc:"OpenRouter's name of that model."`
	Price          *provider.ModelPrice `json:"price,omitempty" doc:"The price, USD per million tokens; missing without one, or when the chosen model isn't in the hub's copy of OpenRouter's list."`
}

// priceStatus is where the hub's copy of OpenRouter's list stands.
type priceStatus struct {
	Enabled   bool       `json:"enabled" doc:"The hub looks the prices up on OpenRouter."`
	FetchedAt *time.Time `json:"fetched_at,omitempty" doc:"When the hub last got OpenRouter's list; missing before it got one."`
	Models    int        `json:"models" doc:"How many models with prices the hub's copy of the list has."`
	Error     string     `json:"error,omitempty" doc:"Why the last try to get the list failed; the hub keeps the list it had."`
}

// openRouterModels is GET /api/providers/openrouter: OpenRouter models
// for the editor's picker.
type openRouterModels struct {
	Models    []openRouterModel `json:"models" nullable:"false" doc:"The models whose ID or name has every word of the query, in OpenRouter's order, at most 50."`
	FetchedAt *time.Time        `json:"fetched_at,omitempty" doc:"When the hub got the list they come from."`
}

// listedModels are the models a provider that lists its own listed.
type listedModels struct {
	// BaseURL is the provider's at the time: another URL is another
	// provider's list.
	BaseURL string           `json:"base_url"`
	Models  []provider.Model `json:"models"`
	At      time.Time        `json:"at"`
}

// priceCache is openrouter.json.
type priceCache struct {
	Version   int                     `json:"version"`
	FetchedAt time.Time               `json:"fetched_at"`
	Models    []openRouterModel       `json:"models"`
	Listed    map[string]listedModels `json:"listed,omitempty"`
}

// priceBook is the hub's copy of OpenRouter's list and of the models its
// providers listed.
type priceBook struct {
	// url is OpenRouter's list; empty looks no price up.
	url    string
	path   string
	client *http.Client
	logger *slog.Logger
	clock  quartz.Clock
	// refreshes makes refreshes at once share one.
	refreshes singleflight.Group

	mu        sync.Mutex
	index     openRouterIndex
	fetchedAt time.Time
	tried     time.Time
	lastErr   string
	listed    map[string]listedModels
}

// newPriceBook returns the price book kept beside statePath (in memory
// without one), with what it kept. A file it can't read is left for the
// next refresh to replace.
func newPriceBook(statePath, url string, client *http.Client, logger *slog.Logger, clock quartz.Clock) *priceBook {
	b := &priceBook{url: url, client: client, logger: logger, clock: clock, listed: map[string]listedModels{}}
	if statePath == "" {
		return b
	}
	b.path = filepath.Join(filepath.Dir(statePath), "openrouter.json")
	data, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return b
	}
	var cache priceCache
	if err == nil {
		err = json.Unmarshal(data, &cache)
	}
	if err != nil {
		logger.Warn("Failed to read the hub's copy of OpenRouter's prices; it gets them again", "path", b.path, "error", err)
		return b
	}
	b.index, b.fetchedAt = newOpenRouterIndex(cache.Models), cache.FetchedAt
	if cache.Listed != nil {
		b.listed = cache.Listed
	}
	return b
}

// enabled reports whether the hub looks prices up.
func (b *priceBook) enabled() bool { return b.url != "" }

// status is where the copy of OpenRouter's list stands.
func (b *priceBook) status() priceStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := priceStatus{Enabled: b.enabled(), Models: len(b.index.models), Error: b.lastErr}
	if !b.fetchedAt.IsZero() {
		at := b.fetchedAt
		s.FetchedAt = &at
	}
	return s
}

// run refreshes the prices when OpenRouter's list is a day old, or there
// is none, while the hub has providers, until ctx ends; after a failure it
// tries again an hour later.
func (b *priceBook) run(ctx context.Context, providers func() []provider.Provider) {
	if !b.enabled() {
		return
	}
	check := func() {
		list := providers()
		if len(list) == 0 {
			return
		}
		b.mu.Lock()
		now := b.clock.Now()
		due := now.Sub(b.fetchedAt) >= priceRefreshEvery && now.Sub(b.tried) >= priceRetryEvery
		b.mu.Unlock()
		if !due {
			return
		}
		if err := b.refresh(ctx, list); err != nil {
			b.logger.Warn("Couldn't get OpenRouter's prices; the hub keeps those it had and tries again in an hour", "error", err)
		}
	}
	check()
	ticker := b.clock.NewTicker(10*time.Minute, "hub", "prices")
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// refresh gets OpenRouter's list again and has the providers that list
// their own models list them, then keeps both. A list that fails leaves
// what the hub had; refresh returns OpenRouter's failure.
func (b *priceBook) refresh(ctx context.Context, providers []provider.Provider) error {
	_, err, _ := b.refreshes.Do("", func() (any, error) { return nil, b.refreshOnce(ctx, providers) })
	return err
}

func (b *priceBook) refreshOnce(ctx context.Context, providers []provider.Provider) error {
	b.mu.Lock()
	b.tried = b.clock.Now()
	b.mu.Unlock()
	var (
		models   []openRouterModel
		fetchErr error
		wg       sync.WaitGroup
		listedMu sync.Mutex
		listed   = map[string]listedModels{}
	)
	wg.Go(func() { models, fetchErr = fetchOpenRouter(ctx, b.client, b.url) })
	for _, p := range providers {
		if len(p.Models) > 0 {
			continue
		}
		wg.Go(func() {
			got, err := provider.ListModels(ctx, nil, p)
			if err != nil {
				b.logger.Warn("Failed to list a model provider's models for their prices", "provider", p.ID, "error", err)
				return
			}
			listedMu.Lock()
			defer listedMu.Unlock()
			listed[p.ID] = listedModels{BaseURL: p.OpenAIBaseURL(), Models: got, At: b.clock.Now()}
		})
	}
	wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()
	if fetchErr == nil {
		b.index, b.fetchedAt, b.lastErr = newOpenRouterIndex(models), b.clock.Now(), ""
	} else {
		b.lastErr = fetchErr.Error()
	}
	kept := make(map[string]listedModels, len(providers))
	for _, p := range providers {
		if l, ok := listed[p.ID]; ok {
			kept[p.ID] = l
		} else if l, ok := b.listed[p.ID]; ok && len(p.Models) == 0 && l.BaseURL == p.OpenAIBaseURL() {
			kept[p.ID] = l
		}
	}
	b.listed = kept
	if err := b.saveLocked(); err != nil {
		b.logger.Warn("Failed to keep OpenRouter's prices", "path", b.path, "error", err)
	}
	return fetchErr
}

// saveLocked writes openrouter.json.
func (b *priceBook) saveLocked() error {
	if b.path == "" {
		return nil
	}
	cache := priceCache{Version: priceCacheFormat, FetchedAt: b.fetchedAt, Models: b.index.models, Listed: b.listed}
	if cache.Models == nil {
		cache.Models = []openRouterModel{}
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return atomicfile.Write(b.path, ".openrouter.json-*", data)
}

// models are p's models to price: those it defines, else those it listed
// at the last refresh, then those choices name that neither has.
func (b *priceBook) modelsLocked(p provider.Provider, choices map[string]string) []provider.Model {
	models := p.Models
	if len(models) == 0 {
		if l, ok := b.listed[p.ID]; ok && l.BaseURL == p.OpenAIBaseURL() {
			models = l.Models
		}
	}
	models = slices.Clone(models)
	extra := []string{}
	for id := range choices {
		if !slices.ContainsFunc(models, func(m provider.Model) bool { return m.ID == id }) {
			extra = append(extra, id)
		}
	}
	slices.Sort(extra)
	for _, id := range extra {
		models = append(models, provider.Model{ID: id})
	}
	return models
}

// resolve returns the price of each of p's models, with how it was
// picked: by choices (the model's OpenRouter model, or none), else by its
// name.
func (b *priceBook) resolve(p provider.Provider, choices map[string]string) []providerModelPrice {
	b.mu.Lock()
	defer b.mu.Unlock()
	models := b.modelsLocked(p, choices)
	out := make([]providerModelPrice, 0, len(models))
	for _, m := range models {
		mp := providerModelPrice{Model: m.ID, Name: m.Name}
		var found openRouterModel
		ok := false
		switch choice := choices[m.ID]; choice {
		case openRouterNone:
			mp.Choice = string(priceNone)
		case "":
			mp.Choice = string(priceAuto)
			found, ok = b.index.match(m.ID)
		default:
			mp.Choice, mp.OpenRouter = string(priceChosen), choice
			found, ok = b.index.lookup(choice)
		}
		if ok {
			price := found.Price
			price.Source = openRouterSource + found.ID
			mp.OpenRouter, mp.OpenRouterName, mp.Price = found.ID, found.Name, &price
		}
		out = append(out, mp)
	}
	return out
}

// prices are the prices of p's models the hub found, by model ID, for the
// AgentAPIs.
func (b *priceBook) prices(p provider.Provider, choices map[string]string) map[string]provider.ModelPrice {
	var out map[string]provider.ModelPrice
	for _, mp := range b.resolve(p, choices) {
		if mp.Price == nil {
			continue
		}
		if out == nil {
			out = map[string]provider.ModelPrice{}
		}
		out[mp.Model] = *mp.Price
	}
	return out
}

// search returns the OpenRouter models whose ID or name has every word of
// query.
func (b *priceBook) search(query string) openRouterModels {
	b.mu.Lock()
	defer b.mu.Unlock()
	body := openRouterModels{Models: b.index.search(query, maxSearchResults)}
	if !b.fetchedAt.IsZero() {
		at := b.fetchedAt
		body.FetchedAt = &at
	}
	return body
}

// validChoices checks a provider's price choices: model IDs, each with an
// OpenRouter model ID or none.
func validChoices(providerID string, choices map[string]string) error {
	if len(choices) > maxPriceChoices {
		return fmt.Errorf("%w: %s has more than %d price choices", provider.ErrInvalid, providerID, maxPriceChoices)
	}
	for model, choice := range choices {
		if model == "" || len(model) > maxPriceModelID {
			return fmt.Errorf("%w: a price choice of %s has no model id, or one longer than %d", provider.ErrInvalid, providerID, maxPriceModelID)
		}
		if choice == "" || len(choice) > maxOpenRouterID || strings.ContainsFunc(choice, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
			return fmt.Errorf("%w: the price choice of %s's model %s must be an OpenRouter model ID or none", provider.ErrInvalid, providerID, model)
		}
	}
	return nil
}

// withPrices adds the prices of its providers' models to body, and where
// the hub's copy of OpenRouter's list stands.
func (h *Hub) withPrices(body *providersBody) {
	for i := range body.Providers {
		view := &body.Providers[i]
		view.ModelPrices = h.prices.resolve(view.Provider, view.OpenRouter)
	}
	body.Prices = h.prices.status()
}

// refreshPrices gets OpenRouter's prices again and has the providers that
// list their models list them (POST /api/providers/prices), then answers
// the providers as GET /api/providers does. OpenRouter failing still
// answers them, with the error in prices.error.
func (h *Hub) refreshPrices(w http.ResponseWriter, r *http.Request) {
	if !h.prices.enabled() {
		writeError(w, http.StatusConflict, "this hub doesn't look prices up on OpenRouter")
		return
	}
	list, _, _ := h.providers.state()
	if err := h.prices.refresh(context.WithoutCancel(r.Context()), list); err != nil {
		h.logger.Warn("Couldn't get OpenRouter's prices", "error", err)
	}
	h.getProviders(w, r)
}

// searchOpenRouter answers the OpenRouter models of the hub's copy whose
// ID or name has every word of q (GET /api/providers/openrouter?q=), for
// the editor's picker.
func (h *Hub) searchOpenRouter(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.prices.search(r.URL.Query().Get("q")))
}

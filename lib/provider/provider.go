// Package provider describes the model providers an agent can run on
// instead of its own: endpoints such as a LiteLLM gateway that speak the
// Anthropic Messages API (Claude Code) or the OpenAI Responses API (Codex).
// A provider is defined in ~/.agentapi/config.yaml or on the hub; a session
// switches to it like it switches models.
package provider

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/coder/agentapi/lib/agentstate"
)

// Default is the provider setting of an agent on its own provider: Claude
// Code's Anthropic login or API key, Codex's OpenAI login or its own
// config's model_provider. No provider can have the ID.
const Default = agentstate.ProviderDefault

// Provider is a model provider an agent can run on.
type Provider struct {
	ID   string `json:"id" yaml:"id" doc:"Identifies the provider: lowercase letters, digits, - and _."`
	Name string `json:"name,omitempty" yaml:"name,omitempty" doc:"Name to show; the ID when empty."`
	// BaseURL is the OpenAI-style base, which ends with /v1; Claude Code
	// gets it without the /v1, unless AnthropicBaseURL is set.
	BaseURL          string `json:"base_url" yaml:"base_url" doc:"Base URL of the provider's API, e.g. https://gateway.example.com/v1. Codex calls <base_url>/responses; Claude Code calls the same URL without /v1, plus /v1/messages."`
	AnthropicBaseURL string `json:"anthropic_base_url,omitempty" yaml:"anthropic_base_url,omitempty" doc:"Base URL for Claude Code when it isn't base_url without /v1."`
	// APIKey is a secret: the APIs don't return it.
	APIKey string  `json:"-" yaml:"api_key,omitempty"`
	Models []Model `json:"models,omitempty" yaml:"models,omitempty" nullable:"false" doc:"Models to offer; when empty, those the provider lists at <base_url>/models."`
	// Prices are the prices of the provider's models, by model ID: those
	// written in the config file, or those the hub resolved. The APIs
	// don't return them; the hub sends them sealed with the providers.
	Prices map[string]ModelPrice `json:"-" yaml:"prices,omitempty"`
}

// Model is a model a provider offers.
type Model struct {
	ID   string `json:"id" yaml:"id" doc:"Model ID the provider's API takes."`
	Name string `json:"name,omitempty" yaml:"name,omitempty" doc:"Name to show; the ID when empty."`
}

// UnmarshalYAML takes a model as its ID alone, or as a map.
func (m *Model) UnmarshalYAML(unmarshal func(any) error) error {
	var id string
	if err := unmarshal(&id); err == nil {
		*m = Model{ID: id}
		return nil
	}
	type plain Model
	return unmarshal((*plain)(m))
}

// DisplayName is the name to show.
func (m Model) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// DisplayName is the name to show.
func (p Provider) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ErrInvalid is wrapped by the errors of Validate.
var ErrInvalid = errors.New("invalid provider")

// Validate returns an ErrInvalid for a provider that can't be used.
func (p Provider) Validate() error {
	if !idPattern.MatchString(p.ID) {
		return fmt.Errorf("%w: id %q must be lowercase letters, digits, - and _", ErrInvalid, p.ID)
	}
	if p.ID == Default {
		return fmt.Errorf("%w: the id %q is reserved for the agent's own provider", ErrInvalid, Default)
	}
	for name, raw := range map[string]string{"base_url": p.BaseURL, "anthropic_base_url": p.AnthropicBaseURL} {
		if raw == "" && name == "anthropic_base_url" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: %s of %s must be an http or https URL", ErrInvalid, name, p.ID)
		}
	}
	for _, m := range p.Models {
		if strings.TrimSpace(m.ID) == "" {
			return fmt.Errorf("%w: a model of %s has no id", ErrInvalid, p.ID)
		}
	}
	for model, price := range p.Prices {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("%w: a price of %s has no model id", ErrInvalid, p.ID)
		}
		if err := price.validate(p.ID, model); err != nil {
			return err
		}
	}
	return nil
}

// OpenAIBaseURL is the base of the OpenAI-style API: base_url, with /v1
// added when it has no version.
func (p Provider) OpenAIBaseURL() string {
	base := strings.TrimRight(p.BaseURL, "/")
	if !versioned(base) {
		base += "/v1"
	}
	return base
}

// ClaudeBaseURL is the base Claude Code calls /v1/messages under:
// anthropic_base_url, else base_url without its /v1.
func (p Provider) ClaudeBaseURL() string {
	if p.AnthropicBaseURL != "" {
		return strings.TrimRight(p.AnthropicBaseURL, "/")
	}
	return strings.TrimSuffix(strings.TrimRight(p.BaseURL, "/"), "/v1")
}

// versioned reports whether base ends with an API version such as /v1 or
// /v1beta.
func versioned(base string) bool {
	i := strings.LastIndex(base, "/")
	return i >= 0 && regexp.MustCompile(`^v[0-9]+[a-z0-9]*$`).MatchString(base[i+1:])
}

// Model returns the provider's model with the id, if it lists it.
func (p Provider) Model(id string) (Model, bool) {
	i := slices.IndexFunc(p.Models, func(m Model) bool { return m.ID == id })
	if i < 0 {
		return Model{}, false
	}
	return p.Models[i], true
}

// Redacted is p without its API key, and whether it had one.
func (p Provider) Redacted() (Provider, bool) {
	hasKey := p.APIKey != ""
	p.APIKey = ""
	return p, hasKey
}

// Host is the host (and port) of the provider's API, to show.
func (p Provider) Host() string {
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return ""
	}
	return u.Host
}

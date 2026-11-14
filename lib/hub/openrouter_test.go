package hub

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/provider"
)

// fixtureModels are the models of testdata/openrouter-models.json, a part
// of OpenRouter's list as it answered in November 2026.
func fixtureModels(t *testing.T) []openRouterModel {
	t.Helper()
	data, err := os.ReadFile("testdata/openrouter-models.json")
	require.NoError(t, err)
	models, err := parseOpenRouter(data)
	require.NoError(t, err)
	return models
}

func TestNormalizeModel(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{
		"Kimi-K3-NVFP4":        "kimi-k3",
		"Kimi-K3.1-Pro-NVFP4":  "kimi-k3.1-pro",
		"moonshotai/kimi-k3":   "kimi-k3",
		"hf/org/Qwen4-32B_AWQ": "qwen4-32b",
		"glm-5-fp8":            "glm-5",
		"GLM-5_FP8":            "glm-5",
		"model-int4-gptq":      "model",
		"llama-bf16":           "llama",
		"deepseek-v4-fp16":     "deepseek-v4",
		"Kimi-K3-INT8":         "kimi-k3",
		// A suffix only when it is a part of its own.
		"kimi-k3nvfp4": "kimi-k3nvfp4",
		"fp8":          "fp8",
		" Kimi-K3 ":    "kimi-k3",
	} {
		assert.Equal(t, want, normalizeModel(id), id)
	}
}

func TestParseOpenRouter(t *testing.T) {
	t.Parallel()
	models := fixtureModels(t)
	byID := map[string]openRouterModel{}
	for _, m := range models {
		byID[m.ID] = m
	}
	assert.NotContains(t, byID, "openrouter/auto", "a router's price is -1: left out")
	assert.Equal(t, openRouterModel{
		ID: "moonshotai/kimi-k3", Name: "MoonshotAI: Kimi K3",
		Price: provider.ModelPrice{Input: 0.192, Output: 16, CacheRead: 0.182},
	}, byID["moonshotai/kimi-k3"], "per million tokens, without float noise")
	assert.Equal(t, provider.ModelPrice{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}, byID["anthropic/claude-sonnet-4.6"].Price)
	assert.Equal(t, provider.ModelPrice{}, byID["inclusionai/ling-3.2-flash:free"].Price, `"0": free`)
	assert.Equal(t, provider.ModelPrice{Input: 1, Output: 5, CacheRead: 0.05, CacheWrite: 1.25}, byID["openai/gpt-6.1-terra-pro"].Price, "the long-context overrides are left out")

	// Numbers too; a list without priced models isn't one to keep.
	got, err := parseOpenRouter([]byte(`{"data":[{"id":"a/b","pricing":{"prompt":0.000001,"completion":"0.000002"}},{"id":"c/d","pricing":{"prompt":"x"}}]}`))
	require.NoError(t, err)
	assert.Equal(t, []openRouterModel{{ID: "a/b", Name: "a/b", Price: provider.ModelPrice{Input: 1, Output: 2}}}, got)
	_, err = parseOpenRouter([]byte(`{"data":[]}`))
	assert.Error(t, err)
	_, err = parseOpenRouter([]byte(`<html>`))
	assert.Error(t, err)
}

func TestOpenRouterMatch(t *testing.T) {
	t.Parallel()
	index := newOpenRouterIndex(fixtureModels(t))
	for model, want := range map[string]string{
		"Kimi-K3-NVFP4":               "moonshotai/kimi-k3",
		"Kimi-K3.1-Pro-NVFP4":         "moonshotai/kimi-k3.1-pro",
		"kimi-k2.6":                   "moonshotai/kimi-k2.6",
		"anthropic/Claude-Sonnet-4.6": "anthropic/claude-sonnet-4.6",
		// Variants and aliases aren't matched by name.
		"kimi-latest":      "",
		"ling-3.2-flash":   "",
		"kimi-k3:batch":    "",
		"unknown-model-v1": "",
	} {
		got, ok := index.match(model)
		assert.Equal(t, want != "", ok, model)
		assert.Equal(t, want, got.ID, model)
	}
	// Two models of one name: neither.
	ambiguous := newOpenRouterIndex(append(fixtureModels(t), openRouterModel{ID: "someone/kimi-k3"}))
	_, ok := ambiguous.match("Kimi-K3-NVFP4")
	assert.False(t, ok)

	got, ok := index.lookup("moonshotai/kimi-k3:batch")
	assert.True(t, ok, "a variant can be chosen")
	assert.InDelta(t, 2.28, got.Price.Input, 1e-12)

	ids := func(models []openRouterModel) []string {
		out := []string{}
		for _, m := range models {
			out = append(out, m.ID)
		}
		return out
	}
	assert.Equal(t, []string{"moonshotai/kimi-k3.1-pro", "moonshotai/kimi-k3.1-pro:batch", "moonshotai/kimi-k3", "moonshotai/kimi-k3:batch"}, ids(index.search("KIMI k3", 50)))
	assert.Equal(t, []string{"moonshotai/kimi-k3.1-pro"}, ids(index.search("kimi k3", 1)))
	assert.Equal(t, []string{"anthropic/claude-sonnet-4.6"}, ids(index.search("sonnet", 50)))
	assert.Empty(t, index.search("nothing-like-it", 50))
	assert.Len(t, index.search("", 50), len(index.models))
}

package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/provider"
)

func TestHubSyncProof(t *testing.T) {
	const secret = "hub-secret"
	original := []provider.Provider{{ID: "original", BaseURL: "https://original.example.com/v1", APIKey: "original-key"}}
	incoming := []provider.Provider{{ID: "incoming", BaseURL: "https://incoming.example.com/v1", APIKey: "incoming-key"}}
	for _, tc := range []struct {
		name    string
		change  func(*provider.SyncResponse)
		wantErr string
	}{
		{name: "valid response"},
		{name: "old hub without proof", change: func(r *provider.SyncResponse) { r.Nonce, r.Proof = "", "" }, wantErr: "upgrade the hub"},
		{name: "missing proof", change: func(r *provider.SyncResponse) { r.Proof = "" }, wantErr: "no sync proof"},
		{name: "another nonce", change: func(r *provider.SyncResponse) { r.Nonce = "another-nonce" }, wantErr: "different sync request"},
		{name: "invalid proof", change: func(r *provider.SyncResponse) { r.Proof = "sha256=invalid" }, wantErr: "invalid sync proof"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var request provider.SyncRequest
				if json.Unmarshal(body, &request) != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				sealed, _ := provider.Seal(secret, incoming)
				response := provider.SyncResponse{
					Sealed: sealed,
					Nonce:  request.Nonce,
					Proof:  instanceapi.Sign(secret, request.Nonce, []byte(sealed)),
				}
				if tc.change != nil {
					tc.change(&response)
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			t.Cleanup(hub.Close)

			source := provider.NewSource("", nil)
			source.SetHub(original)
			before := source.List()
			cachePath := filepath.Join(t.TempDir(), "hub-providers.json")
			cacheBefore := []byte("cache before sync")
			require.NoError(t, os.WriteFile(cachePath, cacheBefore, 0o600))
			sync := &provider.HubSync{
				Source:    source,
				Hub:       func() (string, string) { return hub.URL + "/hooks", secret },
				CachePath: cachePath,
			}

			err := sync.Sync(context.Background())
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.Equal(t, before, source.List(), "a rejected answer changes no providers")
				cacheAfter, readErr := os.ReadFile(cachePath)
				require.NoError(t, readErr)
				assert.Equal(t, cacheBefore, cacheAfter, "a rejected answer changes no cache")
				return
			}
			require.NoError(t, err)
			got, ok := source.Lookup("incoming")
			require.True(t, ok)
			assert.Equal(t, "incoming-key", got.APIKey)
			cacheAfter, readErr := os.ReadFile(cachePath)
			require.NoError(t, readErr)
			assert.NotEqual(t, cacheBefore, cacheAfter)
			assert.NotContains(t, string(cacheAfter), "incoming-key")
		})
	}
}

func TestHubSyncRejectsReplay(t *testing.T) {
	const secret = "hub-secret"
	replayed := []provider.Provider{{ID: "replayed", BaseURL: "https://replayed.example.com/v1", APIKey: "replayed-key"}}
	var calls atomic.Int32
	first := make(chan provider.SyncResponse, 1)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			// Answer the second request with the authenticated response to the first.
			_ = json.NewEncoder(w).Encode(<-first)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request provider.SyncRequest
		if json.Unmarshal(body, &request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sealed, _ := provider.Seal(secret, replayed)
		response := provider.SyncResponse{
			Sealed: sealed,
			Nonce:  request.Nonce,
			Proof:  instanceapi.Sign(secret, request.Nonce, []byte(sealed)),
		}
		first <- response
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(hub.Close)

	source := provider.NewSource("", nil)
	cachePath := filepath.Join(t.TempDir(), "hub-providers.json")
	sync := &provider.HubSync{
		Source:    source,
		Hub:       func() (string, string) { return hub.URL + "/hooks", secret },
		CachePath: cachePath,
	}
	require.NoError(t, sync.Sync(context.Background()))
	_, ok := source.Lookup("replayed")
	require.True(t, ok)
	cacheBefore, err := os.ReadFile(cachePath)
	require.NoError(t, err)

	current := []provider.Provider{{ID: "current", BaseURL: "https://current.example.com/v1", APIKey: "current-key"}}
	source.SetHub(current)
	assert.ErrorContains(t, sync.Sync(context.Background()), "different sync request")
	assert.Equal(t, []provider.Listed{{Provider: current[0], Origin: provider.OriginHub}}, source.List())
	cacheAfter, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.Equal(t, cacheBefore, cacheAfter)
	assert.EqualValues(t, 2, calls.Load())
}

// New hubs add proof fields without changing Seal's plaintext or sealed field,
// so an old client can still ignore those fields and decrypt a new response.
func TestHubSyncResponseOldClientCompatibility(t *testing.T) {
	t.Parallel()
	providers := []provider.Provider{{ID: "kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-old-client"}}
	sealed, err := provider.Seal("hub-secret", providers)
	require.NoError(t, err)
	response := provider.SyncResponse{
		Sealed: sealed,
		Nonce:  "request-nonce",
		Proof:  instanceapi.Sign("hub-secret", "request-nonce", []byte(sealed)),
	}
	data, err := json.Marshal(response)
	require.NoError(t, err)
	var oldResponse struct {
		Sealed string `json:"sealed"`
	}
	require.NoError(t, json.Unmarshal(data, &oldResponse))
	opened, err := provider.Open("hub-secret", oldResponse.Sealed)
	require.NoError(t, err)
	assert.Equal(t, providers, opened)
}

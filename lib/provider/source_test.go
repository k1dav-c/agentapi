package provider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/provider"
)

func TestListModelsSameOriginRedirect(t *testing.T) {
	const key = "sk-synthetic"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("x-api-key") != key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/v1/redirected-models", http.StatusFound)
		case "/v1/redirected-models":
			if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("x-api-key") != key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"same-origin"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(api.Close)

	var redirects atomic.Int32
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(r *http.Request, _ []*http.Request) error {
			redirects.Add(1)
			r.Header.Set("X-Client-Hook", "called")
			return nil
		},
	}
	models, err := provider.ListModels(context.Background(), client, provider.Provider{ID: "p", BaseURL: api.URL + "/v1", APIKey: key})
	require.NoError(t, err)
	assert.Equal(t, []provider.Model{{ID: "same-origin"}}, models)
	assert.EqualValues(t, 1, redirects.Load(), "the client's redirect hook still runs")
	assert.Equal(t, 5*time.Second, client.Timeout)
}

func TestListModelsCrossOriginRedirectScrubsCredentials(t *testing.T) {
	const key = "sk-synthetic"
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"cross-origin"}]}`))
	}))
	t.Cleanup(destination.Close)
	redirecter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("x-api-key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, destination.URL+"/v1/models", http.StatusFound)
	}))
	t.Cleanup(redirecter.Close)

	var redirects atomic.Int32
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(r *http.Request, _ []*http.Request) error {
			redirects.Add(1)
			// Credentials a supplied hook adds must not cross the origin either.
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("x-api-key", key)
			return nil
		},
	}
	// localhost and 127.0.0.1 are different origins on the same local listener.
	baseURL := strings.Replace(redirecter.URL, "127.0.0.1", "localhost", 1) + "/v1"
	models, err := provider.ListModels(context.Background(), client, provider.Provider{ID: "p", BaseURL: baseURL, APIKey: key})
	require.NoError(t, err)
	assert.Equal(t, []provider.Model{{ID: "cross-origin"}}, models)
	assert.EqualValues(t, 1, redirects.Load(), "the client's redirect hook still runs")
}

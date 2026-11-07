package hub_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
)

type favoritesBody struct {
	Favorites []hub.Favorite `json:"favorites"`
}

func getFavorites(t *testing.T, hubURL string) []hub.Favorite {
	t.Helper()
	var body favoritesBody
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubURL+"/api/favorites", hubToken, nil, &body))
	require.NotNil(t, body.Favorites, "a list, empty or not")
	return body.Favorites
}

func putFavorites(t *testing.T, hubURL string, list any) (int, []hub.Favorite) {
	t.Helper()
	var body favoritesBody
	code := request(t, http.MethodPut, hubURL+"/api/favorites", hubToken, map[string]any{"favorites": list}, &body)
	return code, body.Favorites
}

// PUT /api/favorites replaces the favorites; the hub gives an ID to each
// one sent without, tells every signed-in page on its stream, and keeps
// them through a restart.
func TestHubFavorites(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	hubURL := startQuietHub(t, statePath)
	assert.Empty(t, getFavorites(t, hubURL))

	events := openEvents(t, hubURL+"/api/events")
	assert.Equal(t, "instances", next(t, events).event)
	first := next(t, events)
	assert.Equal(t, "favorites", first.event)
	assert.JSONEq(t, `{"favorites": []}`, first.data)

	code, saved := putFavorites(t, hubURL, []map[string]string{
		{"title": "  Review \n the diff ", "text": "\r\n\nReview the diff.\r\nList the risks.  \n\n"},
		{"id": "mine-1", "text": "Write the tests first."},
	})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, saved, 2)
	assert.NotEmpty(t, saved[0].ID, "an ID for one sent without")
	assert.Equal(t, "Review the diff", saved[0].Title, "one line")
	assert.Equal(t, "Review the diff.\nList the risks.", saved[0].Text)
	assert.Equal(t, hub.Favorite{ID: "mine-1", Text: "Write the tests first."}, saved[1])
	assert.Equal(t, saved, getFavorites(t, hubURL))

	changed := next(t, events)
	require.Equal(t, "favorites", changed.event)
	var pushed favoritesBody
	require.NoError(t, json.Unmarshal([]byte(changed.data), &pushed))
	assert.Equal(t, saved, pushed.Favorites)

	// Reordered by their IDs: the same favorites, the other way round.
	code, reordered := putFavorites(t, hubURL, []hub.Favorite{saved[1], saved[0]})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []hub.Favorite{saved[1], saved[0]}, reordered)
	assert.Equal(t, "favorites", next(t, events).event)

	// The same list again changes nothing: no event, the next one is the
	// next change.
	code, _ = putFavorites(t, hubURL, reordered)
	require.Equal(t, http.StatusOK, code)
	code, _ = putFavorites(t, hubURL, reordered[:1])
	require.Equal(t, http.StatusOK, code)
	var shortened favoritesBody
	require.NoError(t, json.Unmarshal([]byte(next(t, events).data), &shortened))
	assert.Equal(t, reordered[:1], shortened.Favorites)

	code, _ = putFavorites(t, hubURL, reordered)
	require.Equal(t, http.StatusOK, code)
	flushHub(t, hubURL)
	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var file struct {
		Version   int            `json:"version"`
		Favorites []hub.Favorite `json:"favorites"`
	}
	require.NoError(t, json.Unmarshal(data, &file))
	assert.Equal(t, 1, file.Version, "an older hub still reads it")
	assert.Equal(t, reordered, file.Favorites)

	again := startQuietHub(t, statePath)
	assert.Equal(t, reordered, getFavorites(t, again), "kept through a restart")

	// Signed in only.
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubURL+"/api/favorites", "", nil, nil))
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPut, hubURL+"/api/favorites", "", map[string]any{"favorites": []any{}}, nil))

	// Emptied, the list is [].
	code, emptied := putFavorites(t, hubURL, []any{})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []hub.Favorite{}, emptied)
}

// A list the hub refuses is 400, and changes nothing.
func TestHubRefusesFavorites(t *testing.T) {
	hubURL := startQuietHub(t, "")
	kept := []hub.Favorite{{ID: "a", Text: "keep me"}}
	code, _ := putFavorites(t, hubURL, kept)
	require.Equal(t, http.StatusOK, code)

	many := make([]map[string]string, 101)
	for n := range many {
		many[n] = map[string]string{"text": "x"}
	}
	for name, list := range map[string]any{
		"too many":       many,
		"empty text":     []map[string]string{{"text": " \n\t "}},
		"long title":     []map[string]string{{"title": strings.Repeat("t", 101), "text": "x"}},
		"long text":      []map[string]string{{"text": strings.Repeat("字", 20_001)}},
		"an ID twice":    []map[string]string{{"id": "a", "text": "x"}, {"id": "a", "text": "y"}},
		"a bad ID":       []map[string]string{{"id": "a b", "text": "x"}},
		"a long ID":      []map[string]string{{"id": strings.Repeat("a", 65), "text": "x"}},
		"not a list":     map[string]string{"text": "x"},
		"no list at all": nil,
	} {
		code, _ := putFavorites(t, hubURL, list)
		assert.Equal(t, http.StatusBadRequest, code, name)
	}
	assert.Equal(t, kept, getFavorites(t, hubURL))

	// At the limits.
	code, _ = putFavorites(t, hubURL, []map[string]string{{"title": strings.Repeat("t", 100), "text": strings.Repeat("字", 20_000)}})
	assert.Equal(t, http.StatusOK, code)
}

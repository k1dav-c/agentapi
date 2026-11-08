package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Favorite is a prompt someone keeps on the hub: the chat of an agent the
// hub's page opens inserts it in its composer. Title is optional; the page
// shows the text's first line without one.
type Favorite struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
}

// The limits of the favorites the hub keeps.
const (
	maxFavorites     = 100
	maxFavoriteTitle = 100    // characters
	maxFavoriteText  = 20_000 // characters
	maxFavoriteID    = 64
	// maxFavoritesBody is the largest PUT /api/favorites: 100 texts of
	// 20,000 characters, of three bytes each, and room for JSON.
	maxFavoritesBody = 8 << 20
)

// favoritesBody is GET and PUT /api/favorites, and the favorites event of
// the browser's stream, which tells of a change of the hub's settings: the
// favorites are all of them for now. The event keeps its name, so a page
// open from before a change of the hub's settings still reads it.
type favoritesBody struct {
	Favorites []Favorite `json:"favorites" nullable:"false"`
}

// favoritesError is a list of favorites the hub refuses, in words for the
// page.
type favoritesError string

func (e favoritesError) Error() string { return string(e) }

// validFavoriteID is the shape of an ID: letters, digits, - and _.
func validFavoriteID(id string) bool {
	if id == "" || len(id) > maxFavoriteID {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// newFavoriteID is the ID of a favorite sent without one.
func newFavoriteID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// normalizeFavorites checks the favorites a page sends and returns them as
// the hub keeps them: a title on one line, without its spaces around it; a
// text with \n line ends, without the blank lines before it or the spaces
// after it; an ID for each one sent without, and each ID once. The first
// favorite it refuses is a favoritesError.
func normalizeFavorites(list []Favorite) ([]Favorite, error) {
	if len(list) > maxFavorites {
		return nil, favoritesError(fmt.Sprintf("the hub keeps at most %d favorites", maxFavorites))
	}
	out := make([]Favorite, 0, len(list))
	seen := make(map[string]bool, len(list))
	for n, f := range list {
		which := fmt.Sprintf("favorite %d", n+1)
		f.Title = strings.Join(strings.Fields(f.Title), " ")
		if utf8.RuneCountInString(f.Title) > maxFavoriteTitle {
			return nil, favoritesError(fmt.Sprintf("%s: a title has at most %d characters", which, maxFavoriteTitle))
		}
		f.Text = strings.ReplaceAll(f.Text, "\r\n", "\n")
		f.Text = strings.TrimRightFunc(strings.TrimLeft(f.Text, "\r\n"), unicode.IsSpace)
		if f.Text == "" {
			return nil, favoritesError(which + ": the text is empty")
		}
		if utf8.RuneCountInString(f.Text) > maxFavoriteText {
			return nil, favoritesError(fmt.Sprintf("%s: a text has at most %d characters", which, maxFavoriteText))
		}
		if f.ID == "" {
			f.ID = newFavoriteID()
		} else if !validFavoriteID(f.ID) {
			return nil, favoritesError(fmt.Sprintf("%s: an ID has 1 to %d letters, digits, - or _", which, maxFavoriteID))
		}
		if seen[f.ID] {
			return nil, favoritesError(f.ID + " is listed twice")
		}
		seen[f.ID] = true
		out = append(out, f)
	}
	return out, nil
}

// getFavorites answers the favorites, in their order (GET /api/favorites).
func (h *Hub) getFavorites(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, favoritesBody{Favorites: h.store.getSettings().Favorites})
}

// setFavorites replaces the favorites (PUT /api/favorites): {"favorites":
// [{"id", "title", "text"}, …]} in their order. A favorite without an ID
// gets one; the answer is the list as the hub keeps it, which every page
// signed in gets on its stream too. A list the hub refuses is 400, and
// changes nothing.
func (h *Hub) setFavorites(w http.ResponseWriter, r *http.Request) {
	var body favoritesBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFavoritesBody)).Decode(&body); err != nil || body.Favorites == nil {
		writeError(w, http.StatusBadRequest, "send {\"favorites\": [{\"title\": ..., \"text\": ...}, ...]}")
		return
	}
	settings, err := h.store.updateSettings(func(s *hubSettings) error {
		list, err := normalizeFavorites(body.Favorites)
		s.Favorites = list
		return err
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, favoritesBody{Favorites: settings.Favorites})
}
